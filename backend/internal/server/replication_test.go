//go:build !nodriver

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"movex/internal/repo"
)

func TestInternalSyncRequiresWorkerKeyAndReturnsCurrentAggregate(t *testing.T) {
	f := newFx(t)
	if rr := f.get("/internal/sync?since=0", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing key: %d %s", rr.Code, rr.Body.String())
	}
	w, key, err := f.r.CreateWorker(context.Background(), "edge", "https://edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/internal/sync?since=0&limit=50", nil)
	req.Header.Set("X-Worker-Key", key)
	rr := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("sync: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Epoch   string `json:"epoch"`
		MaxSeq  int64  `json:"max_seq"`
		Full    bool   `json:"full"`
		Changes []struct {
			Seq   int64          `json:"seq"`
			Table string         `json:"table"`
			ID    string         `json:"id"`
			Op    string         `json:"op"`
			Data  map[string]any `json:"data"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Epoch == "" || got.MaxSeq == 0 || got.Full {
		t.Fatalf("unexpected batch: %+v", got)
	}
	found := false
	for _, c := range got.Changes {
		if c.Table == "movies" && c.ID == f.film.ID && c.Op == "upsert" {
			found = true
			if c.Data["Title"] != "Great Film" {
				t.Fatalf("movie aggregate missing: %#v", c.Data)
			}
		}
	}
	if !found {
		t.Fatalf("movie change missing: %+v", got.Changes)
	}
	workers, _ := f.r.ListWorkers(context.Background())
	if len(workers) != 1 || workers[0].ID != w.ID || workers[0].LastSeenAt != 0 {
		t.Fatalf("serving a batch must not count as applied: %+v", workers)
	}
	epoch, _ := f.r.EnsureEpoch(context.Background())
	ackReq := httptest.NewRequest("POST", "/internal/ack", strings.NewReader(`{"epoch":"`+epoch+`","seq":`+strconv.FormatInt(got.MaxSeq, 10)+`}`))
	ackReq.Header.Set("X-Worker-Key", key)
	ackReq.Header.Set("Content-Type", "application/json")
	ackRR := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(ackRR, ackReq)
	if ackRR.Code != http.StatusNoContent {
		t.Fatalf("ack: %d %s", ackRR.Code, ackRR.Body.String())
	}
	workers, _ = f.r.ListWorkers(context.Background())
	if workers[0].LastSeq != got.MaxSeq || workers[0].LastEpoch != epoch || workers[0].LastSeenAt == 0 {
		t.Fatalf("worker acknowledgement not recorded: %+v", workers[0])
	}
}

func TestInternalSyncEpochMismatchRequestsFullSnapshot(t *testing.T) {
	f := newFx(t)
	_, key, err := f.r.CreateWorker(context.Background(), "edge", "https://edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/internal/sync?epoch=old-epoch&since=999999", nil)
	req.Header.Set("X-Worker-Key", key)
	rr := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Full     bool `json:"full"`
		Snapshot struct {
			Movies []repo.Movie `json:"movies"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Full || len(got.Snapshot.Movies) < 4 {
		t.Fatalf("full snapshot missing catalog: %s", rr.Body.String())
	}
}

func TestInternalSyncRejectsInvalidSinceAndUnknownTableSafely(t *testing.T) {
	f := newFx(t)
	_, key, err := f.r.CreateWorker(context.Background(), "edge", "https://edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/internal/sync?since=-1", nil)
	req.Header.Set("X-Worker-Key", key)
	rr := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_since") {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}

func TestWorkerNotifyRequiresSeparateSharedSecret(t *testing.T) {
	cfg := &config.Config{Role: config.RoleWorker, SyncNotifyKey: strings.Repeat("n", 40)}
	s := New(Deps{Cfg: cfg})
	call := func(secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/internal/notify", strings.NewReader(`{}`))
		req.Header.Set("X-Notify", secret)
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr
	}
	if rr := call("wrong"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong notify key: %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(strings.Repeat("n", 40)); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("valid key without initialized syncer should fail closed: %d %s", rr.Code, rr.Body.String())
	}
}

func TestMainSendsWorkerNotificationWithSharedKey(t *testing.T) {
	var got bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method == http.MethodPost && r.URL.Path == "/base/internal/notify" && r.Header.Get("X-Notify") == strings.Repeat("s", 40)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer worker.Close()
	cfg := &config.Config{Role: config.RoleMain, SyncNotifyKey: strings.Repeat("s", 40)}
	s := New(Deps{Cfg: cfg})
	if err := s.sendWorkerNotification(context.Background(), worker.Client(), worker.URL+"/base"); err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("worker notification request was not correctly authenticated")
	}
}

func TestServerDiscoveryExcludesWorkersAcknowledgedInOldEpoch(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	worker, _, err := f.r.CreateWorker(ctx, "edge", "https://edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := f.r.EnsureEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.r.AcknowledgeWorker(ctx, worker.ID, epoch, 0); err != nil {
		t.Fatal(err)
	}
	if rr := f.get("/api/v1/servers", ""); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "edge.example.com") {
		t.Fatalf("current worker should be discoverable: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := f.r.ResetEpoch(ctx); err != nil {
		t.Fatal(err)
	}
	if rr := f.get("/api/v1/servers", ""); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "edge.example.com") {
		t.Fatalf("old-epoch worker must not be discoverable: %d %s", rr.Code, rr.Body.String())
	}
}
