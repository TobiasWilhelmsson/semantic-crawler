package semantic

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"semantic-crawler/internal/models"
)

func TestGormStorePersistsCandidateHistoryAndScopedRules(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SemanticCrawlRun{}, &models.SemanticQueueItem{}, &models.SemanticPageVersion{}, &models.SemanticCandidate{}, &models.SemanticReview{}, &models.SemanticSkillRule{}, &models.SemanticDelivery{}); err != nil {
		t.Fatal(err)
	}
	store := NewGormStore(db)
	ctx := context.Background()

	page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 4, URL: "https://example.test/a", ContentHash: "hash-one", SnapshotHTML: "<p>A</p>"})
	if err != nil || page.ID == 0 {
		t.Fatalf("save page: %#v %v", page, err)
	}
	candidate, err := store.SaveCandidate(ctx, Candidate{SourceID: 4, PageVersionID: page.ID, URL: page.URL, Status: CandidatePendingReview, Sections: []Section{{BlockID: "b0001", Content: "<p>A</p>"}}, SkillVersion: 1, SourceSkillHash: "abc123", SourceSkillVersion: 7, AppliedRules: []byte(`[{"instruction":"keep tables"}]`)})
	if err != nil || candidate.ID == 0 {
		t.Fatalf("save candidate: %#v %v", candidate, err)
	}
	if err := store.SetCandidateStatus(ctx, candidate.ID, CandidateApproved); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Candidate(ctx, candidate.ID)
	if err != nil || stored.Status != CandidateApproved || stored.Sections[0].Content != "<p>A</p>" || stored.SourceSkillHash != "abc123" || stored.SourceSkillVersion != 7 || len(stored.AppliedRules) == 0 {
		t.Fatalf("candidate did not round trip: %#v %v", stored, err)
	}

	_, err = store.AddRule(ctx, SkillRule{SourceID: 4, Scope: RuleScopeSource, Instruction: "retain tables", Version: 2, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddRule(ctx, SkillRule{SourceID: 4, OriginURL: page.URL, Scope: RuleScopeURL, Instruction: "page only", Version: 3, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	rules, err := store.Rules(ctx, 4, "https://example.test/other")
	if err != nil || len(rules) != 1 || rules[0].Instruction != "retain tables" {
		t.Fatalf("unexpected rules for another page: %#v %v", rules, err)
	}
}

func TestGormStoreTracksRunQueueChecksReviewsAndDeliveries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SemanticCrawlRun{}, &models.SemanticQueueItem{}, &models.SemanticPageVersion{}, &models.SemanticCandidate{}, &models.SemanticReview{}, &models.SemanticDelivery{}); err != nil {
		t.Fatal(err)
	}
	store := NewGormStore(db)
	ctx := context.Background()
	runID, err := store.StartCrawlRun(ctx, CrawlConfig{SourceID: 7, SeedURL: "https://example.test/", Mode: "snapshot", Model: "fixture", PromptVersion: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertQueueItem(ctx, QueueItem{RunID: runID, URL: "https://example.test/", Status: QueuePending}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateQueueItem(ctx, runID, "https://example.test/", QueueCompleted, RouteExtract, ""); err != nil {
		t.Fatal(err)
	}
	page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 7, RunID: runID, URL: "https://example.test/", ContentHash: "abc", CheckedAt: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Now().UTC()
	if err := store.TouchPageCheckedAt(ctx, page.ID, checkedAt); err != nil {
		t.Fatal(err)
	}
	latest, _, _ := store.LatestPageVersion(ctx, 7, page.URL)
	if latest.CheckedAt.Before(checkedAt.Add(-time.Second)) {
		t.Fatalf("last checked was not updated: %s", latest.CheckedAt)
	}
	if err := store.AddReview(ctx, Review{CandidateID: 9, Reviewer: "r", Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDelivery(ctx, DeliveryRecord{CandidateID: 9, Endpoint: "https://receiver.test", Status: "succeeded", StatusCode: 202}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishCrawlRun(ctx, runID, CrawlSummary{Discovered: 1}, ""); err != nil {
		t.Fatal(err)
	}
	var run models.SemanticCrawlRun
	if err := db.First(&run, runID).Error; err != nil || run.Status != "completed" {
		t.Fatalf("run not completed: %#v %v", run, err)
	}
}
