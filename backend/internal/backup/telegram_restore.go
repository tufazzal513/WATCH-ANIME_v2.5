package backup

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TelegramBackupMetadata is embedded in each uploaded document caption. The
// SHA-256 and byte count describe the uncompressed SQLite database, not gzip.
type TelegramBackupMetadata struct {
	At                time.Time        `json:"at"`
	SchemaVersion     int              `json:"schema_version"`
	UncompressedBytes int64            `json:"uncompressed_bytes"`
	SHA256            string           `json:"sha256"`
	Rows              map[string]int64 `json:"rows"`
}

// ParseTelegramBackupMetadata rejects incomplete or unsafe restore metadata.
func ParseTelegramBackupMetadata(caption string) (TelegramBackupMetadata, error) {
	var metadata TelegramBackupMetadata
	if err := json.Unmarshal([]byte(caption), &metadata); err != nil {
		return metadata, fmt.Errorf("decode Telegram backup caption: %w", err)
	}
	hash, err := hex.DecodeString(metadata.SHA256)
	if err != nil || len(hash) != 32 {
		return metadata, errors.New("Telegram backup caption has an invalid SHA-256")
	}
	if metadata.At.IsZero() || metadata.SchemaVersion < 1 || metadata.UncompressedBytes <= 0 {
		return metadata, errors.New("Telegram backup caption is missing its timestamp, schema version, or byte count")
	}
	return metadata, nil
}

// RemoteBackup is a validated Telegram document candidate.
type RemoteBackup struct {
	MessageID int64
	Filename  string
	Metadata  TelegramBackupMetadata
}

// TelegramRestoreClient is the read-only Telegram surface required for restore.
type TelegramRestoreClient interface {
	ListRestoreDocuments(context.Context) ([]RemoteBackup, error)
	DownloadRestoreDocument(context.Context, int64, io.Writer) error
}

// RestoreLatestFromTelegram downloads the newest metadata-valid backup to a
// private temporary file, then verifies its raw SHA-256, size, gzip stream and
// SQLite integrity before an automatic restore. Automatic mode refuses every
// existing non-zero database file.
func RestoreLatestFromTelegram(ctx context.Context, client TelegramRestoreClient, opts RestoreOptions) (RemoteBackup, error) {
	if client == nil {
		return RemoteBackup{}, errors.New("Telegram restore client is not configured")
	}
	if strings.TrimSpace(opts.DBPath) == "" || opts.OpenSnapshot == nil {
		return RemoteBackup{}, errors.New("Telegram restore requires DBPath and OpenSnapshot")
	}
	if opts.IsDatabaseEmpty == nil {
		return RemoteBackup{}, errors.New("Telegram automatic restore requires an empty-database check")
	}
	empty, err := opts.IsDatabaseEmpty(ctx)
	if err != nil {
		return RemoteBackup{}, fmt.Errorf("check database before Telegram restore: %w", err)
	}
	if !empty {
		return RemoteBackup{}, ErrDatabaseNotEmpty
	}
	candidates, err := client.ListRestoreDocuments(ctx)
	if err != nil {
		return RemoteBackup{}, fmt.Errorf("discover Telegram backups: %w", err)
	}
	if len(candidates) == 0 {
		return RemoteBackup{}, errors.New("no valid Telegram database backup was found")
	}
	seenIDs := make(map[int64]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.MessageID <= 0 || candidate.Filename == "" || filepath.Base(candidate.Filename) != candidate.Filename || strings.ContainsAny(candidate.Filename, `/\\`) || !strings.HasPrefix(strings.ToLower(candidate.Filename), "movex-") || !strings.HasSuffix(strings.ToLower(candidate.Filename), ".db.gz") {
			return RemoteBackup{}, fmt.Errorf("Telegram returned an unsafe backup document at message %d", candidate.MessageID)
		}
		if _, duplicate := seenIDs[candidate.MessageID]; duplicate {
			return RemoteBackup{}, fmt.Errorf("duplicate Telegram backup message ID %d", candidate.MessageID)
		}
		seenIDs[candidate.MessageID] = struct{}{}
		hash, hashErr := hex.DecodeString(candidate.Metadata.SHA256)
		if hashErr != nil || len(hash) != 32 {
			return RemoteBackup{}, fmt.Errorf("Telegram backup message %d has an invalid SHA-256", candidate.MessageID)
		}
		if candidate.Metadata.At.IsZero() || candidate.Metadata.SchemaVersion < 1 || candidate.Metadata.UncompressedBytes <= 0 {
			return RemoteBackup{}, fmt.Errorf("Telegram backup message %d has incomplete metadata", candidate.MessageID)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Metadata.At.Equal(candidates[j].Metadata.At) {
			return candidates[i].MessageID > candidates[j].MessageID
		}
		return candidates[i].Metadata.At.After(candidates[j].Metadata.At)
	})
	latest := candidates[0]
	tmpDir := opts.TempDir
	if tmpDir == "" {
		tmpDir = filepath.Dir(opts.DBPath)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return RemoteBackup{}, fmt.Errorf("create Telegram restore temp directory: %w", err)
	}
	archive, err := os.CreateTemp(tmpDir, ".movex-telegram-restore-*.db.gz")
	if err != nil {
		return RemoteBackup{}, err
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	if err := archive.Chmod(0o600); err != nil {
		_ = archive.Close()
		return RemoteBackup{}, err
	}
	if err := client.DownloadRestoreDocument(ctx, latest.MessageID, &boundedWriter{dst: archive, max: maxCompressedRestoreBytes}); err != nil {
		_ = archive.Close()
		return RemoteBackup{}, fmt.Errorf("download Telegram backup message %d: %w", latest.MessageID, err)
	}
	archiveInfo, statErr := archive.Stat()
	if statErr != nil {
		_ = archive.Close()
		return RemoteBackup{}, statErr
	}
	if archiveInfo.Size() <= 0 || archiveInfo.Size() > maxCompressedRestoreBytes {
		_ = archive.Close()
		return RemoteBackup{}, fmt.Errorf("invalid downloaded Telegram archive size: %d bytes", archiveInfo.Size())
	}
	if err := archive.Sync(); err != nil {
		_ = archive.Close()
		return RemoteBackup{}, err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		_ = archive.Close()
		return RemoteBackup{}, err
	}
	opts.ExpectedSHA256 = latest.Metadata.SHA256
	opts.ExpectedSchemaVersion = latest.Metadata.SchemaVersion
	opts.ExpectedBytes = latest.Metadata.UncompressedBytes
	if err := RestoreGzip(ctx, archive, opts, true, ""); err != nil {
		_ = archive.Close()
		return RemoteBackup{}, fmt.Errorf("restore Telegram backup message %d: %w", latest.MessageID, err)
	}
	if err := archive.Close(); err != nil {
		return RemoteBackup{}, fmt.Errorf("close downloaded Telegram backup: %w", err)
	}
	return latest, nil
}

// boundedWriter prevents a remote Telegram document from filling the DATA_DIR
// before its final size can be checked. It deliberately fails the write that
// would cross the configured maximum.
type boundedWriter struct {
	dst     io.Writer
	max     int64
	written int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if w.dst == nil || w.max <= 0 {
		return 0, errors.New("invalid bounded restore writer")
	}
	remaining := w.max - w.written
	if remaining <= 0 {
		return 0, errors.New("Telegram backup exceeds compressed restore size limit")
	}
	if int64(len(p)) > remaining {
		n, err := w.dst.Write(p[:remaining])
		w.written += int64(n)
		if err != nil {
			return n, err
		}
		return n, errors.New("Telegram backup exceeds compressed restore size limit")
	}
	n, err := w.dst.Write(p)
	w.written += int64(n)
	return n, err
}
