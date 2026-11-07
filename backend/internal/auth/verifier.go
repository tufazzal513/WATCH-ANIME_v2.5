// Package auth verifies Firebase Auth ID tokens (RS256 JWTs) against Google's published keys.
// It uses only the standard library.
package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultJWKSURL = "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"

var (
	// ErrInvalidToken covers every problem with the token itself (the caller answers 401 with one generic message).
	ErrInvalidToken = errors.New("invalid token")
	// ErrKeysUnavailable means Google's keys could not be loaded and none are cached (the caller answers 503).
	ErrKeysUnavailable = errors.New("token verification keys unavailable")
)

// Claims is what the application needs from a verified token.
type Claims struct {
	UID     string
	Email   string
	Name    string
	Picture string
	Expires time.Time
}

type Options struct {
	ProjectID   string
	DevInsecure bool // accept "dev:<uid>" tokens. TESTS ONLY (config refuses it in production mode)
	JWKSURL     string
	HTTPClient  *http.Client
	Now         func() time.Time // for tests
}

type Verifier struct {
	project string
	dev     bool
	url     string
	client  *http.Client
	now     func() time.Time

	mu         sync.Mutex // guards keys, keysExp, lastForced and serialises refreshes
	keys       map[string]*rsa.PublicKey
	keysExp    time.Time
	lastForced time.Time

	cmu   sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	claims Claims
	exp    time.Time
}

const (
	maxTokenLen     = 4096
	maxResultCache  = 5000
	resultCacheTTL  = 10 * time.Minute
	forceRefetchGap = time.Minute       // an unknown kid triggers at most one refetch per minute
	staleKeysGrace  = 12 * time.Hour    // keep using expired keys this long when Google cannot be reached
	maxIatSkew      = 300 * time.Second // iat may be at most 5 minutes in the future
	defaultKeysTTL  = time.Hour
	minKeysTTL      = time.Minute
	maxKeysTTL      = 24 * time.Hour
	jwksBodyLimit   = 1 << 20
)

func NewVerifier(o Options) *Verifier {
	v := &Verifier{project: o.ProjectID, dev: o.DevInsecure, url: o.JWKSURL, client: o.HTTPClient, now: o.Now, cache: map[string]cacheEntry{}}
	if v.url == "" {
		v.url = DefaultJWKSURL
	}
	if v.client == nil {
		v.client = &http.Client{Timeout: 10 * time.Second}
	}
	if v.now == nil {
		v.now = time.Now
	}
	return v
}

var devUIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Verify checks the token and returns its claims. Any token problem returns ErrInvalidToken.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	if token == "" || len(token) > maxTokenLen {
		return Claims{}, ErrInvalidToken
	}
	if v.dev && strings.HasPrefix(token, "dev:") {
		uid := strings.TrimPrefix(token, "dev:")
		if !devUIDRe.MatchString(uid) {
			return Claims{}, ErrInvalidToken
		}
		return Claims{UID: uid, Email: uid + "@dev.local", Name: uid, Expires: v.now().Add(time.Hour)}, nil
	}
	now := v.now()
	if c, ok := v.cached(token, now); ok {
		return c, nil
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	var p struct {
		Iss     string  `json:"iss"`
		Aud     string  `json:"aud"`
		Sub     string  `json:"sub"`
		Exp     float64 `json:"exp"`
		Iat     float64 `json:"iat"`
		Email   string  `json:"email"`
		Name    string  `json:"name"`
		Picture string  `json:"picture"`
	}
	if !decodePart(parts[0], &hdr) || !decodePart(parts[1], &p) {
		return Claims{}, ErrInvalidToken
	}
	if hdr.Alg != "RS256" || hdr.Kid == "" { // "none", HS256 and everything else is refused
		return Claims{}, ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) == 0 {
		return Claims{}, ErrInvalidToken
	}

	key, err := v.key(ctx, hdr.Kid)
	if err != nil {
		return Claims{}, err
	}
	if key == nil {
		return Claims{}, ErrInvalidToken
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		return Claims{}, ErrInvalidToken
	}

	exp := time.Unix(int64(p.Exp), 0)
	iat := time.Unix(int64(p.Iat), 0)
	switch {
	case p.Exp == 0 || !exp.After(now):
		return Claims{}, ErrInvalidToken
	case p.Iat == 0 || iat.After(now.Add(maxIatSkew)):
		return Claims{}, ErrInvalidToken
	case v.project == "" || p.Aud != v.project:
		return Claims{}, ErrInvalidToken
	case p.Iss != "https://securetoken.google.com/"+v.project:
		return Claims{}, ErrInvalidToken
	case p.Sub == "" || len(p.Sub) > 128:
		return Claims{}, ErrInvalidToken
	}
	c := Claims{UID: p.Sub, Email: p.Email, Name: p.Name, Picture: p.Picture, Expires: exp}
	v.store(token, c, now)
	return c, nil
}

func decodePart(s string, dst any) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && json.Unmarshal(b, dst) == nil
}

func (v *Verifier) cached(token string, now time.Time) (Claims, bool) {
	v.cmu.Lock()
	defer v.cmu.Unlock()
	e, ok := v.cache[token]
	if !ok || !e.exp.After(now) {
		delete(v.cache, token)
		return Claims{}, false
	}
	return e.claims, true
}

func (v *Verifier) store(token string, c Claims, now time.Time) {
	exp := now.Add(resultCacheTTL)
	if c.Expires.Before(exp) {
		exp = c.Expires
	}
	v.cmu.Lock()
	defer v.cmu.Unlock()
	if len(v.cache) >= maxResultCache {
		for k, e := range v.cache {
			if !e.exp.After(now) {
				delete(v.cache, k)
			}
		}
		if len(v.cache) >= maxResultCache {
			v.cache = map[string]cacheEntry{}
		}
	}
	v.cache[token] = cacheEntry{claims: c, exp: exp}
}

// key returns the public key for kid, refreshing Google's key set when needed.
// (nil, nil) means "the key set is fine but this kid is not in it".
func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if len(v.keys) == 0 || !now.Before(v.keysExp) {
		if err := v.refreshLocked(ctx, now); err != nil {
			// Google unreachable: keep serving from expired keys for a while instead of failing every login.
			if len(v.keys) == 0 || now.After(v.keysExp.Add(staleKeysGrace)) {
				return nil, fmt.Errorf("%w: %v", ErrKeysUnavailable, err)
			}
		}
	}
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	// unknown kid: Google may have rotated keys. Refetch, but at most once a minute.
	if now.Sub(v.lastForced) >= forceRefetchGap {
		v.lastForced = now
		if err := v.refreshLocked(ctx, now); err == nil {
			if k, ok := v.keys[kid]; ok {
				return k, nil
			}
		}
	}
	return nil, nil
}

func (v *Verifier) refreshLocked(ctx context.Context, now time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, jwksBodyLimit))
	if err != nil {
		return err
	}
	keys, err := ParseJWKS(body)
	if err != nil {
		return err
	}
	v.keys = keys
	v.keysExp = now.Add(keysTTL(resp.Header.Get("Cache-Control")))
	return nil
}

var maxAgeRe = regexp.MustCompile(`max-age=(\d+)`)

func keysTTL(cacheControl string) time.Duration {
	ttl := defaultKeysTTL
	if m := maxAgeRe.FindStringSubmatch(cacheControl); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			ttl = time.Duration(n) * time.Second
		}
	}
	if ttl < minKeysTTL {
		ttl = minKeysTTL
	}
	if ttl > maxKeysTTL {
		ttl = maxKeysTTL
	}
	return ttl
}

// ParseJWKS reads a JSON Web Key Set and returns its RSA signing keys by kid.
func ParseJWKS(b []byte) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(n) < 128 || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := 0
		for _, c := range e {
			exp = exp<<8 | int(c)
		}
		if exp < 3 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	if len(out) == 0 {
		return nil, errors.New("jwks: no usable RSA keys")
	}
	return out, nil
}
