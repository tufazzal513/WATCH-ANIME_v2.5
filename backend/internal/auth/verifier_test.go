package auth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const project = "movex-test"

type fakeGoogle struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	keys   map[string]*rsa.PrivateKey
	down   bool
	maxAge string
	hits   atomic.Int32
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{t: t, keys: map[string]*rsa.PrivateKey{}, maxAge: "3600"}
	g.addKey("k1")
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.hits.Add(1)
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		var ks []map[string]string
		for kid, k := range g.keys {
			ks = append(ks, map[string]string{
				"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
				"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes()),
			})
		}
		w.Header().Set("Cache-Control", "public, max-age="+g.maxAge+", must-revalidate")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": ks})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogle) addKey(kid string) *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		g.t.Fatal(err)
	}
	g.mu.Lock()
	g.keys[kid] = k
	g.mu.Unlock()
	return k
}

func (g *fakeGoogle) setDown(d bool) { g.mu.Lock(); g.down = d; g.mu.Unlock() }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

type claims map[string]any

func goodClaims(now time.Time) claims {
	return claims{
		"iss": "https://securetoken.google.com/" + project, "aud": project, "sub": "user-1",
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
		"email": "u@example.com", "name": "U Ser", "picture": "https://img.example.com/u.png",
	}
}

func (c claims) with(k string, v any) claims {
	out := claims{}
	for a, b := range c {
		out[a] = b
	}
	if v == nil {
		delete(out, k)
	} else {
		out[k] = v
	}
	return out
}

func sign(t *testing.T, key *rsa.PrivateKey, header map[string]any, c claims) string {
	t.Helper()
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(c)
	signing := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + b64(sig)
}

func rs256(kid string) map[string]any {
	return map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time          { return c.t }
func (c *clock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func setup(t *testing.T) (*Verifier, *fakeGoogle, *clock) {
	g := newFakeGoogle(t)
	ck := &clock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	return NewVerifier(Options{ProjectID: project, JWKSURL: g.srv.URL, Now: ck.Now}), g, ck
}

func TestValidToken(t *testing.T) {
	v, g, ck := setup(t)
	tok := sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t))
	c, err := v.Verify(context.Background(), tok)
	if err != nil || c.UID != "user-1" || c.Email != "u@example.com" || c.Name != "U Ser" || c.Picture == "" {
		t.Fatalf("%v %+v", err, c)
	}
	// second call comes from the result cache: no new JWKS hit, no new signature work needed
	hits := g.hits.Load()
	if _, err := v.Verify(context.Background(), tok); err != nil || g.hits.Load() != hits {
		t.Fatal("verified token should be served from cache")
	}
}

func TestRejectedTokens(t *testing.T) {
	v, g, ck := setup(t)
	good := goodClaims(ck.t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	ctx := context.Background()

	hs := func() string { // HS256 token "signed" with the public modulus as secret: the classic algorithm-confusion attack
		h, _ := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT", "kid": "k1"})
		p, _ := json.Marshal(good)
		s := b64(h) + "." + b64(p)
		m := hmac.New(sha256.New, g.keys["k1"].N.Bytes())
		m.Write([]byte(s))
		return s + "." + b64(m.Sum(nil))
	}
	none := func() string {
		h, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT", "kid": "k1"})
		p, _ := json.Marshal(good)
		return b64(h) + "." + b64(p) + "."
	}
	tampered := func() string { // valid signature of claims A, payload swapped for claims B
		parts := strings.Split(sign(t, g.keys["k1"], rs256("k1"), good), ".")
		p, _ := json.Marshal(good.with("sub", "admin"))
		return parts[0] + "." + b64(p) + "." + parts[2]
	}

	cases := map[string]string{
		"empty":            "",
		"garbage":          "not-a-token",
		"two parts":        "a.b",
		"four parts":       "a.b.c.d",
		"not base64 json":  "!!!.!!!.!!!",
		"alg none":         none(),
		"alg HS256":        hs(),
		"missing kid":      sign(t, g.keys["k1"], map[string]any{"alg": "RS256"}, good),
		"unknown kid":      sign(t, g.keys["k1"], rs256("nope"), good),
		"wrong key":        sign(t, other, rs256("k1"), good),
		"tampered payload": tampered(),
		"empty signature":  strings.Join(strings.Split(sign(t, g.keys["k1"], rs256("k1"), good), ".")[:2], ".") + ".",
		"expired":          sign(t, g.keys["k1"], rs256("k1"), good.with("exp", ck.t.Add(-time.Second).Unix())),
		"no exp":           sign(t, g.keys["k1"], rs256("k1"), good.with("exp", nil)),
		"iat in future":    sign(t, g.keys["k1"], rs256("k1"), good.with("iat", ck.t.Add(10*time.Minute).Unix())),
		"no iat":           sign(t, g.keys["k1"], rs256("k1"), good.with("iat", nil)),
		"wrong aud":        sign(t, g.keys["k1"], rs256("k1"), good.with("aud", "other-project")),
		"aud array":        sign(t, g.keys["k1"], rs256("k1"), good.with("aud", []string{project})),
		"wrong iss":        sign(t, g.keys["k1"], rs256("k1"), good.with("iss", "https://securetoken.google.com/other")),
		"google iss":       sign(t, g.keys["k1"], rs256("k1"), good.with("iss", "https://accounts.google.com")),
		"empty sub":        sign(t, g.keys["k1"], rs256("k1"), good.with("sub", "")),
		"long sub":         sign(t, g.keys["k1"], rs256("k1"), good.with("sub", strings.Repeat("x", 129))),
		"huge token":       strings.Repeat("a", maxTokenLen+1),
	}
	for name, tok := range cases {
		if _, err := v.Verify(ctx, tok); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: want ErrInvalidToken, got %v", name, err)
		}
	}
	// sanity: the untouched token is still fine
	if _, err := v.Verify(ctx, sign(t, g.keys["k1"], rs256("k1"), good.with("sub", strings.Repeat("x", 128)))); err != nil {
		t.Fatalf("128-char sub is allowed: %v", err)
	}
}

func TestClockEdges(t *testing.T) {
	v, g, ck := setup(t)
	ctx := context.Background()
	if _, err := v.Verify(ctx, sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t).with("iat", ck.t.Add(299*time.Second).Unix()))); err != nil {
		t.Fatalf("iat up to 300 s ahead is tolerated: %v", err)
	}
	tok := sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t).with("exp", ck.t.Add(30*time.Second).Unix()))
	if _, err := v.Verify(ctx, tok); err != nil {
		t.Fatal(err)
	}
	ck.Advance(31 * time.Second) // cached result must not outlive the token
	if _, err := v.Verify(ctx, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token must fail even though it was cached: %v", err)
	}
}

func TestKeyRotationRefetchIsRateLimited(t *testing.T) {
	v, g, ck := setup(t)
	ctx := context.Background()
	if _, err := v.Verify(ctx, sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t))); err != nil {
		t.Fatal(err)
	}
	k2 := g.addKey("k2") // Google rotates
	tok2 := sign(t, k2, rs256("k2"), goodClaims(ck.t).with("sub", "user-2"))
	if c, err := v.Verify(ctx, tok2); err != nil || c.UID != "user-2" {
		t.Fatalf("unknown kid must trigger a refetch and then verify: %v", err)
	}

	// attacker spams random kids: at most one refetch per minute
	before := g.hits.Load()
	for i := 0; i < 20; i++ {
		_, err := v.Verify(ctx, sign(t, g.keys["k1"], rs256(fmt.Sprintf("junk%d", i)), goodClaims(ck.t)))
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatal(err)
		}
	}
	if d := g.hits.Load() - before; d > 1 {
		t.Fatalf("expected at most 1 refetch in a minute, got %d", d)
	}
	ck.Advance(61 * time.Second)
	before = g.hits.Load()
	_, _ = v.Verify(ctx, sign(t, g.keys["k1"], rs256("junk-again"), goodClaims(ck.t)))
	if g.hits.Load()-before != 1 {
		t.Fatal("after a minute one more refetch is allowed")
	}
}

func TestKeyCacheHonoursMaxAge(t *testing.T) {
	v, g, ck := setup(t)
	g.maxAge = "300"
	ctx := context.Background()
	mk := func(sub string) string { return sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t).with("sub", sub)) }
	_, _ = v.Verify(ctx, mk("a"))
	_, _ = v.Verify(ctx, mk("b"))
	if g.hits.Load() != 1 {
		t.Fatalf("keys must be cached within max-age, hits=%d", g.hits.Load())
	}
	ck.Advance(301 * time.Second)
	_, _ = v.Verify(ctx, mk("c"))
	if g.hits.Load() != 2 {
		t.Fatalf("keys must be refreshed after max-age, hits=%d", g.hits.Load())
	}
}

func TestJWKSOutage(t *testing.T) {
	ctx := context.Background()

	// no cached keys at all -> "unavailable", not "invalid token"
	v, g, ck := setup(t)
	g.setDown(true)
	if _, err := v.Verify(ctx, sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t))); !errors.Is(err, ErrKeysUnavailable) || errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrKeysUnavailable, got %v", err)
	}

	// with cached keys the app keeps working through an outage, even after they expired
	v, g, ck = setup(t)
	g.maxAge = "60"
	if _, err := v.Verify(ctx, sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t))); err != nil {
		t.Fatal(err)
	}
	g.setDown(true)
	ck.Advance(2 * time.Hour)
	tok := sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t).with("sub", "user-9"))
	if c, err := v.Verify(ctx, tok); err != nil || c.UID != "user-9" {
		t.Fatalf("stale keys must be used during an outage: %v", err)
	}
	// ...but not forever
	ck.Advance(13 * time.Hour)
	tok = sign(t, g.keys["k1"], rs256("k1"), goodClaims(ck.t).with("sub", "user-10"))
	if _, err := v.Verify(ctx, tok); !errors.Is(err, ErrKeysUnavailable) {
		t.Fatalf("after the grace period: %v", err)
	}
	g.setDown(false) // recovery
	if _, err := v.Verify(ctx, tok); err != nil {
		t.Fatalf("must recover once Google is back: %v", err)
	}
}

func TestDevTokens(t *testing.T) {
	ctx := context.Background()
	off := NewVerifier(Options{ProjectID: project, JWKSURL: "http://127.0.0.1:1"})
	if _, err := off.Verify(ctx, "dev:admin1"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("dev tokens are refused unless explicitly enabled: %v", err)
	}
	on := NewVerifier(Options{ProjectID: project, DevInsecure: true, JWKSURL: "http://127.0.0.1:1"})
	c, err := on.Verify(ctx, "dev:admin1")
	if err != nil || c.UID != "admin1" || c.Email != "admin1@dev.local" {
		t.Fatalf("%v %+v", err, c)
	}
	for _, bad := range []string{"dev:", "dev:a b", "dev:../x", "dev:" + strings.Repeat("x", 65)} {
		if _, err := on.Verify(ctx, bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestParseJWKSSkipsUnusableKeys(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	n, e := b64(k.N.Bytes()), b64(big.NewInt(int64(k.E)).Bytes())
	set := fmt.Sprintf(`{"keys":[
		{"kty":"RSA","kid":"ok","n":"%s","e":"%s"},
		{"kty":"EC","kid":"ec","crv":"P-256","x":"AA","y":"AA"},
		{"kty":"RSA","kid":"wrongalg","alg":"RS512","n":"%s","e":"%s"},
		{"kty":"RSA","kid":"","n":"%s","e":"%s"},
		{"kty":"RSA","kid":"tiny","n":"AQAB","e":"AQAB"}]}`, n, e, n, e, n, e)
	keys, err := ParseJWKS([]byte(set))
	if err != nil || len(keys) != 1 || keys["ok"] == nil {
		t.Fatalf("%v %v", err, keys)
	}
	if _, err := ParseJWKS([]byte(`{"keys":[]}`)); err == nil {
		t.Fatal("empty set is an error")
	}
	if _, err := ParseJWKS([]byte(`nope`)); err == nil {
		t.Fatal("bad json is an error")
	}
}

func TestKeysTTL(t *testing.T) {
	cases := map[string]time.Duration{
		"public, max-age=19000, must-revalidate": 19000 * time.Second,
		"max-age=5":                              time.Minute,
		"max-age=99999999":                       24 * time.Hour,
		"no-store":                               time.Hour,
		"":                                       time.Hour,
	}
	for in, want := range cases {
		if got := keysTTL(in); got != want {
			t.Errorf("keysTTL(%q) = %v, want %v", in, got, want)
		}
	}
}
