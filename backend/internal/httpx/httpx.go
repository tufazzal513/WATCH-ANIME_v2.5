// Package httpx holds small, dependency-free HTTP building blocks: JSON helpers, ETags, request ids,
// access log, panic recovery, CORS, gzip, body limits and a rate limiter.
package httpx

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

// ---------- JSON responses ----------

// APIError is the body of every error response: {"error": code, "message": text, "field": name?}.
type APIError struct {
	Code    string `json:"error"`
	Message string `json:"message,omitempty"`
	Field   string `json:"field,omitempty"`
	Status  string `json:"status,omitempty"` // account status for 403 account_blocked
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal"}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, APIError{Code: code, Message: message})
}

// ---------- ETag ----------

// ETagOf returns a weak ETag for a response body.
func ETagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// NotModified reports whether the request's If-None-Match matches etag (weak comparison, list and "*" supported).
func NotModified(r *http.Request, etag string) bool {
	inm := r.Header.Get("If-None-Match")
	if inm == "" {
		return false
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, c := range strings.Split(inm, ",") {
		c = strings.TrimSpace(c)
		if c == "*" || strings.TrimPrefix(c, "W/") == want {
			return true
		}
	}
	return false
}

// WriteJSONETag writes v as JSON with an ETag, or 304 Not Modified when the client already has it.
// Only for GET/HEAD. Cache-Control is "private, no-cache": the browser always revalidates (cheap with 304).
func WriteJSONETag(w http.ResponseWriter, r *http.Request, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	b = append(b, '\n')
	etag := ETagOf(b)
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, no-cache")
	if NotModified(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}

// ---------- request body ----------

// DecodeJSON reads one JSON value (at most max bytes). On failure it writes the error response and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, max int64) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "send application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, http.StatusRequestEntityTooLarge, "too_large", "request body is too large")
		} else {
			WriteError(w, http.StatusBadRequest, "bad_request", "body must be valid JSON")
		}
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) { // trailing data after the value
		WriteError(w, http.StatusBadRequest, "bad_request", "body must contain a single JSON value")
		return false
	}
	return true
}

// ---------- client IP ----------

// ClientIP returns the caller's IP. With trustProxy the RIGHT-most X-Forwarded-For entry is used: that is the
// address the (single) trusted proxy appended, so a client cannot forge it by sending its own header.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------- request id ----------

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyLogInfo
)

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
