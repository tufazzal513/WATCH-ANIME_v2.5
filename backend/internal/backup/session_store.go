package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SessionFileStore persists the MTProto user session separately from the
// database and backup archive. Session material is sensitive: files are
// created with mode 0600 and writes are published atomically.
type SessionFileStore struct {
	Path string
}

const maxSessionBytes = 4 << 20

func (s SessionFileStore) LoadSession() ([]byte, error) {
	if s.Path == "" {
		return nil, errors.New("Telegram session path is empty")
	}
	st, err := os.Lstat(s.Path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Telegram session path must be a regular file, not a symlink")
	}
	if st.Size() > maxSessionBytes {
		return nil, fmt.Errorf("Telegram session exceeds %d bytes", maxSessionBytes)
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("Telegram session file is empty")
	}
	// Tighten permissions on files created by older versions.
	if err := os.Chmod(s.Path, 0o600); err != nil {
		return nil, fmt.Errorf("secure Telegram session permissions: %w", err)
	}
	return b, nil
}

func (s SessionFileStore) StoreSession(data []byte) error {
	if s.Path == "" {
		return errors.New("Telegram session path is empty")
	}
	if len(data) == 0 {
		return errors.New("refusing to store an empty Telegram session")
	}
	if len(data) > maxSessionBytes {
		return fmt.Errorf("Telegram session exceeds %d bytes", maxSessionBytes)
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Telegram session directory: %w", err)
	}
	if st, err := os.Lstat(s.Path); err == nil {
		if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("Telegram session path must be a regular file, not a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".telegram-session-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return fmt.Errorf("publish Telegram session: %w", err)
	}
	return nil
}

func (s SessionFileStore) ClearSession() error {
	if s.Path == "" {
		return errors.New("Telegram session path is empty")
	}
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
