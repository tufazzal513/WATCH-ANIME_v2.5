package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionFileStoreRoundTripAndPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "private", "telegram.session")
	s := SessionFileStore{Path: p}
	want := []byte("opaque-session-state")
	if err := s.StoreSession(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q", got)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", st.Mode().Perm())
	}
}

func TestSessionFileStoreRejectsEmptyAndOversized(t *testing.T) {
	s := SessionFileStore{Path: filepath.Join(t.TempDir(), "session")}
	if err := s.StoreSession(nil); err == nil {
		t.Fatal("empty session accepted")
	}
	if err := s.StoreSession(make([]byte, maxSessionBytes+1)); err == nil {
		t.Fatal("oversized session accepted")
	}
}

func TestSessionFileStoreRejectsSymlink(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d, "session")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	s := SessionFileStore{Path: link}
	if _, err := s.LoadSession(); err == nil {
		t.Fatal("symlink load accepted")
	}
	if err := s.StoreSession([]byte("replacement")); err == nil {
		t.Fatal("symlink overwrite accepted")
	}
}

func TestSessionFileStoreClear(t *testing.T) {
	s := SessionFileStore{Path: filepath.Join(t.TempDir(), "session")}
	if err := s.StoreSession([]byte("session")); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSession(); !os.IsNotExist(err) {
		t.Fatalf("LoadSession error = %v, want not-exist", err)
	}
}
