// Package backup creates consistent SQLite snapshots and ships them through a
// narrow Telegram MTProto interface. The transport is deliberately injectable
// so backup orchestration can be tested without a live Telegram account.
package backup

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TelegramClient is implemented by the gotd/td transport and by test doubles.
type TelegramClient interface {
	UploadDocument(context.Context, string, string, io.Reader, int64) (int64, error)
	PinMessage(context.Context, int64) error
	UnpinMessage(context.Context, int64) error
	DeleteMessage(context.Context, int64) error
}

type FloodWaitError struct {
	Wait time.Duration
	Err  error
}

func (e *FloodWaitError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("telegram flood wait %s: %v", e.Wait, e.Err)
	}
	return "telegram flood wait " + e.Wait.String()
}
func (e *FloodWaitError) Unwrap() error { return e.Err }

type Options struct {
	DB            *sql.DB
	DBPath        string
	Dir           string
	Keep          int
	SchemaVersion int
	Telegram      TelegramClient
	Now           func() time.Time
	Logger        interface{ Printf(string, ...any) }
	RetryAttempts int
}

type ManifestEntry struct {
	MessageID         int64     `json:"message_id"`
	File              string    `json:"file"`
	CreatedAt         time.Time `json:"created_at"`
	SHA256            string    `json:"sha256"`
	Bytes             int64     `json:"bytes"` // compressed Telegram document size
	UncompressedBytes int64     `json:"uncompressed_bytes,omitempty"`
}

type Result struct {
	File              string           `json:"file"`
	Filename          string           `json:"filename"`
	Size              int64            `json:"size"`
	SHA256            string           `json:"sha256"`
	SchemaVersion     int              `json:"schema_version"`
	Rows              map[string]int64 `json:"rows"`
	TelegramMessageID int64            `json:"telegram_message_id,omitempty"`
	At                time.Time        `json:"at"`
}

type Manager struct {
	opts     Options
	mu       sync.Mutex
	statusMu sync.RWMutex
	last     *Result
	lastErr  string
	running  bool
}

func New(opts Options) (*Manager, error) {
	if opts.DB == nil || strings.TrimSpace(opts.DBPath) == "" || strings.TrimSpace(opts.Dir) == "" {
		return nil, errors.New("backup requires DB, DBPath and Dir")
	}
	if opts.Keep < 1 {
		opts.Keep = 30
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RetryAttempts <= 0 {
		opts.RetryAttempts = 4
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create backup directory: %w", err)
	}
	return &Manager{opts: opts}, nil
}

func (m *Manager) Status() (running bool, last *Result, lastErr string) {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	if m.last != nil {
		cp := *m.last
		last = &cp
	}
	return m.running, last, m.lastErr
}

// Run serializes backups. A concurrent request is reported as skipped, not queued.
func (m *Manager) Run(ctx context.Context) (Result, error) {
	if !m.mu.TryLock() {
		return Result{}, ErrAlreadyRunning
	}
	defer m.mu.Unlock()
	m.statusMu.Lock()
	m.running = true
	m.statusMu.Unlock()
	defer func() { m.statusMu.Lock(); m.running = false; m.statusMu.Unlock() }()

	res, err := m.run(ctx)
	m.statusMu.Lock()
	if err != nil {
		m.lastErr = err.Error()
	} else {
		m.last = &res
		m.lastErr = ""
	}
	m.statusMu.Unlock()
	return res, err
}

var ErrAlreadyRunning = errors.New("backup already running")

func (m *Manager) run(ctx context.Context) (Result, error) {
	at := m.opts.Now().UTC()
	stamp := at.Format("20060102-150405")
	filename := "movex-" + stamp + ".db.gz"
	dbTmp, err := os.CreateTemp(m.opts.Dir, ".movex-snapshot-*.db")
	if err != nil {
		return Result{}, fmt.Errorf("create snapshot temp file: %w", err)
	}
	dbTmpPath := dbTmp.Name()
	_ = dbTmp.Close()
	_ = os.Remove(dbTmpPath)
	defer os.Remove(dbTmpPath)
	if err := vacuumInto(ctx, m.opts.DB, dbTmpPath); err != nil {
		return Result{}, err
	}
	rows, err := rowCounts(ctx, m.opts.DB)
	if err != nil {
		return Result{}, fmt.Errorf("count database rows: %w", err)
	}
	hash, rawSize, err := fileSHA256(dbTmpPath)
	if err != nil {
		return Result{}, err
	}
	outTmp, err := os.CreateTemp(m.opts.Dir, ".movex-gzip-*.tmp")
	if err != nil {
		return Result{}, fmt.Errorf("create gzip temp file: %w", err)
	}
	outTmpPath := outTmp.Name()
	defer os.Remove(outTmpPath)
	gz := gzip.NewWriter(outTmp)
	gz.Name = filename[:len(filename)-3]
	in, err := os.Open(dbTmpPath)
	if err == nil {
		_, err = io.Copy(gz, in)
		_ = in.Close()
	}
	if err == nil {
		err = gz.Close()
	} else {
		_ = gz.Close()
	}
	if err == nil {
		err = outTmp.Sync()
	}
	if closeErr := outTmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Result{}, fmt.Errorf("gzip snapshot: %w", err)
	}
	finalPath := filepath.Join(m.opts.Dir, filename)
	if err := os.Rename(outTmpPath, finalPath); err != nil {
		return Result{}, fmt.Errorf("publish backup file: %w", err)
	}
	st, err := os.Stat(finalPath)
	if err != nil {
		return Result{}, err
	}
	res := Result{File: finalPath, Filename: filename, Size: st.Size(), SHA256: hash, SchemaVersion: m.opts.SchemaVersion, Rows: rows, At: at}
	if m.opts.Telegram != nil {
		caption, _ := json.Marshal(struct {
			At     string           `json:"at"`
			Schema int              `json:"schema_version"`
			Size   int64            `json:"uncompressed_bytes"`
			SHA    string           `json:"sha256"`
			Rows   map[string]int64 `json:"rows"`
		}{at.Format(time.RFC3339), m.opts.SchemaVersion, rawSize, hash, rows})
		f, err := os.Open(finalPath)
		if err != nil {
			return res, err
		}
		var messageID int64
		upErr := m.retry(ctx, func() error {
			if _, e := f.Seek(0, io.SeekStart); e != nil {
				return e
			}
			var e error
			messageID, e = m.opts.Telegram.UploadDocument(ctx, filename, string(caption), f, st.Size())
			return e
		})
		_ = f.Close()
		if upErr != nil {
			return res, fmt.Errorf("telegram upload: %w", upErr)
		}
		res.TelegramMessageID = messageID
		if err := m.rotateTelegram(ctx, ManifestEntry{MessageID: messageID, File: filename, CreatedAt: at, SHA256: hash, Bytes: st.Size(), UncompressedBytes: rawSize}); err != nil {
			return res, fmt.Errorf("telegram retention: %w", err)
		}
	}
	if err := m.rotateLocal(finalPath); err != nil {
		return res, err
	}
	if m.opts.Logger != nil {
		m.opts.Logger.Printf("backup completed file=%s bytes=%d telegram_message_id=%d", filename, st.Size(), res.TelegramMessageID)
	}
	return res, nil
}

func vacuumInto(ctx context.Context, d *sql.DB, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// VACUUM INTO expects a string literal; escape quotes rather than interpolating raw input.
	q := strings.ReplaceAll(abs, "'", "''")
	if _, err := d.ExecContext(ctx, "VACUUM INTO '"+q+"'"); err != nil {
		return fmt.Errorf("SQLite VACUUM INTO: %w", err)
	}
	return nil
}

var backupTables = []string{"movies", "seasons", "episodes", "links", "categories", "movie_categories", "movie_languages", "banners", "social_links", "notifications", "settings", "users", "my_list", "watch_progress", "broken_links", "error_logs", "search_stats", "movie_stats", "pwd_reset_attempts", "audit_log", "workers", "meta", "changes", "admin_sessions"}

func rowCounts(ctx context.Context, d *sql.DB) (map[string]int64, error) {
	out := make(map[string]int64, len(backupTables))
	for _, table := range backupTables {
		var n int64
		if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM \""+table+"\"").Scan(&n); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				continue
			}
			return nil, fmt.Errorf("%s: %w", table, err)
		}
		out[table] = n
	}
	return out, nil
}
func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func (m *Manager) retry(ctx context.Context, fn func() error) error {
	var last error
	for attempt := 0; attempt < m.opts.RetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = fn()
		if last == nil {
			return nil
		}
		wait := time.Duration(1<<attempt) * time.Second
		var flood *FloodWaitError
		if errors.As(last, &flood) && flood.Wait > wait {
			wait = flood.Wait
		}
		if attempt == m.opts.RetryAttempts-1 {
			break
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return last
}

type manifest struct {
	Entries []ManifestEntry `json:"entries"`
}

func (m *Manager) readManifest() (manifest, error) {
	b, err := os.ReadFile(filepath.Join(m.opts.Dir, "telegram-manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return manifest{}, nil
	}
	if err != nil {
		return manifest{}, err
	}
	var v manifest
	if err := json.Unmarshal(b, &v); err != nil {
		return manifest{}, fmt.Errorf("decode Telegram backup manifest: %w", err)
	}
	return v, nil
}
func (m *Manager) writeManifest(v manifest) error {
	p := filepath.Join(m.opts.Dir, "telegram-manifest.json")
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	t, err := os.CreateTemp(m.opts.Dir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	name := t.Name()
	defer os.Remove(name)
	if _, err = t.Write(b); err == nil {
		err = t.Sync()
	}
	if ce := t.Close(); err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, p); err != nil {
		return err
	}
	return syncDir(m.opts.Dir)
}
func (m *Manager) rotateTelegram(ctx context.Context, added ManifestEntry) error {
	if m.opts.Telegram == nil {
		return errors.New("Telegram retention requires a configured client")
	}
	if added.MessageID <= 0 || strings.TrimSpace(added.File) == "" ||
		filepath.Base(added.File) != added.File || strings.ContainsAny(added.File, `/\`) {
		return errors.New("invalid Telegram backup manifest entry")
	}

	v, err := m.readManifest()
	if err != nil {
		return err
	}
	// Reject malformed manifest data before using message IDs for destructive
	// Telegram operations. Keep the validation compatible with older entries
	// that predate the optional size/hash fields.
	seen := make(map[int64]struct{}, len(v.Entries)+1)
	for _, entry := range v.Entries {
		if entry.MessageID <= 0 || strings.TrimSpace(entry.File) == "" ||
			filepath.Base(entry.File) != entry.File || strings.ContainsAny(entry.File, `/\`) {
			return fmt.Errorf("invalid Telegram backup manifest entry for message %d", entry.MessageID)
		}
		if _, ok := seen[entry.MessageID]; ok {
			return fmt.Errorf("duplicate Telegram backup message ID %d in manifest", entry.MessageID)
		}
		seen[entry.MessageID] = struct{}{}
	}
	if _, exists := seen[added.MessageID]; exists {
		return fmt.Errorf("Telegram backup message ID %d is already in the manifest", added.MessageID)
	}

	v.Entries = append(v.Entries, added)
	sort.Slice(v.Entries, func(i, j int) bool { return v.Entries[i].CreatedAt.Before(v.Entries[j].CreatedAt) })
	cut := len(v.Entries) - m.opts.Keep
	retained := v.Entries
	if cut > 0 {
		retained = v.Entries[cut:]
	}

	// Persist the message ID before making Telegram state changes. If pinning
	// fails or the process stops, the uploaded document remains discoverable
	// and can be reconciled on the next backup run instead of becoming orphaned.
	if err := m.writeManifest(manifest{Entries: v.Entries}); err != nil {
		return fmt.Errorf("persist Telegram backup manifest before pinning and retention: %w", err)
	}

	// Reconcile every retained pin, not only the newest one. This repairs a
	// previous transient pin failure on the next successful backup run.
	for _, entry := range retained {
		if err := m.retry(ctx, func() error { return m.opts.Telegram.PinMessage(ctx, entry.MessageID) }); err != nil {
			return fmt.Errorf("pin Telegram backup message %d: %w", entry.MessageID, err)
		}
	}

	// Only delete entries outside the retention window after the new manifest
	// and all retained pins are durable. Failed deletions remain recoverable
	// in the manifest and will be retried on a later run.
	if cut <= 0 {
		return nil
	}
	for _, old := range v.Entries[:cut] {
		_ = m.retry(ctx, func() error { return m.opts.Telegram.UnpinMessage(ctx, old.MessageID) })
		if err := m.retry(ctx, func() error { return m.opts.Telegram.DeleteMessage(ctx, old.MessageID) }); err != nil {
			// Keep the complete manifest, including all not-yet-deleted expired
			// entries. A later run can retry without losing Telegram message IDs.
			return fmt.Errorf("delete expired backup message %d: %w", old.MessageID, err)
		}
	}
	if err := m.writeManifest(manifest{Entries: retained}); err != nil {
		// Telegram deletions may already have succeeded. Keeping stale IDs is
		// safe because DeleteMessage is idempotent for already-absent messages.
		return fmt.Errorf("persist Telegram retention manifest after deletion: %w", err)
	}
	return nil
}
func (m *Manager) rotateLocal(current string) error {
	entries, err := os.ReadDir(m.opts.Dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "movex-") && strings.HasSuffix(n, ".db.gz") {
			files = append(files, filepath.Join(m.opts.Dir, n))
		}
	}
	sort.Strings(files)
	excess := len(files) - m.opts.Keep
	removed := 0
	for _, path := range files {
		if removed >= excess {
			break
		}
		// The snapshot produced by this run must survive even if a clock
		// adjustment makes its filename sort before older snapshots.
		if path == current {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed++
	}
	return nil
}
