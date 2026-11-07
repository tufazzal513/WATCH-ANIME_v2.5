package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"movex/internal/httpx"
	"movex/internal/repo"
)

const (
	presenceSaveEvery = 10 * time.Minute // how often "last seen" is persisted per user
	errorBodyLimit    = 4100             // a little over the 4000-byte report limit (the repo enforces the exact rule)
	defaultResetLimit = 3                // password resets per email per 24 h when the admin has not set one
	resetWindow       = 24 * time.Hour
	firebaseDefault   = "https://identitytoolkit.googleapis.com"
)

func (s *Server) privateRoutes() {
	s.Private("GET /api/v1/me", s.getMe)
	s.Private("PUT /api/v1/me", s.putMe)
	s.mux.Handle("DELETE /api/v1/me", s.api(http.HandlerFunc(s.deleteMe)))

	s.Private("GET /api/v1/me/list", s.getList)
	s.Private("PUT /api/v1/me/list/{movieId}", s.putListItem)
	s.Private("DELETE /api/v1/me/list/{movieId}", s.deleteListItem)

	s.Private("GET /api/v1/me/progress", s.getProgress)
	s.Private("PUT /api/v1/me/progress/{movieId}", s.putProgress)
	s.Private("DELETE /api/v1/me/progress/{movieId}", s.deleteProgress)

	s.Private("POST /api/v1/presence", s.postPresence)
	s.Private("DELETE /api/v1/presence", s.deletePresence)
	s.Private("POST /api/v1/broken-links", s.postBrokenLink)
	s.Private("POST /api/v1/errors", s.postError)
	s.Public("POST /api/v1/auth/password-reset", s.passwordReset)
}

// private user data is never cached by the browser or a proxy
func writePrivate(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, v)
}

// ---------- /me ----------

type meDTO struct {
	UID         string `json:"uid"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	PhotoURL    string `json:"photoURL"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"createdAt"`
	LastLoginAt int64  `json:"lastLoginAt"`
}

func toMe(u *repo.User) meDTO {
	return meDTO{u.UID, u.Email, u.Name, u.PhotoURL, u.Status, u.CreatedAt, u.LastLoginAt}
}

func (s *Server) ensureUser(ctx context.Context, p *Principal) (*repo.User, error) {
	u, _, err := s.deps.Repo.EnsureUser(ctx, repo.UserProfile{UID: p.UID, Email: p.Email, Name: p.Name, PhotoURL: p.Picture})
	return u, err
}

// GET /me is the "I just signed in" call: it creates the account record on the first login (status active)
// and refreshes last_login_at afterwards. Frozen and deleted accounts never get here (403 account_blocked).
func (s *Server) getMe(w http.ResponseWriter, r *http.Request, p *Principal) {
	u, err := s.ensureUser(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writePrivate(w, http.StatusOK, toMe(u))
}

// PUT /me changes the display name and photo only. Any other field in the body (status, email, ...) is ignored,
// so a user can never change their own status.
func (s *Server) putMe(w http.ResponseWriter, r *http.Request, p *Principal) {
	var body struct {
		Name     string `json:"name"`
		PhotoURL string `json:"photoURL"`
	}
	if !httpx.DecodeJSON(w, r, &body, 4<<10) {
		return
	}
	if _, err := s.ensureUser(r.Context(), p); err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.deps.Repo.UpdateProfile(r.Context(), p.UID, body.Name, body.PhotoURL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writePrivate(w, http.StatusOK, toMe(u))
}

// DELETE /me removes the caller's own record, but ONLY when an admin has marked the account deleted
// (same rule as the old database rules). The client calls this first and then deletes its Firebase account.
func (s *Server) deleteMe(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticateOpts(w, r, true)
	if !ok {
		return
	}
	if p.Status != repo.StatusDeleted {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "only an account that was deleted by an admin can be removed")
		return
	}
	if err := s.deps.Repo.PurgeUser(r.Context(), p.UID); err != nil && !errors.Is(err, repo.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- My List ----------

func movieIDParam(r *http.Request) (string, error) {
	id := r.PathValue("movieId")
	if id == "" || len(id) > 40 {
		return "", &repo.ValidationError{Field: "movieId", Msg: "is not a valid id"}
	}
	return id, nil
}

func (s *Server) getList(w http.ResponseWriter, r *http.Request, p *Principal) {
	items, err := s.deps.Repo.GetList(r.Context(), p.UID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		MovieID string `json:"movieId"`
		AddedAt int64  `json:"addedAt"`
	}
	out := make([]dto, 0, len(items))
	for _, it := range items {
		out = append(out, dto{it.MovieID, it.AddedAt})
	}
	writePrivate(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) putListItem(w http.ResponseWriter, r *http.Request, p *Principal) {
	id, err := movieIDParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ok, err := s.deps.Repo.IsPublicMovie(r.Context(), id); err != nil || !ok {
		s.failOrNotFound(w, r, err)
		return
	}
	if _, err := s.ensureUser(r.Context(), p); err != nil {
		s.fail(w, r, err)
		return
	}
	added, err := s.deps.Repo.AddToList(r.Context(), p.UID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writePrivate(w, http.StatusOK, map[string]any{"added": added})
}

func (s *Server) deleteListItem(w http.ResponseWriter, r *http.Request, p *Principal) {
	id, err := movieIDParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	removed, err := s.deps.Repo.RemoveFromList(r.Context(), p.UID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writePrivate(w, http.StatusOK, map[string]any{"removed": removed})
}

func (s *Server) failOrNotFound(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		err = repo.ErrNotFound
	}
	s.fail(w, r, err)
}

// ---------- progress ----------

func (s *Server) getProgress(w http.ResponseWriter, r *http.Request, p *Principal) {
	items, err := s.deps.Repo.GetProgress(r.Context(), p.UID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		MovieID string `json:"movieId"`
		Season  string `json:"season,omitempty"`
		Ep      *int   `json:"ep,omitempty"`
		T       int    `json:"t"`
		D       int    `json:"d"`
		At      int64  `json:"at"`
	}
	out := make([]dto, 0, len(items))
	for _, it := range items {
		out = append(out, dto{it.MovieID, it.Season, it.Ep, it.T, it.D, it.At})
	}
	writePrivate(w, http.StatusOK, map[string]any{"items": out})
}

// PUT /me/progress/{movieId} {"season":"Season 1","ep":2,"t":123.4,"d":3000}. t and d are required (as before).
func (s *Server) putProgress(w http.ResponseWriter, r *http.Request, p *Principal) {
	id, err := movieIDParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var body struct {
		Season string   `json:"season"`
		Ep     *int     `json:"ep"`
		T      *float64 `json:"t"`
		D      *float64 `json:"d"`
	}
	if !httpx.DecodeJSON(w, r, &body, 2<<10) {
		return
	}
	if body.T == nil || body.D == nil {
		s.fail(w, r, &repo.ValidationError{Field: "t", Msg: "t and d are required"})
		return
	}
	if *body.T < 0 || *body.T > 1e9 || *body.D <= 0 || *body.D > 1e9 { // also keeps the float->int conversion safe
		s.fail(w, r, &repo.ValidationError{Field: "d", Msg: "t and d must be seconds (d > 0)"})
		return
	}
	if ok, err := s.deps.Repo.IsPublicMovie(r.Context(), id); err != nil || !ok {
		s.failOrNotFound(w, r, err)
		return
	}
	if _, err := s.ensureUser(r.Context(), p); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deps.Repo.SetProgress(r.Context(), p.UID, id, body.Season, body.Ep, int(math.Round(*body.T)), int(math.Round(*body.D))); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProgress(w http.ResponseWriter, r *http.Request, p *Principal) {
	id, err := movieIDParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deps.Repo.DeleteProgress(r.Context(), p.UID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- presence ----------

// POST /presence {"name"?}: "I am here". Kept in memory; last-seen is saved to the user record every 10 minutes.
func (s *Server) postPresence(w http.ResponseWriter, r *http.Request, p *Principal) {
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 && !httpx.DecodeJSON(w, r, &body, 1<<10) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = p.Name
	}
	if s.deps.Presence != nil {
		s.deps.Presence.Touch(p.UID, name)
	}
	_ = s.deps.Repo.TouchLastSeen(r.Context(), p.UID, presenceSaveEvery)
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /presence: the tab closed.
func (s *Server) deletePresence(w http.ResponseWriter, r *http.Request, p *Principal) {
	if s.deps.Presence != nil {
		s.deps.Presence.Remove(p.UID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- broken links ----------

// POST /broken-links {"movieId","epInfo","serverType","url"}: one report per title; the same user can report the
// same title again only after an hour.
func (s *Server) postBrokenLink(w http.ResponseWriter, r *http.Request, p *Principal) {
	var body struct {
		MovieID    string `json:"movieId"`
		EpInfo     string `json:"epInfo"`
		ServerType string `json:"serverType"`
		URL        string `json:"url"`
	}
	if !httpx.DecodeJSON(w, r, &body, 8<<10) {
		return
	}
	if body.MovieID == "" || len(body.MovieID) > 40 {
		s.fail(w, r, &repo.ValidationError{Field: "movieId", Msg: "is required"})
		return
	}
	if ok, wait := s.brokenLimit.Allow(p.UID); !ok {
		httpx.TooMany(w, wait)
		return
	}
	if ok, err := s.deps.Repo.IsPublicMovie(r.Context(), body.MovieID); err != nil || !ok {
		s.failOrNotFound(w, r, err)
		return
	}
	if ok, _ := s.brokenDup.Allow(p.UID + "|" + body.MovieID); !ok {
		writePrivate(w, http.StatusOK, map[string]any{"ok": true, "counted": false})
		return
	}
	err := s.deps.Repo.ReportBrokenLink(r.Context(), repo.BrokenLinkInput{
		MovieID: body.MovieID, EpInfo: body.EpInfo, ServerType: body.ServerType, URL: body.URL, ReporterUID: p.UID,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writePrivate(w, http.StatusOK, map[string]any{"ok": true, "counted": true})
}

// ---------- client error reports ----------

// POST /errors with any small JSON object ({"message","source","line","stack",...}). At most 4000 bytes in total,
// 20 per minute per user, stored write-once. The panel is always "user" here.
func (s *Server) postError(w http.ResponseWriter, r *http.Request, p *Principal) {
	if ok, wait := s.errLimit.Allow(p.UID); !ok {
		httpx.TooMany(w, wait)
		return
	}
	var payload map[string]any
	if !httpx.DecodeJSON(w, r, &payload, errorBodyLimit) {
		return
	}
	if payload == nil || len(payload) > 30 {
		s.fail(w, r, &repo.ValidationError{Field: "payload", Msg: "must be a JSON object with at most 30 keys"})
		return
	}
	delete(payload, "ts") // the server sets these two
	delete(payload, "panel")
	if _, err := s.deps.Repo.LogError(r.Context(), p.UID, "user", payload); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- password reset ----------

func validEmail(e string) bool {
	if len(e) < 5 || len(e) > 254 || strings.ContainsAny(e, " \t\r\n,;<>\"") {
		return false
	}
	at := strings.LastIndexByte(e, '@')
	if at < 1 || strings.Count(e, "@") != 1 {
		return false
	}
	domain := e[at+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

// POST /auth/password-reset {"email"}. Public. Rate-limited per IP and per email (settings.pwdResetLimit per
// 24 h, default 3). The answer never depends on whether an account exists, so it cannot be used to find out
// who has an account: unknown address, disabled account and success all return the same 200.
func (s *Server) passwordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !httpx.DecodeJSON(w, r, &body, 2<<10) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(email) {
		s.fail(w, r, &repo.ValidationError{Field: "email", Msg: "enter a valid email address"})
		return
	}
	if ok, wait := s.resetLimit.Allow(httpx.ClientIP(r, s.deps.Cfg.TrustProxy)); !ok {
		httpx.TooMany(w, wait)
		return
	}

	limit := defaultResetLimit
	if kv, err := s.deps.Repo.GetSettings(r.Context()); err == nil {
		if n, err := strconv.Atoi(kv["pwdResetLimit"]); err == nil && n > 0 {
			limit = n
		}
	}
	key := repo.ResetKey(email)
	n, err := s.deps.Repo.CountResetAttempts(r.Context(), key, resetWindow)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n >= limit {
		w.Header().Set("Retry-After", strconv.Itoa(int(resetWindow.Seconds())))
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited",
			"Limit reached. You can only request a password reset "+strconv.Itoa(limit)+" times in 24 hours.")
		return
	}
	// Count the attempt before asking Firebase, so the limit applies equally to real and made-up addresses.
	if err := s.deps.Repo.AddResetAttempt(r.Context(), key); err != nil {
		s.fail(w, r, err)
		return
	}
	switch err := s.sendResetEmail(r.Context(), email); {
	case errors.Is(err, errFirebaseUnavailable):
		s.deps.Log.Error("password reset: Firebase unreachable", "id", httpx.RequestID(r), "err", err)
		httpx.WriteError(w, http.StatusServiceUnavailable, "unavailable", "Could not send the email right now. Please try again in a few minutes.")
		return
	case err != nil: // Firebase refused (unknown address, disabled account, its own throttling): never tell the caller
		s.deps.Log.Info("password reset: not sent", "id", httpx.RequestID(r), "reason", err.Error())
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true,
		"message": "If an account exists for this email, a reset link has been sent. Check your inbox and spam folder."})
}

var errFirebaseUnavailable = errors.New("firebase unavailable")

// sendResetEmail asks Firebase to mail the reset link. It returns errFirebaseUnavailable for transport errors
// and 5xx, and another error when Firebase answered with a refusal.
func (s *Server) sendResetEmail(ctx context.Context, email string) error {
	base := s.deps.FirebaseURL
	if base == "" {
		base = firebaseDefault
	}
	client := s.deps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	payload, _ := json.Marshal(map[string]string{"requestType": "PASSWORD_RESET", "email": email})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/accounts:sendOobCode?key="+s.deps.Cfg.FirebaseAPIKey, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return errors.Join(errFirebaseUnavailable, errors.New("request failed")) // never include the URL: it carries the API key
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode >= 500:
		return errors.Join(errFirebaseUnavailable, errors.New("HTTP "+strconv.Itoa(resp.StatusCode)))
	case resp.StatusCode != http.StatusOK:
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return errors.New("firebase: " + e.Error.Message)
	}
	return nil
}
