package semantic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"semantic-crawler/internal/models"
)

type transientRouteModel struct {
	recordingModel
	failed bool
}

func (m *transientRouteModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	if !m.failed {
		m.failed = true
		return RouteResponse{}, ModelUsage{}, fmt.Errorf("temporary model failure")
	}
	return m.recordingModel.Route(ctx, prompt)
}

func TestFailedExtractionCanRecoverWithoutChangedHTML(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "review.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SemanticSource{}, &models.SemanticCrawlRun{}, &models.SemanticQueueItem{}, &models.SemanticPageVersion{}, &models.SemanticCandidate{}, &models.SemanticReview{}, &models.SemanticSkillRule{}, &models.SemanticDelivery{}); err != nil {
		t.Fatal(err)
	}
	store := NewGormStore(db)
	target := "https://example.test/article/"
	fetcher := mapFetcher{target: "<main><p>Retain this source content.</p></main>"}
	model := &transientRouteModel{}
	config := CrawlConfig{SourceID: 1, SeedURL: target, AllowedHost: "example.test", MaxPages: 1}
	first, err := NewPipeline(fetcher, model, store).Run(context.Background(), config)
	if err != nil || first.Failed != 1 {
		t.Fatalf("first failure missing: %#v %v", first, err)
	}
	// Reopen the persistent store: completion must survive a process restart.
	recovered, err := NewPipeline(fetcher, model, NewGormStore(db)).Run(context.Background(), config)
	if err != nil || recovered.PendingReview != 1 || recovered.Unchanged != 0 {
		t.Fatalf("failed page was permanently cached: %#v %v", recovered, err)
	}
	third, err := NewPipeline(fetcher, model, NewGormStore(db)).Run(context.Background(), config)
	if err != nil || third.Unchanged != 1 || len(model.extractCalls) != 1 {
		t.Fatalf("successful page did not skip later model calls: %#v %v", third, err)
	}
}

type fixedResultFetcher struct{ result FetchResult }

func (f fixedResultFetcher) Fetch(context.Context, string) (FetchResult, error) { return f.result, nil }

func TestPipelineRejectsHTTPErrorPagesBeforeModelOrPersistence(t *testing.T) {
	for _, status := range []int{404, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			target := "https://example.test/article/"
			store := NewMemoryStore()
			model := &recordingModel{}
			run, err := NewPipeline(fixedResultFetcher{FetchResult{URL: target, StatusCode: status, HTML: "<main><p>Error content</p></main>"}}, model, store).Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: target, AllowedHost: "example.test"})
			if err != nil || run.Failed != 1 || len(model.routeCalls) != 0 || len(store.Candidates()) != 0 {
				t.Fatalf("HTTP error became content: %#v %v", run, err)
			}
			_, found, _ := store.LatestPageVersion(context.Background(), 1, target)
			if found {
				t.Fatal("failed HTTP response replaced content state")
			}
		})
	}
}

func TestPipelinePreventsRedirectsOutsideConfiguredPath(t *testing.T) {
	outside := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/allowed/start/" {
			http.Redirect(w, r, "/outside/private/", http.StatusFound)
			return
		}
		outside++
		fmt.Fprint(w, "<main><p>Outside content</p></main>")
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	store := NewMemoryStore()
	model := &recordingModel{}
	run, err := NewPipeline(&HTTPFetcher{}, model, store).Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: server.URL + "/allowed/start/", AllowedHost: parsed.Hostname(), AllowedPathPrefix: "/allowed/", MaxPages: 1})
	if err != nil || run.Failed != 1 || outside != 0 || len(model.routeCalls) != 0 {
		t.Fatalf("redirect escaped scope: outside=%d run=%#v err=%v", outside, run, err)
	}
}

func TestPipelineRejectsSeedOutsideConfiguredScope(t *testing.T) {
	model := &recordingModel{}
	_, err := NewPipeline(mapFetcher{"https://example.test/outside/": "<p>Outside</p>"}, model, NewMemoryStore()).Run(context.Background(), CrawlConfig{SeedURL: "https://example.test/outside/", AllowedHost: "example.test", AllowedPathPrefix: "/allowed/"})
	if err == nil || len(model.routeCalls) != 0 {
		t.Fatal("out-of-scope seed was accepted")
	}
}

func TestContentHashDetectsLinkAndAssetChanges(t *testing.T) {
	for _, pair := range [][2]string{
		{`<main><p><a href="/old">Download guide</a></p></main>`, `<main><p><a href="/new">Download guide</a></p></main>`},
		{`<main><figure><img src="/old.png" alt="Diagram"></figure></main>`, `<main><figure><img src="/new.png" alt="Diagram"></figure></main>`},
	} {
		before, _, _ := BuildCandidateBlocks(strings.NewReader(pair[0]), "https://example.test/")
		after, _, _ := BuildCandidateBlocks(strings.NewReader(pair[1]), "https://example.test/")
		if ContentHash(before) == ContentHash(after) {
			t.Fatal("changed link/media destination was cached as unchanged")
		}
	}
}

func TestHTTPFetcherRejectsOversizedDecodedPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, strings.Repeat("a", 9*1024*1024)) }))
	defer server.Close()
	_, err := (&HTTPFetcher{}).Fetch(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized page accepted: %v", err)
	}
}

func TestDeliveryRedirectDoesNotSendPayloadToUnlistedDestination(t *testing.T) {
	received := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { received++; w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	status, _, err := NewHTTPDeliveryClient(nil).Send(context.Background(), source.URL, DeliveryPayload{CandidateID: 1, Markdown: "private payload"})
	if received != 0 || err == nil || status != http.StatusTemporaryRedirect {
		t.Fatalf("redirect sent approved content elsewhere: requests=%d status=%d err=%v", received, status, err)
	}
}

func TestRestrictedDeliveryFailsClosedWithEmptyAllowlist(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	candidate := store.PutCandidate(Candidate{Status: CandidateApproved, ApprovedAt: &now})
	_, err := NewRestrictedDeliveryService(store, NewHTTPDeliveryClient(nil), nil).Deliver(context.Background(), candidate.ID, "http://127.0.0.1:1/approved")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("empty restricted allowlist was not enforced: %v", err)
	}
}

func TestRemoveAndApproveRequiresReviewableStateAndKnownBlocks(t *testing.T) {
	for _, state := range []CandidateStatus{CandidateRejected, CandidateSuperseded, CandidateQueued, CandidateApproved, CandidatePendingReview} {
		t.Run(string(state), func(t *testing.T) {
			store := NewMemoryStore()
			original := store.PutCandidate(Candidate{Status: state, Sections: []Section{{BlockID: "b0001", Role: RoleContent, Type: TypeNone, ParseMode: true, Content: "<p>Source</p>"}}})
			_, err := NewReviewService(store).RemoveSelectedAndApprove(context.Background(), original.ID, "reviewer", []string{"missing"}, "remove")
			if err == nil || len(store.Candidates()) != 1 {
				t.Fatalf("invalid removal approved content from state %s: %v", state, err)
			}
		})
	}
}

func TestCrossOriginReviewMutationIsRejectedEvenWithToken(t *testing.T) {
	store := NewMemoryStore()
	candidate := store.PutCandidate(Candidate{Status: CandidatePendingReview})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	request := newLocalReviewRequest(http.MethodPost, fmt.Sprintf("/semantic/review/%d/approve", candidate.ID), strings.NewReader("csrf_token=secret&reviewer=attacker"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://untrusted.example")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	stored, _ := store.Candidate(context.Background(), candidate.ID)
	if result.Code != http.StatusForbidden || stored.Status != CandidatePendingReview {
		t.Fatalf("foreign origin approved candidate: status=%d candidate=%s", result.Code, stored.Status)
	}
}

func TestDefaultReviewTokenIsNotPublicConstant(t *testing.T) {
	store := NewMemoryStore()
	candidate := store.PutCandidate(Candidate{Status: CandidatePendingReview})
	router := NewReviewRouter(store, NewReviewService(store), nil)
	request := newLocalReviewRequest(http.MethodPost, fmt.Sprintf("/semantic/review/%d/approve", candidate.ID), strings.NewReader("csrf_token=semantic-crawler-local"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("known default token authorized mutation: %d", result.Code)
	}
}

type transientExtractionModel struct {
	recordingModel
	failed bool
}

func (m *transientExtractionModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	if !m.failed {
		m.failed = true
		return ExtractionResponse{}, ModelUsage{}, fmt.Errorf("temporary extraction failure")
	}
	return m.recordingModel.Extract(ctx, prompt, blocks)
}

func TestFailedReviewRetryRemainsVisibleAndCanResume(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/a/", RawHTML: "<main><p>Source</p></main>"})
	original := store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidatePendingReview})
	processor := NewRetryProcessor(store, &transientExtractionModel{}, CrawlConfig{Scope: "source"})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"}, processor)
	request := newLocalReviewRequest(http.MethodPost, fmt.Sprintf("/semantic/review/%d/retry", original.ID), strings.NewReader("csrf_token=secret&reviewer=test&scope=url&instruction=Keep+source"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	if result.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected temporary retry failure: %d", result.Code)
	}
	queue := httptest.NewRecorder()
	router.ServeHTTP(queue, newLocalReviewRequest(http.MethodGet, "/semantic/review", nil))
	if !strings.Contains(queue.Body.String(), "queued") {
		t.Fatal("failed retry disappeared from review queue")
	}
	var queued Candidate
	for _, c := range store.Candidates() {
		if c.Status == CandidateQueued {
			queued = c
		}
	}
	if queued.ID == 0 {
		t.Fatal("queued retry missing")
	}
	resume := newLocalReviewRequest(http.MethodPost, fmt.Sprintf("/semantic/review/%d/resume", queued.ID), strings.NewReader("csrf_token=secret"))
	resume.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resumed := httptest.NewRecorder()
	router.ServeHTTP(resumed, resume)
	recovered, _ := store.Candidate(context.Background(), queued.ID)
	rules, _ := store.Rules(context.Background(), 1, page.URL)
	if resumed.Code != http.StatusSeeOther || recovered.Status != CandidatePendingReview || len(recovered.Sections) == 0 || len(rules) != 1 {
		t.Fatalf("retry did not recover without duplicating instruction: status=%d candidate=%#v rules=%d", resumed.Code, recovered, len(rules))
	}
}
