package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"movex/internal/auth"
	"movex/internal/httpx"
)

const (
	msgBadLogin   = "Invalid credentials, or this account has no admin access."
	msgThrottled  = "Too many sign-in attempts. Please wait a few minutes and try again."
	msgUnavail    = "The sign-in service is temporarily unavailable. Please try again in a minute."
	msgExpired    = "This page expired. Please sign in again."
	firebaseBase  = "https://identitytoolkit.googleapis.com"
	maxEmailChars = 254
	maxPassChars  = 4096
)

type loginData struct {
	Error string
	Email string
	Next  string
	Token string
}

func (a *Admin) renderLogin(w http.ResponseWriter, r *http.Request, status int, msg, email, next string) {
	c := &Ctx{W: w, R: r, Admin: a}
	c.Render("login", Page{Layout: "bare", Title: "Sign in", Status: status,
		Data: loginData{Error: msg, Email: email, Next: safeNext(next), Token: a.loginToken()}})
}

// GET /admin/login
func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.sessionFrom(r); ok {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	a.renderLogin(w, r, http.StatusOK, "", "", r.URL.Query().Get("next"))
}

// POST /admin/login. Every failure shows the same message, so the page cannot be used to find out which
// addresses have Firebase accounts or which of them are admins.
func (a *Admin) loginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyLimit)
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostForm.Get("email")))
	password := r.PostForm.Get("password")
	next := r.PostForm.Get("next")
	ip := httpx.ClientIP(r, a.deps.Cfg.TrustProxy)

	if !a.loginTokenOK(r.PostForm.Get("csrf")) {
		a.renderLogin(w, r, http.StatusBadRequest, msgExpired, email, next)
		return
	}
	okIP, waitIP := a.loginIP.Allow(ip)
	okMail, waitMail := a.loginEmail.Allow(email)
	if !okIP || !okMail {
		wait := max(waitIP, waitMail)
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		a.renderLogin(w, r, http.StatusTooManyRequests, msgThrottled, email, next)
		return
	}
	if email == "" || password == "" || len(email) > maxEmailChars || len(password) > maxPassChars {
		a.renderLogin(w, r, http.StatusUnauthorized, msgBadLogin, email, next)
		return
	}

	idToken, localID, err := a.firebaseSignIn(r.Context(), email, password)
	switch {
	case errors.Is(err, errSignInUnavailable):
		a.deps.Log.Error("admin login: Firebase unreachable", "id", httpx.RequestID(r))
		a.renderLogin(w, r, http.StatusServiceUnavailable, msgUnavail, email, next)
		return
	case err != nil:
		a.deps.Log.Warn("admin login: rejected", "id", httpx.RequestID(r), "ip", ip)
		a.renderLogin(w, r, http.StatusUnauthorized, msgBadLogin, email, next)
		return
	}
	claims, err := a.deps.Verifier.Verify(r.Context(), idToken)
	if err != nil || (localID != "" && claims.UID != localID) {
		if errors.Is(err, auth.ErrKeysUnavailable) {
			a.renderLogin(w, r, http.StatusServiceUnavailable, msgUnavail, email, next)
			return
		}
		a.deps.Log.Warn("admin login: token check failed", "id", httpx.RequestID(r), "ip", ip)
		a.renderLogin(w, r, http.StatusUnauthorized, msgBadLogin, email, next)
		return
	}
	if !a.admins[claims.UID] {
		a.deps.Log.Warn("admin login: not an admin", "id", httpx.RequestID(r), "ip", ip, "uid", claims.UID)
		a.renderLogin(w, r, http.StatusUnauthorized, msgBadLogin, email, next)
		return
	}

	sid, err := a.deps.Repo.CreateAdminSession(r.Context(), claims.UID, ip, r.UserAgent(), sessionTTL)
	if err != nil {
		a.deps.Log.Error("admin login: cannot create session", "id", httpx.RequestID(r), "err", err)
		a.renderLogin(w, r, http.StatusInternalServerError, msgUnavail, email, next)
		return
	}
	a.setSessionCookie(w, sid)
	_ = a.deps.Repo.Audit(r.Context(), claims.UID, "admin.login", "", ip)
	httpx.SetUID(r, claims.UID)
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
}

// POST /admin/logout deletes the server-side session, so a copied cookie stops working too.
func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if sess, ok := a.sessionFrom(r); ok {
		if !a.csrfOK(r, sess) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_ = a.deps.Repo.DeleteAdminSession(r.Context(), sess.ID)
		_ = a.deps.Repo.Audit(r.Context(), sess.UID, "admin.logout", "", httpx.ClientIP(r, a.deps.Cfg.TrustProxy))
	}
	a.clearSessionCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

var (
	errSignInUnavailable = errors.New("sign-in unavailable")
	errSignInRejected    = errors.New("sign-in rejected")
)

// firebaseSignIn checks the email and password with Firebase (accounts:signInWithPassword).
func (a *Admin) firebaseSignIn(ctx context.Context, email, password string) (idToken, localID string, err error) {
	base := a.deps.FirebaseURL
	if base == "" {
		base = firebaseBase
	}
	client := a.deps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	payload, _ := json.Marshal(map[string]any{"email": email, "password": password, "returnSecureToken": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/accounts:signInWithPassword?key="+a.deps.Cfg.FirebaseAPIKey, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", errSignInUnavailable // the URL contains the API key: never wrap the transport error
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 500 {
		return "", "", errSignInUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", errSignInRejected
	}
	var out struct {
		IDToken string `json:"idToken"`
		LocalID string `json:"localId"`
	}
	if json.Unmarshal(body, &out) != nil || out.IDToken == "" {
		return "", "", errSignInRejected
	}
	return out.IDToken, out.LocalID, nil
}
