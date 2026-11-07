//go:build !nodriver

package repo

import (
	"context"
	"testing"
)

func TestApplyFullAndIncrementalReplication(t *testing.T) {
	ctx := context.Background()
	mainRepo := newTestRepo(t)
	workerRepo := newTestRepo(t)
	if _, err := mainRepo.CreateMovie(ctx, MovieInput{Title: "Replica Film", ContentType: "movie", Type: "Movie", Genres: []string{"Action"}, Languages: []string{"English"}, Links: []LinkInput{{Kind: "download", URL: "https://cdn.example.test/film.mp4", Quality: "720p"}}}); err != nil {
		t.Fatal(err)
	}
	epoch, err := mainRepo.EnsureEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	full, err := mainRepo.ReplicationBatch(ctx, "", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !full.Full || full.Epoch != epoch {
		t.Fatalf("expected full snapshot, got %#v", full)
	}
	if err := workerRepo.ApplyReplicationBatch(ctx, *full); err != nil {
		t.Fatal(err)
	}
	got, err := workerRepo.GetMovie(ctx, full.Snapshot.Movies[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Replica Film" || len(got.Links) != 1 || got.Links[0].URL != "https://cdn.example.test/film.mp4" {
		t.Fatalf("full snapshot movie mismatch: %#v", got)
	}

	second, err := mainRepo.CreateMovie(ctx, MovieInput{Title: "Incremental Film", ContentType: "movie", Type: "Movie", Genres: []string{"Comedy"}, Languages: []string{"English"}})
	if err != nil {
		t.Fatal(err)
	}
	incremental, err := mainRepo.ReplicationBatch(ctx, epoch, full.MaxSeq, 200)
	if err != nil {
		t.Fatal(err)
	}
	if incremental.Full || len(incremental.Changes) == 0 {
		t.Fatalf("expected incremental changes: %#v", incremental)
	}
	if err := workerRepo.ApplyReplicationBatch(ctx, *incremental); err != nil {
		t.Fatal(err)
	}
	if _, err := workerRepo.GetMovie(ctx, second.ID); err != nil {
		t.Fatalf("incremental movie missing: %v", err)
	}

	if err := mainRepo.DeleteMovie(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	deletion, err := mainRepo.ReplicationBatch(ctx, epoch, incremental.Changes[len(incremental.Changes)-1].Seq, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := workerRepo.ApplyReplicationBatch(ctx, *deletion); err != nil {
		t.Fatal(err)
	}
	if _, err := workerRepo.GetMovie(ctx, second.ID); err == nil {
		t.Fatal("deleted movie still exists on worker")
	}
}

func TestApplyReplicationBatchRollsBackOnInvalidSnapshot(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	before, _, err := r.ListMovies(ctx, ListOpts{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	bad := ReplicationBatch{Epoch: "epoch", Full: true, Snapshot: &ReplicationSnapshot{
		Categories: []Category{{ID: "same-a", Name: "Duplicate"}, {ID: "same-b", Name: "Duplicate"}},
		Settings:   map[string]string{"appName": "MoveX"},
	}}
	if err := r.ApplyReplicationBatch(ctx, bad); err == nil {
		t.Fatal("expected invalid snapshot to fail")
	}
	after, _, err := r.ListMovies(ctx, ListOpts{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("failed batch changed movie count: before=%d after=%d", len(before), len(after))
	}
	epoch, seq, err := r.ReplicaCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if epoch != "" || seq != 0 {
		t.Fatalf("failed batch advanced cursor: %q/%d", epoch, seq)
	}
}

func TestReplicationBatchFallsBackToFullWhenChangeLogIsEmptyOrCursorAhead(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	if _, err := r.CreateMovie(ctx, MovieInput{Title: "History", ContentType: "movie", Type: "Movie", Genres: []string{"Action"}, Languages: []string{"English"}}); err != nil {
		t.Fatal(err)
	}
	epoch, err := r.EnsureEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	maxSeq, err := r.MaxSeq(ctx)
	if err != nil || maxSeq == 0 {
		t.Fatalf("expected a non-empty change log, seq=%d err=%v", maxSeq, err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM changes`); err != nil {
		t.Fatal(err)
	}
	for _, since := range []int64{0, maxSeq + 10} {
		batch, err := r.ReplicationBatch(ctx, epoch, since, 200)
		if err != nil {
			t.Fatal(err)
		}
		if !batch.Full || batch.Snapshot == nil {
			t.Fatalf("cursor %d must receive a full snapshot after history loss: %#v", since, batch)
		}
	}
}

func TestReplicationBatchRejectsNegativeCursor(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.ReplicationBatch(context.Background(), "epoch", -1, 200); err == nil {
		t.Fatal("expected negative cursor to be rejected")
	}
}
