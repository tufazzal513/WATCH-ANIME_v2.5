package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type restoreConnector struct{}

func (restoreConnector) Connect(context.Context) (driver.Conn, error) { return restoreConn{}, nil }
func (restoreConnector) Driver() driver.Driver                        { return restoreDriver{} }

type restoreDriver struct{}

func (restoreDriver) Open(string) (driver.Conn, error) { return restoreConn{}, nil }

type restoreConn struct{}

func (restoreConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (restoreConn) Close() error                        { return nil }
func (restoreConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }
func (restoreConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	switch q {
	case "PRAGMA integrity_check":
		return &restoreRows{cols: []string{"integrity_check"}, values: []driver.Value{"ok"}}, nil
	case "SELECT COALESCE(MAX(version),0) FROM schema_migrations":
		return &restoreRows{cols: []string{"version"}, values: []driver.Value{int64(4)}}, nil
	case "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?":
		return &restoreRows{cols: []string{"count"}, values: []driver.Value{int64(1)}}, nil
	default:
		return nil, errors.New("unexpected query: " + q)
	}
}

type restoreRows struct {
	cols   []string
	values []driver.Value
	sent   bool
}

func (r *restoreRows) Columns() []string { return r.cols }
func (r *restoreRows) Close() error      { return nil }
func (r *restoreRows) Next(dst []driver.Value) error {
	if r.sent {
		return io.EOF
	}
	r.sent = true
	copy(dst, r.values)
	return nil
}

func validRestoreOpts(path string) RestoreOptions {
	return RestoreOptions{DBPath: path, CurrentSchema: 4, RequiredTables: []string{"movies"}, OpenSnapshot: func(context.Context, string) (*sql.DB, error) { return sql.OpenDB(restoreConnector{}), nil }}
}
func gzipBytes(t *testing.T, raw string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, err := z.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestRestoreRequiresExactManualConfirmation(t *testing.T) {
	opts := validRestoreOpts(filepath.Join(t.TempDir(), "movex.db"))
	err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "snapshot")), opts, false, "restore")
	if !errors.Is(err, ErrRestoreConfirmation) {
		t.Fatalf("got %v", err)
	}
}
func TestAutomaticRestoreNeverOverwritesNonEmptyDB(t *testing.T) {
	opts := validRestoreOpts(filepath.Join(t.TempDir(), "movex.db"))
	opts.IsDatabaseEmpty = func(context.Context) (bool, error) { return false, nil }
	err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "snapshot")), opts, true, "")
	if !errors.Is(err, ErrDatabaseNotEmpty) {
		t.Fatalf("got %v", err)
	}
}
func TestRestoreValidatesBeforeReplacingLiveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movex.db")
	if err := os.WriteFile(path, []byte("old database"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := validRestoreOpts(path)
	opts.BeforeReplace = func() error { return nil }
	opts.AfterReplace = func() error { return nil }
	if err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "validated snapshot")), opts, false, "RESTORE"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "validated snapshot" {
		t.Fatalf("restored content %q", got)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(bak) != "old database" {
		t.Fatalf("safety backup %q", bak)
	}
}
func TestRestoreRejectsInvalidGzipWithoutTouchingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movex.db")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := validRestoreOpts(path)
	if err := RestoreGzip(context.Background(), bytes.NewReader([]byte("not gzip")), opts, false, "RESTORE"); err == nil {
		t.Fatal("expected invalid gzip error")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Fatalf("live DB changed: %q", got)
	}
}

func TestRestoreReopenFailureRollsBackOriginalDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movex.db")
	if err := os.WriteFile(path, []byte("original database"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := validRestoreOpts(path)
	calls := 0
	opts.BeforeReplace = func() error { return nil }
	opts.AfterReplace = func() error {
		calls++
		if calls == 1 {
			return errors.New("simulated reopen failure")
		}
		return nil
	}
	err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "replacement database")), opts, false, "RESTORE")
	if err == nil {
		t.Fatal("expected reopen failure")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "original database" {
		t.Fatalf("rollback left %q", got)
	}
	if calls != 2 {
		t.Fatalf("expected recovery reopen attempt, got %d calls", calls)
	}
	if _, statErr := os.Stat(path + ".bak"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rollback backup should have been consumed, stat err=%v", statErr)
	}
}

func TestRestoreReopensLiveDatabaseWhenSafetyCopyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database-directory")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	opts := validRestoreOpts(path)
	beforeCalls, afterCalls := 0, 0
	opts.BeforeReplace = func() error { beforeCalls++; return nil }
	opts.AfterReplace = func() error { afterCalls++; return nil }
	err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "snapshot")), opts, false, "RESTORE")
	if err == nil {
		t.Fatal("expected safety-copy failure")
	}
	if beforeCalls != 1 || afterCalls != 1 {
		t.Fatalf("database lifecycle not recovered: before=%d after=%d", beforeCalls, afterCalls)
	}
}

func TestRestoreRequiresPairedDatabaseLifecycleHooks(t *testing.T) {
	opts := validRestoreOpts(filepath.Join(t.TempDir(), "movex.db"))
	opts.BeforeReplace = func() error { return nil }
	err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "snapshot")), opts, false, "RESTORE")
	if err == nil {
		t.Fatal("expected lifecycle hook validation error")
	}
}

func TestStageRestoreArchiveAndApplyOnlyAtStartup(t *testing.T) {
	dataDir := t.TempDir()
	archive := gzipBytes(t, "validated database snapshot")
	staged, err := StageRestoreArchive(dataDir, bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(staged) != pendingRestoreName {
		t.Fatalf("unexpected staged path %q", staged)
	}
	live := filepath.Join(dataDir, "movex.db")
	if err := os.WriteFile(live, []byte("existing live database"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != "existing live database" {
		t.Fatal("staging changed live database")
	}
	opts := validRestoreOpts(live)
	applied, err := ApplyPendingRestore(context.Background(), dataDir, live, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("pending restore was not applied")
	}
	got, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "validated database snapshot" {
		t.Fatalf("restored content %q", got)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending archive remains: %v", err)
	}
}

func TestStageRestoreRejectsInvalidGzipAndPreservesExistingPending(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, pendingRestoreName)
	if err := os.WriteFile(path, []byte("existing pending"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StageRestoreArchive(dataDir, bytes.NewReader([]byte("not gzip"))); err == nil {
		t.Fatal("expected invalid gzip error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing pending" {
		t.Fatalf("existing pending archive changed: %q", got)
	}
}

func TestApplyPendingRestoreLeavesArchiveWhenSnapshotValidationFails(t *testing.T) {
	dataDir := t.TempDir()
	staged, err := StageRestoreArchive(dataDir, bytes.NewReader(gzipBytes(t, "invalid sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dataDir, "movex.db")
	if err := os.WriteFile(live, []byte("live database"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := validRestoreOpts(live)
	opts.OpenSnapshot = func(context.Context, string) (*sql.DB, error) { return nil, errors.New("not a SQLite database") }
	if _, err := ApplyPendingRestore(context.Background(), dataDir, live, opts); err == nil {
		t.Fatal("expected validation failure")
	}
	got, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "live database" {
		t.Fatalf("live DB changed: %q", got)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("pending archive should remain for recovery: %v", err)
	}
}

func TestRestoreVerifiesUncompressedSHA256AndSizeBeforeReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movex.db")
	if err := os.WriteFile(path, []byte("existing database"), 0600); err != nil {
		t.Fatal(err)
	}
	payload := []byte("trusted snapshot bytes")
	sum := sha256.Sum256(payload)
	opts := validRestoreOpts(path)
	opts.ExpectedSHA256 = hex.EncodeToString(sum[:])
	opts.ExpectedBytes = int64(len(payload))
	opts.BeforeReplace = func() error { return nil }
	opts.AfterReplace = func() error { return nil }
	if err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, string(payload))), opts, false, "RESTORE"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("restored bytes = %q, want %q", got, payload)
	}
}

func TestRestoreRejectsHashOrSizeMismatchWithoutReplacingDatabase(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*RestoreOptions)
	}{
		{name: "hash", set: func(o *RestoreOptions) { o.ExpectedSHA256 = strings.Repeat("0", 64) }},
		{name: "size", set: func(o *RestoreOptions) { o.ExpectedBytes = 999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "movex.db")
			if err := os.WriteFile(path, []byte("live database"), 0600); err != nil {
				t.Fatal(err)
			}
			opts := validRestoreOpts(path)
			tc.set(&opts)
			if err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "incoming snapshot")), opts, false, "RESTORE"); err == nil {
				t.Fatal("expected backup metadata mismatch")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "live database" {
				t.Fatalf("live database changed: %q", got)
			}
		})
	}
}

func TestRestoreRejectsMalformedExpectedHash(t *testing.T) {
	opts := validRestoreOpts(filepath.Join(t.TempDir(), "movex.db"))
	opts.ExpectedSHA256 = "not-a-sha256"
	if err := RestoreGzip(context.Background(), bytes.NewReader(gzipBytes(t, "snapshot")), opts, false, "RESTORE"); err == nil {
		t.Fatal("expected malformed hash to be rejected")
	}
}

func TestIsDatabaseFileEmptyOnlyAcceptsMissingOrZeroByteFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	empty, err := IsDatabaseFileEmpty(context.Background(), missing)
	if err != nil || !empty {
		t.Fatalf("missing database: empty=%v err=%v", empty, err)
	}
	zero := filepath.Join(dir, "zero.db")
	if err := os.WriteFile(zero, nil, 0600); err != nil {
		t.Fatal(err)
	}
	empty, err = IsDatabaseFileEmpty(context.Background(), zero)
	if err != nil || !empty {
		t.Fatalf("zero-byte database: empty=%v err=%v", empty, err)
	}
	initialized := filepath.Join(dir, "initialized.db")
	if err := os.WriteFile(initialized, []byte("SQLite format 3\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	empty, err = IsDatabaseFileEmpty(context.Background(), initialized)
	if err != nil || empty {
		t.Fatalf("initialized database must be protected: empty=%v err=%v", empty, err)
	}
}

type fakeTelegramRestore struct {
	backups []RemoteBackup
	archive []byte
	err     error
}

func (f fakeTelegramRestore) ListRestoreDocuments(context.Context) ([]RemoteBackup, error) {
	return f.backups, f.err
}
func (f fakeTelegramRestore) DownloadRestoreDocument(_ context.Context, _ int64, dst io.Writer) error {
	if f.err != nil {
		return f.err
	}
	_, err := dst.Write(f.archive)
	return err
}

func TestParseTelegramBackupMetadata(t *testing.T) {
	payload := []byte("sqlite snapshot")
	sum := sha256.Sum256(payload)
	caption, err := json.Marshal(TelegramBackupMetadata{
		At: time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC), SchemaVersion: 4,
		UncompressedBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := ParseTelegramBackupMetadata(string(caption))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.SchemaVersion != 4 || metadata.UncompressedBytes != int64(len(payload)) {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if _, err := ParseTelegramBackupMetadata(`{"at":"2026-11-07T03:00:00Z","schema_version":4,"uncompressed_bytes":100,"sha256":"bad"}`); err == nil {
		t.Fatal("invalid SHA-256 accepted")
	}
}

func TestRestoreLatestFromTelegramUsesNewestAndVerifiesHash(t *testing.T) {
	payload := []byte("newest verified SQLite snapshot")
	sum := sha256.Sum256(payload)
	at := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	remote := fakeTelegramRestore{
		backups: []RemoteBackup{
			{MessageID: 10, Filename: "movex-old.db.gz", Metadata: TelegramBackupMetadata{At: at.Add(-time.Hour), SchemaVersion: 4, UncompressedBytes: 3, SHA256: strings.Repeat("a", 64)}},
			{MessageID: 11, Filename: "movex-new.db.gz", Metadata: TelegramBackupMetadata{At: at, SchemaVersion: 4, UncompressedBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}},
		},
		archive: gzipBytes(t, string(payload)),
	}
	path := filepath.Join(t.TempDir(), "movex.db")
	opts := validRestoreOpts(path)
	opts.IsDatabaseEmpty = func(context.Context) (bool, error) { return true, nil }
	got, err := RestoreLatestFromTelegram(context.Background(), remote, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID != 11 {
		t.Fatalf("restored message %d, want newest message 11", got.MessageID)
	}
	dbBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dbBytes, payload) {
		t.Fatalf("restored %q, want %q", dbBytes, payload)
	}
}

func TestRestoreLatestFromTelegramRefusesExistingDatabase(t *testing.T) {
	payload := []byte("snapshot")
	sum := sha256.Sum256(payload)
	remote := fakeTelegramRestore{
		backups: []RemoteBackup{{MessageID: 21, Filename: "movex.db.gz", Metadata: TelegramBackupMetadata{At: time.Now().UTC(), SchemaVersion: 4, UncompressedBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}}},
		archive: gzipBytes(t, string(payload)),
	}
	path := filepath.Join(t.TempDir(), "movex.db")
	if err := os.WriteFile(path, []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := validRestoreOpts(path)
	opts.IsDatabaseEmpty = func(context.Context) (bool, error) { return false, nil }
	if _, err := RestoreLatestFromTelegram(context.Background(), remote, opts); !errors.Is(err, ErrDatabaseNotEmpty) {
		t.Fatalf("got %v, want ErrDatabaseNotEmpty", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do not overwrite" {
		t.Fatalf("database overwritten: %q", got)
	}
}

func TestBoundedWriterStopsBeforeExceedingLimit(t *testing.T) {
	var dst bytes.Buffer
	w := &boundedWriter{dst: &dst, max: 5}
	if n, err := w.Write([]byte("1234")); err != nil || n != 4 {
		t.Fatalf("first write n=%d err=%v", n, err)
	}
	if n, err := w.Write([]byte("678")); err == nil || n != 1 {
		t.Fatalf("oversize write n=%d err=%v", n, err)
	}
	if dst.String() != "12346" {
		t.Fatalf("writer stored %q, want exactly 5 bytes", dst.String())
	}
}

func TestRestoreLatestFromTelegramFailsClosedWhenNoBackupExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movex.db")
	opts := validRestoreOpts(path)
	opts.IsDatabaseEmpty = func(context.Context) (bool, error) { return true, nil }
	_, err := RestoreLatestFromTelegram(context.Background(), fakeTelegramRestore{}, opts)
	if err == nil {
		t.Fatal("expected missing Telegram backup to stop automatic restore")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("database file should not be created after missing backup, stat err=%v", statErr)
	}
}
