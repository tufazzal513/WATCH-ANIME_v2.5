package replica

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"movex/internal/repo"
)

type memoryStore struct {
	epoch   string
	seq     int64
	applied []repo.ReplicationBatch
	err     error
}

func (m *memoryStore) ReplicaCursor(context.Context) (string, int64, error) {
	return m.epoch, m.seq, nil
}
func (m *memoryStore) ApplyReplicationBatch(_ context.Context, b repo.ReplicationBatch) error {
	if m.err != nil {
		return m.err
	}
	m.applied = append(m.applied, b)
	m.epoch = b.Epoch
	if b.Full {
		m.seq = b.MaxSeq
	} else if len(b.Changes) > 0 {
		m.seq = b.Changes[len(b.Changes)-1].Seq
	}
	return nil
}

func TestSyncOnceAuthenticatesAndAppliesBatch(t *testing.T) {
	store := &memoryStore{epoch: "old-epoch", seq: 12}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Worker-Key") != "worker-secret" {
			t.Errorf("worker key was not sent")
		}
		if r.URL.Path == "/prefix/internal/ack" {
			var ack struct {
				Epoch string `json:"epoch"`
				Seq   int64  `json:"seq"`
			}
			if err := json.NewDecoder(r.Body).Decode(&ack); err != nil || ack.Epoch != "new-epoch" || ack.Seq != 15 {
				t.Errorf("bad ack: %+v %v", ack, err)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/prefix/internal/sync" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("epoch") != "old-epoch" || q.Get("since") != "12" || q.Get("limit") != "200" {
			t.Errorf("query = %v", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(repo.ReplicationBatch{Epoch: "new-epoch", MaxSeq: 15, Full: true, Snapshot: &repo.ReplicationSnapshot{Settings: map[string]string{}}})
	}))
	defer server.Close()
	s, err := NewSyncer(server.URL+"/prefix", "worker-secret", time.Minute, store, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.applied) != 1 || !store.applied[0].Full || store.epoch != "new-epoch" || store.seq != 15 {
		t.Fatalf("batch was not applied: %#v", store)
	}
}

func TestSyncOnceRejectsUnauthorizedMain(t *testing.T) {
	store := &memoryStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unauthorized", http.StatusUnauthorized) }))
	defer server.Close()
	s, err := NewSyncer(server.URL, "bad-key", time.Minute, store, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected unauthorized response")
	}
	if len(store.applied) != 0 {
		t.Fatal("unauthorized batch must not be applied")
	}
}

func TestSyncOncePropagatesAtomicApplyFailure(t *testing.T) {
	store := &memoryStore{err: errors.New("disk full")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(repo.ReplicationBatch{Epoch: "epoch", MaxSeq: 1, Full: true, Snapshot: &repo.ReplicationSnapshot{Settings: map[string]string{}}})
	}))
	defer server.Close()
	s, err := NewSyncer(server.URL, "key", time.Minute, store, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected store failure")
	}
}

func TestNewSyncerRequiresHTTPURLAndKey(t *testing.T) {
	store := &memoryStore{}
	if _, err := NewSyncer("file:///tmp/main", "key", time.Minute, store, nil, nil); err == nil {
		t.Fatal("expected invalid URL")
	}
	if _, err := NewSyncer("https://main.example", "", time.Minute, store, nil, nil); err == nil {
		t.Fatal("expected missing key")
	}
	if _, err := url.Parse("https://main.example"); err != nil {
		t.Fatal(err)
	}
}

func TestSyncOnceRejectsMalformedIncrementalBatchBeforeApply(t *testing.T) {
	cases := []struct {
		name  string
		batch repo.ReplicationBatch
	}{
		{
			name:  "change beyond maximum sequence",
			batch: repo.ReplicationBatch{Epoch: "epoch", MaxSeq: 4, Changes: []repo.ReplicationChange{{Seq: 5, Table: "movies", ID: "m1", Op: "delete"}}},
		},
		{
			name:  "unsupported table",
			batch: repo.ReplicationBatch{Epoch: "epoch", MaxSeq: 1, Changes: []repo.ReplicationChange{{Seq: 1, Table: "users", ID: "u1", Op: "delete"}}},
		},
		{
			name:  "missing upsert row",
			batch: repo.ReplicationBatch{Epoch: "epoch", MaxSeq: 1, Changes: []repo.ReplicationChange{{Seq: 1, Table: "movies", ID: "m1", Op: "upsert"}}},
		},
		{
			name:  "empty batch with advanced max sequence",
			batch: repo.ReplicationBatch{Epoch: "epoch", MaxSeq: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &memoryStore{epoch: "epoch", seq: 0}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.batch)
			}))
			defer server.Close()
			s, err := NewSyncer(server.URL, "key", time.Minute, store, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SyncOnce(context.Background()); err == nil {
				t.Fatal("expected malformed batch to be rejected")
			}
			if len(store.applied) != 0 {
				t.Fatal("malformed batch must not reach the store")
			}
		})
	}
}

func TestNewSyncerRejectsURLsWithCredentialsOrQuery(t *testing.T) {
	for _, raw := range []string{"https://user:pass@main.example", "https://main.example?token=secret", "https://main.example/#fragment"} {
		if _, err := NewSyncer(raw, "key", time.Minute, &memoryStore{}, nil, nil); err == nil {
			t.Errorf("expected unsafe base URL %q to be rejected", raw)
		}
	}
}

func TestSyncOnceReportsAckFailureAfterLocalCommit(t *testing.T) {
	store := &memoryStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/ack" {
			http.Error(w, "ack unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(repo.ReplicationBatch{Epoch: "epoch", MaxSeq: 3, Full: true, Snapshot: &repo.ReplicationSnapshot{Settings: map[string]string{}}})
	}))
	defer server.Close()
	s, err := NewSyncer(server.URL, "key", time.Minute, store, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected acknowledgement failure")
	}
	if store.epoch != "epoch" || store.seq != 3 || len(store.applied) != 1 {
		t.Fatalf("local transaction should remain committed for retry: %+v", store)
	}
}
