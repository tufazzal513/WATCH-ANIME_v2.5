// Package replica implements the worker-side pull protocol for the public catalog.
package replica

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"movex/internal/repo"
)

const (
	maxSyncResponseBytes = 64 << 20
	syncBatchLimit       = 200
)

type Store interface {
	ReplicaCursor(context.Context) (string, int64, error)
	ApplyReplicationBatch(context.Context, repo.ReplicationBatch) error
}

type Syncer struct {
	mainURL  string
	key      string
	interval time.Duration
	store    Store
	client   *http.Client
	log      *slog.Logger
	mu       sync.Mutex
	notify   chan struct{}
}

func NewSyncer(mainURL, key string, interval time.Duration, store Store, client *http.Client, logger *slog.Logger) (*Syncer, error) {
	mainURL = strings.TrimRight(strings.TrimSpace(mainURL), "/")
	u, err := url.Parse(mainURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("replication requires a valid main server URL")
	}
	key = strings.TrimSpace(key)
	if key == "" || store == nil {
		return nil, errors.New("replication requires worker key and catalog store")
	}
	if interval < 5*time.Second {
		interval = 60 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: 35 * time.Second}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{mainURL: mainURL, key: key, interval: interval, store: store, client: client, log: logger, notify: make(chan struct{}, 1)}, nil
}

// SyncOnce pulls one bounded batch and atomically applies it to the worker database.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	epoch, since, err := s.store.ReplicaCursor(ctx)
	if err != nil {
		return fmt.Errorf("read local replication cursor: %w", err)
	}
	u, err := url.Parse(s.mainURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/internal/sync"
	q := u.Query()
	q.Set("epoch", epoch)
	q.Set("since", strconv.FormatInt(since, 10))
	q.Set("limit", strconv.Itoa(syncBatchLimit))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create replication request: %w", err)
	}
	req.Header.Set("X-Worker-Key", s.key)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("request main replication batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("main replication endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	limited := io.LimitReader(resp.Body, maxSyncResponseBytes+1)
	dec := json.NewDecoder(limited)
	var batch repo.ReplicationBatch
	if err := dec.Decode(&batch); err != nil {
		return fmt.Errorf("decode replication batch: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("replication response contains trailing or oversized data")
	}
	if err := validateBatch(batch, since); err != nil {
		return fmt.Errorf("invalid replication batch: %w", err)
	}
	if err := s.store.ApplyReplicationBatch(ctx, batch); err != nil {
		return fmt.Errorf("apply replication batch: %w", err)
	}
	appliedEpoch, appliedSeq, err := s.store.ReplicaCursor(ctx)
	if err != nil {
		return fmt.Errorf("read committed replication cursor: %w", err)
	}
	if err := s.acknowledge(ctx, appliedEpoch, appliedSeq); err != nil {
		return fmt.Errorf("acknowledge committed replication batch: %w", err)
	}
	s.log.Info("worker catalog synchronized", "full", batch.Full, "changes", len(batch.Changes), "max_seq", appliedSeq)
	return nil
}

func (s *Syncer) acknowledge(ctx context.Context, epoch string, seq int64) error {
	payload, err := json.Marshal(struct {
		Epoch string `json:"epoch"`
		Seq   int64  `json:"seq"`
	}{Epoch: epoch, Seq: seq})
	if err != nil {
		return err
	}
	u, err := url.Parse(s.mainURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/internal/ack"
	u.RawQuery = ""
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("X-Worker-Key", s.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("main acknowledgement endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Notify requests an immediate synchronization without starting a second concurrent pull.
func (s *Syncer) Notify() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Run polls at the configured interval and also reacts to authenticated notify requests.
// Errors are logged and retried on the next interval; the worker keeps its last committed catalog.
func (s *Syncer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.notify:
		}
		callCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := s.SyncOnce(callCtx)
		cancel()
		if err != nil {
			s.log.Error("worker catalog sync failed", "err", err)
		}
	}
}

func validateBatch(batch repo.ReplicationBatch, since int64) error {
	if strings.TrimSpace(batch.Epoch) == "" {
		return errors.New("empty replication epoch")
	}
	if batch.MaxSeq < 0 {
		return errors.New("negative maximum sequence")
	}
	if batch.Full {
		if batch.Snapshot == nil {
			return errors.New("full snapshot is missing")
		}
		if len(batch.Changes) != 0 {
			return errors.New("full snapshot unexpectedly contains incremental changes")
		}
		return nil
	}
	if batch.Snapshot != nil {
		return errors.New("incremental batch unexpectedly contains a full snapshot")
	}
	if batch.MaxSeq < since {
		return errors.New("maximum sequence is older than the worker cursor")
	}
	if len(batch.Changes) > syncBatchLimit {
		return errors.New("incremental batch exceeds the requested change limit")
	}
	allowedTables := map[string]bool{"movies": true, "categories": true, "banners": true, "social_links": true, "notifications": true, "settings": true}
	last := since
	for i, change := range batch.Changes {
		if change.Seq <= last || change.Seq > batch.MaxSeq {
			return fmt.Errorf("change %d has an out-of-range or unordered sequence", i)
		}
		if !allowedTables[change.Table] || strings.TrimSpace(change.ID) == "" {
			return fmt.Errorf("change %d has an unsupported table or empty row ID", i)
		}
		switch change.Op {
		case "upsert":
			if change.Data == nil {
				return fmt.Errorf("upsert change %d has no row data", i)
			}
		case "delete":
			if change.Data != nil {
				return fmt.Errorf("delete change %d unexpectedly contains row data", i)
			}
		default:
			return fmt.Errorf("change %d has an unsupported operation", i)
		}
		last = change.Seq
	}
	if len(batch.Changes) == 0 && batch.MaxSeq != since {
		return errors.New("empty incremental batch does not match the worker cursor; full snapshot required")
	}
	if len(batch.Changes) > 0 && len(batch.Changes) < syncBatchLimit && last != batch.MaxSeq {
		return errors.New("incremental batch ends before the advertised maximum sequence")
	}
	return nil
}
