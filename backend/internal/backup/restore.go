package backup

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SnapshotOpener opens a SQLite snapshot at path. Keeping the opener injectable
// lets the restore workflow be tested without a bundled SQLite driver.
type SnapshotOpener func(context.Context, string) (*sql.DB, error)

type RestoreOptions struct {
	DBPath                string
	TempDir               string
	CurrentSchema         int
	ExpectedSchemaVersion int
	RequiredTables        []string
	OpenSnapshot          SnapshotOpener
	BeforeReplace         func() error // stop DB users and close the live database
	AfterReplace          func() error // reopen the live database after replacement or recovery
	IsDatabaseEmpty       func(context.Context) (bool, error)
	ExpectedSHA256        string // SHA-256 of the uncompressed SQLite file, when supplied by trusted backup metadata.
	ExpectedBytes         int64  // Exact uncompressed SQLite size, when supplied by trusted backup metadata.
}

var ErrDatabaseNotEmpty = errors.New("automatic restore refused: database is not empty")
var ErrRestoreConfirmation = errors.New("manual restore requires the exact confirmation RESTORE")

// RestoreGzip validates a gzipped SQLite archive before replacing DBPath.
// Auto mode is strictly limited to an empty/uninitialized database.
func RestoreGzip(ctx context.Context, src io.Reader, opts RestoreOptions, automatic bool, confirmation string) error {
	if strings.TrimSpace(opts.DBPath) == "" || opts.OpenSnapshot == nil {
		return errors.New("restore requires DBPath and OpenSnapshot")
	}
	if opts.ExpectedSHA256 != "" {
		decoded, err := hex.DecodeString(opts.ExpectedSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("restore expected SHA-256 must be 64 hexadecimal characters")
		}
	}
	if opts.ExpectedBytes < 0 {
		return errors.New("restore expected byte count cannot be negative")
	}
	if (opts.BeforeReplace == nil) != (opts.AfterReplace == nil) {
		return errors.New("restore requires BeforeReplace and AfterReplace lifecycle hooks together")
	}
	if !automatic && confirmation != "RESTORE" {
		return ErrRestoreConfirmation
	}
	if automatic {
		if opts.IsDatabaseEmpty == nil {
			return errors.New("automatic restore requires an empty-database check")
		}
		empty, err := opts.IsDatabaseEmpty(ctx)
		if err != nil {
			return fmt.Errorf("check database before automatic restore: %w", err)
		}
		if !empty {
			return ErrDatabaseNotEmpty
		}
	}
	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o700); err != nil {
		return err
	}
	tmpDir := opts.TempDir
	if tmpDir == "" {
		tmpDir = filepath.Dir(opts.DBPath)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(tmpDir, ".movex-restore-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	// Bound both compressed input and decompressed output to avoid disk exhaustion.
	const maxSnapshotBytes = int64(2 << 30)
	const maxCompressedBytes = int64(2 << 30)
	compressed := &io.LimitedReader{R: src, N: maxCompressedBytes + 1}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("open bounded backup gzip: %w", err)
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(gz, maxSnapshotBytes+1))
	gzErr := gz.Close()
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if copyErr != nil {
		return fmt.Errorf("extract backup: %w", copyErr)
	}
	if n == 0 || n > maxSnapshotBytes {
		return fmt.Errorf("invalid snapshot size: %d bytes", n)
	}
	if compressed.N == 0 {
		return fmt.Errorf("compressed backup exceeds %d bytes", maxCompressedBytes)
	}
	if opts.ExpectedBytes > 0 && n != opts.ExpectedBytes {
		return fmt.Errorf("backup size mismatch: got %d bytes, expected %d", n, opts.ExpectedBytes)
	}
	if opts.ExpectedSHA256 != "" {
		actual := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(actual, opts.ExpectedSHA256) {
			return fmt.Errorf("backup SHA-256 mismatch: got %s", actual)
		}
	}
	if gzErr != nil {
		return fmt.Errorf("finish backup gzip: %w", gzErr)
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := validateSnapshot(ctx, tmpPath, opts); err != nil {
		return fmt.Errorf("backup validation failed: %w", err)
	}

	closed := false
	resume := func() error {
		if !closed || opts.AfterReplace == nil {
			return nil
		}
		closed = false
		return opts.AfterReplace()
	}
	if opts.BeforeReplace != nil {
		if err := opts.BeforeReplace(); err != nil {
			return fmt.Errorf("stop live database: %w", err)
		}
		closed = true
	}
	// Keep a local rollback copy before publishing the validated snapshot.
	bakPath := opts.DBPath + ".bak"
	oldExists := false
	if _, err := os.Stat(opts.DBPath); err == nil {
		oldExists = true
		if err := copyFile(opts.DBPath, bakPath); err != nil {
			resumeErr := resume()
			return errors.Join(fmt.Errorf("create pre-restore backup: %w", err), wrapResume(resumeErr))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		resumeErr := resume()
		return errors.Join(err, wrapResume(resumeErr))
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		resumeErr := resume()
		return errors.Join(err, wrapResume(resumeErr))
	}
	if err := os.Rename(tmpPath, opts.DBPath); err != nil {
		resumeErr := resume()
		return errors.Join(fmt.Errorf("publish restored database: %w", err), wrapResume(resumeErr))
	}
	if err := syncDir(filepath.Dir(opts.DBPath)); err != nil {
		// The rename may already be visible. Restore the previous file before reopening.
		rollbackErr := rollbackRestore(opts.DBPath, bakPath, oldExists)
		resumeErr := resume()
		return errors.Join(fmt.Errorf("sync restored database directory: %w", err), wrapResume(rollbackErr), wrapResume(resumeErr))
	}
	if opts.AfterReplace != nil {
		closed = false
		if err := opts.AfterReplace(); err != nil {
			rollbackErr := rollbackRestore(opts.DBPath, bakPath, oldExists)
			// AfterReplace may have partially reopened resources. Give the owner one
			// more chance to open the restored original after rollback.
			resumeErr := opts.AfterReplace()
			return errors.Join(fmt.Errorf("reopen restored database (rollback attempted): %w", err), wrapResume(rollbackErr), wrapResume(resumeErr))
		}
	}

	return nil
}

func validateSnapshot(ctx context.Context, path string, opts RestoreOptions) error {
	d, err := opts.OpenSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("open SQLite snapshot: %w", err)
	}
	defer d.Close()
	var integrity string
	if err := d.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("SQLite integrity_check: %s", integrity)
	}
	var version int
	if err := d.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > opts.CurrentSchema {
		return fmt.Errorf("snapshot schema %d is newer than server schema %d", version, opts.CurrentSchema)
	}
	if opts.ExpectedSchemaVersion > 0 && version != opts.ExpectedSchemaVersion {
		return fmt.Errorf("snapshot schema %d does not match backup metadata schema %d", version, opts.ExpectedSchemaVersion)
	}
	for _, table := range opts.RequiredTables {
		var count int
		if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("required table %q is missing", table)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".restore-safety-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
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
	return os.Rename(tmpPath, dst)
}

func wrapResume(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("recover live database: %w", err)
}

func rollbackRestore(dbPath, backupPath string, oldExists bool) error {
	if oldExists {
		if err := os.Rename(backupPath, dbPath); err != nil {
			return err
		}
	} else if err := os.Remove(dbPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(filepath.Dir(dbPath))
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// IsDatabaseFileEmpty reports whether a database path is safe for automatic
// first-boot restoration. Only a missing or zero-byte file is considered
// empty. An initialized SQLite file is never treated as empty, even when its
// application tables currently contain zero rows.
func IsDatabaseFileEmpty(_ context.Context, path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("database path is required for empty-database check")
	}
	st, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err == nil && !st.Mode().IsRegular() {
		return false, errors.New("database path is not a regular file")
	}
	if err == nil && st.Size() != 0 {
		return false, nil
	}
	// A crash may leave committed pages in a WAL even if the main database
	// file is absent or zero bytes. Never classify that state as empty.
	for _, suffix := range []string{"-wal", "-journal", "-shm"} {
		sidecar := path + suffix
		sidecarInfo, sidecarErr := os.Stat(sidecar)
		if sidecarErr == nil {
			if !sidecarInfo.Mode().IsRegular() || sidecarInfo.Size() > 0 {
				return false, nil
			}
		} else if !errors.Is(sidecarErr, os.ErrNotExist) {
			return false, sidecarErr
		}
	}
	return true, nil
}
