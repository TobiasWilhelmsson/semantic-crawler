package semantic

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"semantic-crawler/internal/models"
)

func TestRetryProcessorReextractsQueuedCandidateWithApplicableRules(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/a", RawHTML: `<html><body><main><h1>A</h1><p>Clinical guidance.</p></main></body></html>`})
	queued := store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidateQueued, SkillVersion: 2})
	_, _ = store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "retain guidance", Version: 2, Status: "active"})
	model := &recordingModel{}
	processor := NewRetryProcessor(store, model, CrawlConfig{Scope: "clinical", Model: "test", PromptVersion: "p1"})

	if err := processor.Retry(context.Background(), queued.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Candidate(context.Background(), queued.ID)
	if stored.Status != CandidatePendingReview || len(stored.Sections) != 2 || stored.Model != "test" {
		t.Fatalf("queued retry was not re-extracted: %#v", stored)
	}
	if len(model.extractCalls) != 1 || !strings.Contains(model.extractCalls[0], "retain guidance") {
		t.Fatalf("applicable rule was not included in retry prompt: %#v", model.extractCalls)
	}
}

func TestRetryProcessorPromptIncludesOnlyApplicableActiveRules(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/feet-warning", RawHTML: `<html><body><main><h1>A</h1><aside>Warning about feet.</aside><p>Clinical guidance.</p></main></body></html>`})
	queued := store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidateQueued, SkillVersion: 5})
	_, _ = store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "source rule: remove share controls", Version: 2, Status: "active"})
	_, _ = store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeURL, OriginURL: page.URL, Instruction: "url rule: only keep this warning box when text includes a note about feet", Version: 3, Status: "active"})
	_, _ = store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeURL, OriginURL: "https://example.test/other-page", Instruction: "other url rule should not appear", Version: 4, Status: "active"})
	disabled, _ := store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "disabled rule should not appear", Version: 5, Status: "active"})
	if err := store.DisableRule(context.Background(), disabled.ID); err != nil {
		t.Fatal(err)
	}
	model := &recordingModel{}
	processor := NewRetryProcessor(store, model, CrawlConfig{Scope: "clinical", Model: "test", PromptVersion: "p1"})

	if err := processor.Retry(context.Background(), queued.ID); err != nil {
		t.Fatal(err)
	}
	if len(model.extractCalls) != 1 {
		t.Fatalf("expected one extraction call, got %#v", model.extractCalls)
	}
	prompt := model.extractCalls[0]
	for _, expected := range []string{"source rule: remove share controls", "url rule: only keep this warning box when text includes a note about feet"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing applicable rule %q: %s", expected, prompt)
		}
	}
	for _, unexpected := range []string{"other url rule should not appear", "disabled rule should not appear"} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("prompt included non-applicable rule %q: %s", unexpected, prompt)
		}
	}
	stored, _ := store.Candidate(context.Background(), queued.ID)
	if stored.Status != CandidatePendingReview {
		t.Fatalf("queued candidate was not re-extracted: %#v", stored)
	}
}

func TestRetryProcessorRejectsInvalidModelExtraction(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/a", RawHTML: `<html><body><main><h1>A</h1></main></body></html>`})
	queued := store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidateQueued})
	processor := NewRetryProcessor(store, &invalidExtractionModel{}, CrawlConfig{Scope: "clinical"})

	if err := processor.Retry(context.Background(), queued.ID); err == nil {
		t.Fatal("expected invalid retry extraction to fail")
	}
	stored, _ := store.Candidate(context.Background(), queued.ID)
	if stored.Status != CandidateQueued || len(stored.Sections) != 0 {
		t.Fatalf("invalid retry should not mutate queued candidate: %#v", stored)
	}
}

type blockedRetryModel struct {
	FixtureModel
	entered chan struct{}
	release chan struct{}
}

func (model *blockedRetryModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	close(model.entered)
	select {
	case <-model.release:
		return model.FixtureModel.Extract(ctx, prompt, blocks)
	case <-ctx.Done():
		return ExtractionResponse{}, ModelUsage{}, ctx.Err()
	}
}

func TestRetryProcessorDoesNotOverwriteApprovalFromFasterResume(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var store RetryStore
			var page PageVersion
			var reviewCount func() int64
			if backend == "memory" {
				memory := NewMemoryStore()
				store = memory
				page = memory.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/a", RawHTML: "<main><h1>A</h1></main>"})
				reviewCount = func() int64 { return int64(len(memory.Reviews())) }
			} else {
				db, sqliteStore := newReviewTestStore(t)
				if err := db.AutoMigrate(&models.SemanticPageVersion{}); err != nil {
					t.Fatal(err)
				}
				store = sqliteStore
				var err error
				page, err = sqliteStore.SavePageVersion(ctx, PageVersion{SourceID: 1, URL: "https://example.test/a", RawHTML: "<main><h1>A</h1></main>"})
				if err != nil {
					t.Fatal(err)
				}
				reviewCount = func() int64 {
					var count int64
					if err := db.Model(&models.SemanticReview{}).Count(&count).Error; err != nil {
						t.Fatal(err)
					}
					return count
				}
			}
			queued, err := store.SaveCandidate(ctx, Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidateQueued})
			if err != nil {
				t.Fatal(err)
			}
			blocked := &blockedRetryModel{entered: make(chan struct{}), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(blocked.release) })
			t.Cleanup(release)
			slow := NewRetryProcessor(store, blocked, CrawlConfig{Model: "slow"})
			fast := NewRetryProcessor(store, &FixtureModel{}, CrawlConfig{Model: "fast"})
			done := make(chan error, 1)
			go func() { done <- slow.Retry(ctx, queued.ID) }()
			select {
			case <-blocked.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("slow retry did not enter model extraction")
			}
			if err := fast.Retry(ctx, queued.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := NewReviewService(store).Approve(ctx, queued.ID, "reviewer"); err != nil {
				t.Fatal(err)
			}
			release()
			select {
			case err := <-done:
				if err == nil {
					t.Error("stale retry result should be rejected")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("slow retry did not finish")
			}
			result, err := store.Candidate(ctx, queued.ID)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != CandidateApproved || result.ApprovedBy != "reviewer" || result.ApprovedAt == nil || result.Model != "fast" || reviewCount() != 1 {
				t.Fatalf("slow retry overwrote completed review: status=%s reviewer=%q approved=%t model=%q reviews=%d", result.Status, result.ApprovedBy, result.ApprovedAt != nil, result.Model, reviewCount())
			}
		})
	}
}
