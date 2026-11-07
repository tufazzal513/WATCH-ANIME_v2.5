package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeConnector struct{}

func (fakeConnector) Connect(context.Context) (driver.Conn, error) { return fakeConn{}, nil }
func (fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }
func (fakeConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	const prefix = "VACUUM INTO '"
	if !strings.HasPrefix(q, prefix) || !strings.HasSuffix(q, "'") {
		return nil, fmt.Errorf("unexpected exec: %s", q)
	}
	p := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(q, prefix), "'"), "''", "'")
	return driver.RowsAffected(0), os.WriteFile(p, []byte("fake-sqlite-snapshot"), 0600)
}
func (fakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &fakeRows{}, nil
}

type fakeRows struct{ done bool }

func (*fakeRows) Columns() []string { return []string{"count"} }
func (*fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(3)
	return nil
}

type fakeTG struct {
	mu                     sync.Mutex
	next                   int64
	uploads                []string
	pinned, unpin, deleted []int64
	failOnce               bool
	failPinOnce            bool
	failDelete             bool
}

func (f *fakeTG) UploadDocument(_ context.Context, name, _ string, r io.Reader, _ int64) (int64, error) {
	b, _ := io.ReadAll(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOnce {
		f.failOnce = false
		return 0, &FloodWaitError{Wait: time.Millisecond, Err: errors.New("retry")}
	}
	if len(b) == 0 {
		return 0, errors.New("empty upload")
	}
	f.next++
	f.uploads = append(f.uploads, name)
	return f.next, nil
}
func (f *fakeTG) PinMessage(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinned = append(f.pinned, id)
	if f.failPinOnce {
		f.failPinOnce = false
		return errors.New("temporary pin failure")
	}
	return nil
}
func (f *fakeTG) UnpinMessage(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unpin = append(f.unpin, id)
	return nil
}
func (f *fakeTG) DeleteMessage(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	if f.failDelete {
		return errors.New("temporary delete failure")
	}
	return nil
}

func TestRunSnapshotGzipTelegramAndRetention(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	dir := t.TempDir()
	tg := &fakeTG{failOnce: true}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	m, err := New(Options{DB: db, DBPath: filepath.Join(dir, "live.db"), Dir: filepath.Join(dir, "backups"), Keep: 2, SchemaVersion: 4, Telegram: tg, Now: func() time.Time { v := now; now = now.Add(time.Minute); return v }, RetryAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := m.Run(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if len(tg.uploads) != 3 || len(tg.pinned) != 5 {
		t.Fatalf("uploads=%v pinned=%v", tg.uploads, tg.pinned)
	}
	wantPins := []int64{1, 1, 2, 2, 3}
	for i, want := range wantPins {
		if tg.pinned[i] != want {
			t.Fatalf("pin sequence=%v, want %v", tg.pinned, wantPins)
		}
	}
	if len(tg.deleted) != 1 || tg.deleted[0] != 1 {
		t.Fatalf("deleted=%v", tg.deleted)
	}
	if len(tg.unpin) != 1 || tg.unpin[0] != 1 {
		t.Fatalf("unpin=%v", tg.unpin)
	}
	files, err := filepath.Glob(filepath.Join(dir, "backups", "movex-*.db.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("local retained %d files, want 2", len(files))
	}
	f, err := os.Open(files[len(files)-1])
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	_ = gz.Close()
	_ = f.Close()
	if !bytes.Equal(raw, []byte("fake-sqlite-snapshot")) {
		t.Fatalf("bad snapshot payload %q", raw)
	}
	v, err := m.readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 2 {
		t.Fatalf("manifest retained %d entries", len(v.Entries))
	}
	_, last, lastErr := m.Status()
	if last == nil || lastErr != "" || last.TelegramMessageID != 3 {
		t.Fatalf("status last=%+v err=%q", last, lastErr)
	}
}

func TestRetryHonorsFloodWaitAndStopsAtAttemptLimit(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	m, err := New(Options{DB: db, DBPath: "x", Dir: t.TempDir(), RetryAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = m.retry(context.Background(), func() error { calls++; return errors.New("no") })
	if err == nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestConcurrentRunIsRejected(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	m, err := New(Options{DB: db, DBPath: "x", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !m.mu.TryLock() {
		t.Fatal("could not lock test manager")
	}
	_, err = m.Run(context.Background())
	m.mu.Unlock()
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("got %v", err)
	}
}

func TestTelegramRetentionKeepsManifestWhenDeleteFails(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	tg := &fakeTG{failDelete: true}
	m, err := New(Options{DB: db, DBPath: "unused", Dir: t.TempDir(), Keep: 1, Telegram: tg, RetryAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	old := ManifestEntry{MessageID: 10, File: "old.db.gz", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := m.writeManifest(manifest{Entries: []ManifestEntry{old}}); err != nil {
		t.Fatal(err)
	}
	added := ManifestEntry{MessageID: 11, File: "new.db.gz", CreatedAt: old.CreatedAt.Add(time.Hour)}
	if err := m.rotateTelegram(context.Background(), added); err == nil {
		t.Fatal("expected delete failure")
	}
	got, err := m.readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 || got.Entries[0].MessageID != 10 || got.Entries[1].MessageID != 11 {
		t.Fatalf("manifest lost retry state: %+v", got.Entries)
	}
}

func TestLocalRetentionAlwaysKeepsCurrentAndExactLimit(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	dir := t.TempDir()
	m, err := New(Options{DB: db, DBPath: "unused", Dir: dir, Keep: 2})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{
		"movex-20260101-000000.db.gz",
		"movex-20260102-000000.db.gz",
		"movex-20260103-000000.db.gz",
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(dir, names[0]) // Simulate a wall-clock rollback.
	if err := m.rotateLocal(current); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "movex-*.db.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("retained %d files, want exactly 2: %v", len(files), files)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current backup was removed: %v", err)
	}
}

func TestTelegramManifestPersistsUploadedMessageBeforePinAndRetriesPin(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	dir := t.TempDir()
	tg := &fakeTG{failPinOnce: true}
	m, err := New(Options{DB: db, DBPath: "unused", Dir: dir, Keep: 2, Telegram: tg, RetryAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	first := ManifestEntry{MessageID: 41, File: "movex-first.db.gz", CreatedAt: time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC)}
	if err := m.rotateTelegram(context.Background(), first); err == nil {
		t.Fatal("expected first pin attempt to fail")
	}
	got, err := m.readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].MessageID != first.MessageID {
		t.Fatalf("uploaded message was not persisted before pin: %+v", got.Entries)
	}

	second := ManifestEntry{MessageID: 42, File: "movex-second.db.gz", CreatedAt: first.CreatedAt.Add(time.Minute)}
	if err := m.rotateTelegram(context.Background(), second); err != nil {
		t.Fatalf("second rotation should reconcile old pin and pin new message: %v", err)
	}
	got, err = m.readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 || got.Entries[0].MessageID != 41 || got.Entries[1].MessageID != 42 {
		t.Fatalf("unexpected recovered manifest: %+v", got.Entries)
	}
	if len(tg.pinned) != 3 || tg.pinned[1] != 41 || tg.pinned[2] != 42 {
		t.Fatalf("expected old and new messages to be pinned on retry, got %v", tg.pinned)
	}
}

func TestTelegramRetentionRejectsUnsafeOrDuplicateManifestEntries(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	dir := t.TempDir()
	tg := &fakeTG{}
	m, err := New(Options{DB: db, DBPath: "unused", Dir: dir, Keep: 2, Telegram: tg, RetryAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	unsafe := ManifestEntry{MessageID: 7, File: "../outside.db.gz", CreatedAt: time.Now()}
	if err := m.rotateTelegram(context.Background(), unsafe); err == nil {
		t.Fatal("unsafe filename accepted")
	}
	duplicate := ManifestEntry{MessageID: 8, File: "backup.db.gz", CreatedAt: time.Now()}
	if err := m.writeManifest(manifest{Entries: []ManifestEntry{duplicate, duplicate}}); err != nil {
		t.Fatal(err)
	}
	added := ManifestEntry{MessageID: 9, File: "new.db.gz", CreatedAt: time.Now().Add(time.Minute)}
	if err := m.rotateTelegram(context.Background(), added); err == nil {
		t.Fatal("duplicate message IDs in manifest accepted")
	}
	if len(tg.pinned) != 0 || len(tg.deleted) != 0 {
		t.Fatalf("Telegram operations ran on invalid manifest: pinned=%v deleted=%v", tg.pinned, tg.deleted)
	}
}

func TestTelegramRetentionRetriesExpiredMessagesFromManifest(t *testing.T) {
	db := sql.OpenDB(fakeConnector{})
	defer db.Close()
	tg := &fakeTG{failDelete: true}
	m, err := New(Options{DB: db, DBPath: "unused", Dir: t.TempDir(), Keep: 1, Telegram: tg, RetryAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	old := ManifestEntry{MessageID: 51, File: "old.db.gz", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	first := ManifestEntry{MessageID: 52, File: "first.db.gz", CreatedAt: old.CreatedAt.Add(time.Hour)}
	if err := m.writeManifest(manifest{Entries: []ManifestEntry{old}}); err != nil {
		t.Fatal(err)
	}
	if err := m.rotateTelegram(context.Background(), first); err == nil {
		t.Fatal("expected first delete failure")
	}
	pending, err := m.readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Entries) != 2 || pending.Entries[0].MessageID != 51 {
		t.Fatalf("failed deletion was not retained for retry: %+v", pending.Entries)
	}

	tg.failDelete = false
	second := ManifestEntry{MessageID: 53, File: "second.db.gz", CreatedAt: first.CreatedAt.Add(time.Hour)}
	if err := m.rotateTelegram(context.Background(), second); err != nil {
		t.Fatalf("retrying expired message failed: %v", err)
	}
	final, err := m.readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Entries) != 1 || final.Entries[0].MessageID != 53 {
		t.Fatalf("expired messages remain after successful retry: %+v", final.Entries)
	}
}
