package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLoginTransport struct {
	requestErr    error
	signErr       error
	passwordErr   error
	challenge     string
	authorized    bool
	needsPassword bool
	session       []byte
	closed        bool
}

func (f *fakeLoginTransport) RequestCode(context.Context, string) (string, error) {
	return f.challenge, f.requestErr
}
func (f *fakeLoginTransport) SignIn(context.Context, string, string, string) (bool, bool, error) {
	return f.authorized, f.needsPassword, f.signErr
}
func (f *fakeLoginTransport) CheckPassword(context.Context, string) (bool, error) {
	return f.authorized, f.passwordErr
}
func (f *fakeLoginTransport) ExportSession(context.Context) ([]byte, error) {
	return append([]byte(nil), f.session...), nil
}
func (f *fakeLoginTransport) Close() error { f.closed = true; return nil }

func TestLoginFlowCodeLoginPersistsOnlyAuthorizedSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram.session")
	tr := &fakeLoginTransport{challenge: "challenge", authorized: true, session: []byte("authorized-session")}
	flow, err := NewLoginFlow(func(context.Context) (LoginTransport, error) { return tr, nil }, SessionFileStore{Path: path}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := flow.Begin(context.Background(), "+8801700000000"); err != nil {
		t.Fatal(err)
	}
	if err := flow.SubmitCode(context.Background(), "12345"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "authorized-session" {
		t.Fatalf("session=%q", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("session mode=%o", st.Mode().Perm())
	}
	if !tr.closed {
		t.Fatal("login transport was not closed")
	}
}

func TestLoginFlowSupportsTwoFactorPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram.session")
	tr := &fakeLoginTransport{challenge: "challenge", needsPassword: true, authorized: true, session: []byte("2fa-session")}
	flow, _ := NewLoginFlow(func(context.Context) (LoginTransport, error) { return tr, nil }, SessionFileStore{Path: path}, time.Minute)
	if err := flow.Begin(context.Background(), "+8801700000000"); err != nil {
		t.Fatal(err)
	}
	if err := flow.SubmitCode(context.Background(), "12345"); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("got %v", err)
	}
	if err := flow.SubmitPassword(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2fa-session" {
		t.Fatalf("session=%q", got)
	}
}

func TestLoginFlowExpiresPendingChallenge(t *testing.T) {
	tr := &fakeLoginTransport{challenge: "challenge"}
	flow, _ := NewLoginFlow(func(context.Context) (LoginTransport, error) { return tr, nil }, SessionFileStore{Path: filepath.Join(t.TempDir(), "session")}, time.Minute)
	now := time.Now()
	flow.now = func() time.Time { return now }
	if err := flow.Begin(context.Background(), "+8801700000000"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if err := flow.SubmitCode(context.Background(), "12345"); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("got %v", err)
	}
	if !tr.closed {
		t.Fatal("expired transport was not closed")
	}
}

func TestLoginFlowRejectsInvalidPhoneAndEmptyChallenge(t *testing.T) {
	tr := &fakeLoginTransport{challenge: ""}
	flow, _ := NewLoginFlow(func(context.Context) (LoginTransport, error) { return tr, nil }, SessionFileStore{Path: filepath.Join(t.TempDir(), "session")}, time.Minute)
	if err := flow.Begin(context.Background(), "01700000000"); err == nil {
		t.Fatal("expected invalid phone")
	}
	if err := flow.Begin(context.Background(), "+8801700000000"); err == nil {
		t.Fatal("expected empty challenge failure")
	}
	if !tr.closed {
		t.Fatal("failed transport was not closed")
	}
}

func TestLoginFlowDoesNotSaveSessionWhenAuthorizationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	tr := &fakeLoginTransport{challenge: "challenge", authorized: false, session: []byte("not-authorized")}
	flow, _ := NewLoginFlow(func(context.Context) (LoginTransport, error) { return tr, nil }, SessionFileStore{Path: path}, time.Minute)
	if err := flow.Begin(context.Background(), "+8801700000000"); err != nil {
		t.Fatal(err)
	}
	if err := flow.SubmitCode(context.Background(), "12345"); err == nil {
		t.Fatal("expected authorization failure")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session unexpectedly exists: %v", err)
	}
}
