package backup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const pendingRestoreName = "restore.pending.db.gz"
const maxCompressedRestoreBytes int64 = 2 << 30

func gzipReader(r io.Reader) (*gzip.Reader, error) { return gzip.NewReader(r) }

// StageRestoreArchive stores a manually uploaded archive for validation and
// application during the next process start. It never changes the live DB.
func StageRestoreArchive(dataDir string, src io.Reader) (string, error) {
	if dataDir == "" || src == nil {
		return "", errors.New("restore staging requires DATA_DIR and an archive")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dataDir, pendingRestoreName)
	if _, err := os.Lstat(path); err == nil {
		return "", errors.New("a restore archive is already pending; cancel it before staging another")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp, err := os.CreateTemp(dataDir, ".restore-upload-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	n, copyErr := io.Copy(tmp, io.LimitReader(src, maxCompressedRestoreBytes+1))
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if n <= 0 || n > maxCompressedRestoreBytes {
		return "", fmt.Errorf("invalid compressed restore size: %d", n)
	}
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	// Check the gzip header now. Full SQLite validation happens at startup,
	// before the existing database is moved or replaced.
	f, err := os.Open(tmpPath)
	if err != nil {
		return "", err
	}
	gz, gzErr := gzipReader(f)
	if gzErr == nil {
		var expanded int64
		expanded, gzErr = io.Copy(io.Discard, io.LimitReader(gz, maxCompressedRestoreBytes+1))
		if gzErr == nil && (expanded == 0 || expanded > maxCompressedRestoreBytes) {
			gzErr = fmt.Errorf("invalid uncompressed restore size: %d bytes", expanded)
		}
		if closeErr := gz.Close(); gzErr == nil {
			gzErr = closeErr
		}
	}
	_ = f.Close()
	if gzErr != nil {
		return "", fmt.Errorf("invalid gzip restore archive: %w", gzErr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	if err := syncDir(dataDir); err != nil {
		return "", err
	}
	return path, nil
}

// ApplyPendingRestore validates the SQLite archive and atomically replaces the
// database before it is opened by the server. A failure leaves the pending file
// in place and prevents startup, so operators can fix or remove it deliberately.
func ApplyPendingRestore(ctx context.Context, dataDir, dbPath string, opts RestoreOptions) (bool, error) {
	path := filepath.Join(dataDir, pendingRestoreName)
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("pending restore is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	opts.DBPath = dbPath
	if opts.TempDir == "" {
		opts.TempDir = dataDir
	}
	if err := RestoreGzip(ctx, f, opts, false, "RESTORE"); err != nil {
		return false, fmt.Errorf("apply pending restore: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("restored database but could not remove pending archive: %w", err)
	}
	if err := syncDir(dataDir); err != nil {
		return false, err
	}
	return true, nil
}
