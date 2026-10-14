package semantic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"semantic-crawler/internal/models"
)

func TestScanStoreReservationAndTerminalState(t *testing.T) {
	_, store := newCrawlRecoveryStore(t)
	ctx := context.Background()
	config := CrawlConfig{SourceID: 2, SeedURL: "https://example.test/", MaxPages: 10}
	request := ScanRequest{SourceSlug: "sample", RequestKey: "one", ModelMode: "fixture", MaxPages: 10}
	run, existing, err := store.ReserveScan(ctx, config, request, "identity")
	if err != nil || existing || run.State != ScanQueued {
		t.Fatalf("reserve: %#v %v %v", run, existing, err)
	}
	same, existing, err := store.ReserveScan(ctx, config, request, "identity")
	if err != nil || !existing || same.ID != run.ID {
		t.Fatalf("duplicate: %#v %v %v", same, existing, err)
	}
	if _, _, err = store.ReserveScan(ctx, config, request, "changed"); !errors.Is(err, ErrScanRequestConflict) {
		t.Fatalf("changed settings: %v", err)
	}
	if err = store.BeginScan(ctx, run.ID, "identity"); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertQueueItem(ctx, QueueItem{RunID: run.ID, URL: config.SeedURL, Status: QueueProcessing}); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateQueueItem(ctx, run.ID, config.SeedURL, QueueFailed, RouteExtract, context.Canceled.Error()); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishScan(ctx, run.ID, ScanCancelled, CrawlSummary{Discovered: 1}, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishScan(ctx, run.ID, ScanCompleted, CrawlSummary{}, nil); err == nil {
		t.Fatal("late finish overwrote cancellation")
	}
	saved, err := store.ScanRun(ctx, run.ID)
	if err != nil || saved.State != ScanCancelled || saved.CompletedAt == nil {
		t.Fatalf("saved: %#v %v", saved, err)
	}
	if !strings.Contains(saved.Error, "Scan cancelled") || strings.Contains(saved.Error, "configuration") {
		t.Fatalf("cancellation should explain recovery without blaming configuration: %q", saved.Error)
	}
	progress, err := store.ScanProgress(ctx, run.ID)
	if err != nil || len(progress.Pages) != 1 || !strings.Contains(progress.Pages[0].Error, "cancelled scan") {
		t.Fatalf("cancelled progress should explain recovery: %#v, %v", progress, err)
	}
}

func TestScanProgressKeepsRunCandidatesAndOutcomesDistinct(t *testing.T) {
	_, store := newCrawlRecoveryStore(t)
	ctx := context.Background()
	id, err := store.StartCrawlRun(ctx, CrawlConfig{SourceID: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"a", "b", "c"} {
		if err = store.UpsertQueueItem(ctx, QueueItem{RunID: id, URL: url, Status: QueuePending}); err != nil {
			t.Fatal(err)
		}
	}
	for url, outcome := range map[string]string{"a": "unchanged", "b": "candidate"} {
		if err = store.RecordQueueOutcome(ctx, id, url, outcome); err != nil {
			t.Fatal(err)
		}
		if err = store.UpdateQueueItem(ctx, id, url, QueueCompleted, RouteExtract, ""); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 2, RunID: id, URL: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveCandidate(ctx, Candidate{SourceID: 2, PageVersionID: page.ID, Status: CandidatePendingReview}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveCandidate(ctx, Candidate{SourceID: 2, Status: CandidatePendingReview}); err != nil {
		t.Fatal(err)
	}
	progress, err := store.ScanProgress(ctx, id)
	if err != nil || progress.Discovered != 3 || progress.Waiting != 1 || progress.Finished != 2 || progress.Unchanged != 1 || progress.Candidates != 1 {
		t.Fatalf("progress: %#v %v", progress, err)
	}
	candidates, err := store.CandidatesForRun(ctx, id)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates: %#v %v", candidates, err)
	}
	var rows []models.SemanticCrawlRun
	if err = store.db.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].RequestKey != nil {
		t.Fatalf("CLI request key: %#v %v", rows, err)
	}
}

func TestScanProgressDoesNotDecodeCandidatePayloads(t *testing.T) {
	db, store := newCrawlRecoveryStore(t)
	ctx := context.Background()
	id, err := store.StartCrawlRun(ctx, CrawlConfig{SourceID: 2})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 2, RunID: id})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.SaveCandidate(ctx, Candidate{SourceID: 2, PageVersionID: page.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&models.SemanticCandidate{}).Where("id = ?", candidate.ID).Update("sections", "not-json").Error; err != nil {
		t.Fatal(err)
	}
	progress, err := store.ScanProgress(ctx, id)
	if err != nil || progress.Candidates != 1 {
		t.Fatalf("progress decoded an unused payload: %#v %v", progress, err)
	}
}

func TestScanStoreReadsLegacySchemaWithoutMigration(t *testing.T) {
	db, store := newCrawlRecoveryStore(t)
	ctx := context.Background()
	id, err := store.StartCrawlRun(ctx, CrawlConfig{SourceID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertQueueItem(ctx, QueueItem{RunID: id, URL: "https://example.test/", Status: QueueCompleted}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DROP INDEX idx_scan_request", "ALTER TABLE semantic_crawl_runs DROP COLUMN request_key", "ALTER TABLE semantic_crawl_runs DROP COLUMN request_identity", "ALTER TABLE semantic_queue_items DROP COLUMN outcome"} {
		if err = db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	progress, err := store.ScanProgress(ctx, id)
	if err != nil || progress.Finished != 1 || progress.Unknown != 1 {
		t.Fatalf("legacy progress: %#v %v", progress, err)
	}
	if db.Migrator().HasColumn(&models.SemanticQueueItem{}, "outcome") {
		t.Fatal("read migrated the database")
	}
}
