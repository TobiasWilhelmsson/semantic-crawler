package semantic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"semantic-crawler/internal/models"
)

type crawlRecoveryFetcher func(context.Context, string) (FetchResult, error)

func (fetch crawlRecoveryFetcher) Fetch(ctx context.Context, url string) (FetchResult, error) {
	return fetch(ctx, url)
}

type crawlRecoveryModel struct {
	recordingModel
	action RouteAction
}

func (model *crawlRecoveryModel) Route(_ context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	model.routeCalls = append(model.routeCalls, prompt)
	return RouteResponse{Action: model.action, Confidence: 1, Reason: "source page"}, ModelUsage{}, nil
}

func newCrawlRecoveryStore(t *testing.T) (*gorm.DB, *GormStore) {
	t.Helper()
	db, store := newReviewTestStore(t)
	if err := db.AutoMigrate(&models.SemanticCrawlRun{}, &models.SemanticQueueItem{}, &models.SemanticPageVersion{}); err != nil {
		t.Fatal(err)
	}
	return db, store
}

func TestPipelineCandidateCompletionFailureRollsBackCandidate(t *testing.T) {
	for _, action := range []RouteAction{RouteExtract, RouteNeedsReview} {
		t.Run(string(action), func(t *testing.T) {
			db, store := newCrawlRecoveryStore(t)
			target := "https://example.test/article/"
			config := CrawlConfig{SourceID: 1, SeedURL: target, AllowedHost: "example.test", MaxPages: 1}
			pipeline := NewPipeline(mapFetcher{target: `<main><p>Source content</p></main>`}, &crawlRecoveryModel{action: action}, store)
			if err := db.Exec("CREATE TRIGGER fail_complete BEFORE UPDATE OF processed ON semantic_page_versions WHEN NEW.processed = 1 BEGIN SELECT RAISE(ABORT, 'forced completion failure'); END").Error; err != nil {
				t.Fatal(err)
			}
			if _, err := pipeline.Run(context.Background(), config); err == nil || !strings.Contains(err.Error(), "forced completion failure") {
				t.Fatalf("expected completion failure, got %v", err)
			}
			candidates, err := store.CandidatesByStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 0 {
				t.Fatalf("failed completion left %d review candidates", len(candidates))
			}
			captured, exists, err := store.LatestPageVersion(context.Background(), 1, target)
			if err != nil || !exists || captured.Processed || captured.RawHTML == "" {
				t.Fatalf("initial snapshot should survive failed processing: page=%+v exists=%v err=%v", captured, exists, err)
			}
			if err := db.Exec("DROP TRIGGER fail_complete").Error; err != nil {
				t.Fatal(err)
			}
			if _, err := pipeline.Run(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			candidates, err = store.CandidatesByStatus(context.Background(), CandidatePendingReview)
			if err != nil || len(candidates) != 1 {
				t.Fatalf("recovery should produce exactly one candidate: count=%d err=%v", len(candidates), err)
			}
			latest, exists, err := store.LatestPageVersion(context.Background(), 1, target)
			if err != nil || !exists || !latest.Processed {
				t.Fatalf("recovery was not marked processed: page=%+v exists=%v err=%v", latest, exists, err)
			}
		})
	}
}

func TestPipelineRedirectAliasDoesNotDisplaceChildAtPageLimit(t *testing.T) {
	seed := "https://example.test/start"
	final := "https://example.test/start/"
	child := "https://example.test/start/child/"
	fetcher := crawlRecoveryFetcher(func(_ context.Context, target string) (FetchResult, error) {
		if target == seed || target == final {
			return FetchResult{URL: final, StatusCode: 200, HTML: `<main><p>Start</p><a href="/start/">Home</a><a href="child/">Child</a></main>`}, nil
		}
		return FetchResult{URL: target, StatusCode: 200, HTML: `<main><p>Child content</p></main>`}, nil
	})
	store := NewMemoryStore()
	summary, err := NewPipeline(fetcher, &recordingModel{}, store).Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: seed, AllowedHost: "example.test", MaxPages: 2, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	candidates := store.Candidates()
	if summary.Discovered != 2 || summary.PendingReview != 2 || summary.Unchanged != 0 || len(candidates) != 2 || candidates[1].URL != child {
		t.Fatalf("redirect alias displaced child: summary=%+v candidates=%+v", summary, candidates)
	}
}

func TestPipelineDiscoveryUsesTheFetchPathBoundary(t *testing.T) {
	for _, test := range []struct{ name, prefix, html, child string }{
		{"similar path", "/guide", `<main><p>Start</p><a href="/guidebook/">Outside</a><a href="/guide/child/">Child</a></main>`, "https://example.test/guide/child/"},
		{"boundary without slash", "/guide/", `<main><p>Start</p><a href="/guide">Guide root</a></main>`, "https://example.test/guide"},
	} {
		t.Run(test.name, func(t *testing.T) {
			seed := "https://example.test/guide/start/"
			store := NewMemoryStore()
			summary, err := NewPipeline(mapFetcher{seed: test.html, test.child: `<main><p>Child content</p></main>`}, &recordingModel{}, store).Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: seed, AllowedHost: "example.test", AllowedPathPrefix: test.prefix, MaxPages: 2, MaxDepth: 1})
			if err != nil {
				t.Fatal(err)
			}
			candidates := store.Candidates()
			if summary.Discovered != 2 || summary.PendingReview != 2 || summary.Failed != 0 || len(candidates) != 2 || candidates[1].URL != test.child {
				t.Fatalf("discovery disagreed with the configured boundary: summary=%+v candidates=%+v", summary, candidates)
			}
		})
	}
}

func TestPipelineCancellationStopsTraversalAndFinalizesRun(t *testing.T) {
	db, store := newCrawlRecoveryStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seed := "https://example.test/start/"
	firstChild := seed + "a/"
	var fetched []string
	fetcher := crawlRecoveryFetcher(func(_ context.Context, target string) (FetchResult, error) {
		fetched = append(fetched, target)
		if target == seed {
			return FetchResult{URL: seed, StatusCode: 200, HTML: `<main><p>Start</p><a href="a/">A</a><a href="b/">B</a></main>`}, nil
		}
		cancel()
		return FetchResult{}, ctx.Err()
	})
	_, err := NewPipeline(fetcher, &recordingModel{}, store).Run(ctx, CrawlConfig{SourceID: 1, SeedURL: seed, AllowedHost: "example.test", MaxDepth: 1, MaxPages: 3})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if len(fetched) != 2 {
		t.Errorf("cancellation continued into another page: %v", fetched)
	}
	var run models.SemanticCrawlRun
	if err := db.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" || run.CompletedAt == nil || !strings.Contains(run.Error, "context canceled") {
		t.Errorf("canceled crawl was not finalized: %+v", run)
	}
	var queue models.SemanticQueueItem
	if err := db.Where("canonical_url = ?", firstChild).First(&queue).Error; err != nil {
		t.Fatal(err)
	}
	if queue.Status != string(QueueFailed) || !strings.Contains(queue.LastError, "context canceled") {
		t.Errorf("canceled page was not finalized: %+v", queue)
	}
}

func TestPipelineTrackerPersistenceFailuresFailRun(t *testing.T) {
	for _, test := range []struct{ name, trigger string }{
		{"start page", "CREATE TRIGGER fail_tracking BEFORE UPDATE ON semantic_queue_items WHEN NEW.status = 'processing' BEGIN SELECT RAISE(ABORT, 'forced tracker failure'); END"},
		{"complete page", "CREATE TRIGGER fail_tracking BEFORE UPDATE ON semantic_queue_items WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'forced tracker failure'); END"},
		{"discover child", "CREATE TRIGGER fail_tracking BEFORE INSERT ON semantic_queue_items WHEN NEW.canonical_url LIKE '%/child/' BEGIN SELECT RAISE(ABORT, 'forced tracker failure'); END"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, store := newCrawlRecoveryStore(t)
			if err := db.Exec(test.trigger).Error; err != nil {
				t.Fatal(err)
			}
			seed := "https://example.test/start/"
			_, err := NewPipeline(mapFetcher{seed: `<main><p>Source content</p><a href="child/">Child</a></main>`, seed + "child/": `<main><p>Child content</p></main>`}, &recordingModel{}, store).Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: seed, AllowedHost: "example.test", MaxPages: 2, MaxDepth: 1})
			if err == nil || !strings.Contains(err.Error(), "forced tracker failure") {
				t.Fatalf("tracking failure was silently accepted: %v", err)
			}
			var run models.SemanticCrawlRun
			if err := db.First(&run).Error; err != nil {
				t.Fatal(err)
			}
			if run.Status != "failed" || run.CompletedAt == nil {
				t.Fatalf("tracking failure was recorded as a successful run: %+v", run)
			}
		})
	}
}

func TestPipelineCheckedAtPersistenceFailureFailsRun(t *testing.T) {
	db, store := newCrawlRecoveryStore(t)
	seed := "https://example.test/start/"
	pipeline := NewPipeline(mapFetcher{seed: `<main><p>Source content</p></main>`}, &recordingModel{}, store)
	config := CrawlConfig{SourceID: 1, SeedURL: seed, AllowedHost: "example.test", MaxPages: 1}
	if _, err := pipeline.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER fail_checked_at BEFORE UPDATE OF last_checked_at ON semantic_page_versions BEGIN SELECT RAISE(ABORT, 'forced checked-at failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Run(context.Background(), config); err == nil || !strings.Contains(err.Error(), "forced checked-at failure") {
		t.Fatalf("failed unchanged-page persistence was silently accepted: %v", err)
	}
}
