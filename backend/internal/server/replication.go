package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"movex/internal/httpx"
	"movex/internal/repo"
)

func (s *Server) replicationRoutes() {
	if s.deps.Cfg.IsMain() {
		s.mux.Handle("GET /internal/sync", httpx.Chain(http.HandlerFunc(s.internalSync), httpx.RateLimitIP(s.ipLimit, s.deps.Cfg.TrustProxy)))
		s.mux.Handle("POST /internal/ack", httpx.Chain(http.HandlerFunc(s.internalAck), httpx.RateLimitIP(s.ipLimit, s.deps.Cfg.TrustProxy)))
	} else {
		s.mux.Handle("POST /internal/notify", httpx.Chain(http.HandlerFunc(s.internalNotify), httpx.RateLimitIP(s.ipLimit, s.deps.Cfg.TrustProxy)))
	}
}

func (s *Server) internalSync(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("X-Worker-Key"))
	worker, err := s.deps.Repo.WorkerByKey(r.Context(), key)
	if err != nil || worker == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid worker key")
		return
	}
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil || since < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_since", "since must be a non-negative integer")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	batch, err := s.deps.Repo.ReplicationBatch(r.Context(), r.URL.Query().Get("epoch"), since, limit)
	if err != nil {
		s.deps.Log.Error("replication batch failed", "worker", worker.ID, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "sync_failed", "could not prepare replication batch")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, batch)
}

// internalNotify accepts only the worker's configured shared key and schedules an immediate pull.
func (s *Server) internalNotify(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(r.Body, 1025))
	if err != nil || n > 1024 {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "too_large", "notification body is too large")
		return
	}
	provided := r.Header.Get("X-Notify")
	want := s.deps.Cfg.SyncNotifyKey
	if want == "" || provided == "" || len(provided) != len(want) || subtle.ConstantTimeCompare([]byte(provided), []byte(want)) != 1 {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid notify key")
		return
	}
	if s.workerSyncer == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "sync_unavailable", "worker synchronization is not initialized")
		return
	}
	s.workerSyncer.Notify()
	w.WriteHeader(http.StatusAccepted)
}

type workerAck struct {
	Epoch string `json:"epoch"`
	Seq   *int64 `json:"seq"`
}

// internalAck records only progress that the worker reports after its local transaction commits.
func (s *Server) internalAck(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("X-Worker-Key"))
	worker, err := s.deps.Repo.WorkerByKey(r.Context(), key)
	if err != nil || worker == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid worker key")
		return
	}
	defer r.Body.Close()
	var ack workerAck
	if !httpx.DecodeJSON(w, r, &ack, 4096) {
		return
	}
	if ack.Seq == nil || strings.TrimSpace(ack.Epoch) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_ack", "epoch and seq are required")
		return
	}
	if err := s.deps.Repo.AcknowledgeWorker(r.Context(), worker.ID, ack.Epoch, *ack.Seq); err != nil {
		if repo.IsValidation(err) {
			httpx.WriteError(w, http.StatusConflict, "stale_ack", "acknowledgement does not match the current replication state")
			return
		}
		if errors.Is(err, repo.ErrNotFound) {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "worker no longer exists")
			return
		}
		s.deps.Log.Error("worker acknowledgement failed", "worker", worker.ID, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "ack_failed", "could not record worker acknowledgement")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notificationLoop sends debounced wake-ups when the main catalog sequence changes.
// Failed deliveries remain pending and are retried with bounded exponential backoff;
// periodic worker polling remains the final fallback.
type pendingWorkerNotification struct {
	worker  repo.Worker
	nextTry time.Time
	backoff time.Duration
}

func notificationRetryDelay(previous time.Duration) time.Duration {
	if previous <= 0 {
		return time.Second
	}
	if previous >= 30*time.Second {
		return time.Minute
	}
	next := previous * 2
	if next > time.Minute {
		return time.Minute
	}
	return next
}

func (s *Server) notificationLoop(ctx context.Context) {
	if s.deps.Cfg.SyncNotifyKey == "" || s.deps.Repo == nil {
		s.deps.Log.Info("worker push notifications disabled; workers will use periodic polling")
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastEpoch string
	var lastSeq int64 = -1
	pending := make(map[string]pendingWorkerNotification)
	client := s.deps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			epoch, err := s.deps.Repo.EnsureEpoch(ctx)
			if err != nil {
				s.deps.Log.Warn("read replication epoch for notifications failed", "err", err)
				continue
			}
			seq, err := s.deps.Repo.MaxSeq(ctx)
			if err != nil {
				s.deps.Log.Warn("read replication sequence for notifications failed", "err", err)
				continue
			}
			if epoch != lastEpoch || seq != lastSeq {
				workers, listErr := s.deps.Repo.ListWorkers(ctx)
				if listErr != nil {
					s.deps.Log.Warn("list workers for notifications failed", "err", listErr)
					continue
				}
				current := make(map[string]bool, len(workers))
				for _, worker := range workers {
					current[worker.ID] = true
					if _, exists := pending[worker.ID]; !exists {
						pending[worker.ID] = pendingWorkerNotification{worker: worker, nextTry: now}
					} else {
						entry := pending[worker.ID]
						entry.worker = worker
						pending[worker.ID] = entry
					}
				}
				for id := range pending {
					if !current[id] {
						delete(pending, id)
					}
				}
				lastEpoch, lastSeq = epoch, seq
			}
			for id, entry := range pending {
				if now.Before(entry.nextTry) {
					continue
				}
				if err := s.sendWorkerNotification(ctx, client, entry.worker.URL); err != nil {
					delay := notificationRetryDelay(entry.backoff)
					entry.backoff = delay
					entry.nextTry = now.Add(delay)
					pending[id] = entry
					s.deps.Log.Warn("worker notification failed; delivery will be retried and periodic polling remains available", "worker", id, "retry_in", delay, "err", err)
					continue
				}
				delete(pending, id)
			}
		}
	}
}

func (s *Server) sendWorkerNotification(ctx context.Context, client *http.Client, base string) error {
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/internal/notify"
	u.RawQuery, u.Fragment = "", ""
	body := bytes.NewReader([]byte(`{}`))
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Notify", s.deps.Cfg.SyncNotifyKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("worker returned HTTP %d", resp.StatusCode)
	}
	return nil
}
