package semantic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type mapFetcher map[string]string

func (fetcher mapFetcher) Fetch(_ context.Context, url string) (FetchResult, error) {
	html, exists := fetcher[url]
	if !exists {
		return FetchResult{}, fmt.Errorf("not found: %s", url)
	}
	return FetchResult{URL: url, StatusCode: 200, HTML: html}, nil
}

type recordingModel struct {
	routeCalls   []string
	extractCalls []string
}

func (model *recordingModel) Route(_ context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	model.routeCalls = append(model.routeCalls, prompt)
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "article"}, ModelUsage{}, nil
}

type invalidExtractionModel struct {
	recordingModel
}

func (model *invalidExtractionModel) Extract(_ context.Context, prompt string, _ []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extractCalls = append(model.extractCalls, prompt)
	return ExtractionResponse{PageKind: "article", Blocks: []BlockDecision{{BlockID: "missing", Include: true, Role: RoleContent, Type: TypeNone, ParseMode: true}}}, ModelUsage{}, nil
}

type failingTrackedStore struct {
	*MemoryStore
	finishedError string
}

func (store *failingTrackedStore) StartCrawlRun(context.Context, CrawlConfig) (int64, error) {
	return 99, nil
}

func (store *failingTrackedStore) UpsertQueueItem(context.Context, QueueItem) error {
	return nil
}

func (store *failingTrackedStore) UpdateQueueItem(context.Context, int64, string, QueueStatus, RouteAction, string) error {
	return nil
}

func (store *failingTrackedStore) FinishCrawlRun(_ context.Context, _ int64, _ CrawlSummary, runError string) error {
	store.finishedError = runError
	return nil
}

func (store *failingTrackedStore) TouchPageCheckedAt(context.Context, int64, time.Time) error {
	return nil
}

func (store *failingTrackedStore) SavePageVersion(context.Context, PageVersion) (PageVersion, error) {
	return PageVersion{}, fmt.Errorf("save failed")
}

func (model *recordingModel) Extract(_ context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extractCalls = append(model.extractCalls, prompt)
	decisions := make([]BlockDecision, 0, len(blocks))
	for _, block := range blocks {
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: true, Role: RoleContent, Type: TypeNone, ParseMode: true})
	}
	return ExtractionResponse{PageKind: "article", Blocks: decisions}, ModelUsage{}, nil
}

func TestPipelineHarvestsLinksBeforeSkippingUnchangedPage(t *testing.T) {
	seed := "https://source.example.test/topic/"
	child := "https://source.example.test/topic/child/"
	seedHTML := `<html><head><title>Topic</title></head><body><main><p>Unchanged introduction.</p><a href="child/">Child</a></main></body></html>`
	childHTML := `<html><head><title>Child</title></head><body><main><h1>Child</h1><p>Clinical guidance.</p></main></body></html>`

	store := NewMemoryStore()
	seedBlocks, _, err := BuildCandidateBlocks(strings.NewReader(seedHTML), seed)
	if err != nil {
		t.Fatal(err)
	}
	model := &recordingModel{}
	config := CrawlConfig{SourceID: 1, SeedURL: seed, AllowedHost: "source.example.test", Scope: "Clinical airway suction guidance", MaxDepth: 2, MaxPages: 10, SkillVersion: 1}
	store.PutPageVersion(PageVersion{ExtractionIdentity: extractionIdentity(config, nil, model), SourceID: 1, URL: seed, ContentHash: ContentHash(seedBlocks), Processed: true})
	pipeline := NewPipeline(mapFetcher{seed: seedHTML, child: childHTML}, model, store)

	run, err := pipeline.Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: seed, AllowedHost: "source.example.test", Scope: "Clinical airway suction guidance", MaxDepth: 2, MaxPages: 10, SkillVersion: 1})
	if err != nil {
		t.Fatalf("run pipeline: %v", err)
	}
	if run.Discovered != 2 || run.Unchanged != 1 || run.PendingReview != 1 {
		t.Fatalf("unexpected crawl result: %#v", run)
	}
	if len(model.routeCalls) != 1 || len(model.extractCalls) != 1 {
		t.Fatalf("unchanged seed should not call model; route=%d extract=%d", len(model.routeCalls), len(model.extractCalls))
	}
	candidates := store.Candidates()
	if len(candidates) != 1 || candidates[0].URL != child || candidates[0].Status != CandidatePendingReview {
		t.Fatalf("child did not reach review: %#v", candidates)
	}
	if len(candidates[0].Sections) != 2 || candidates[0].Sections[1].Content != "<p data-semantic-block=\"b0002\">Clinical guidance.</p>" {
		t.Fatalf("candidate does not contain verbatim selected source HTML: %#v", candidates[0].Sections)
	}
}

func TestPipelinePersistsModelEvidence(t *testing.T) {
	url := "https://source.example.test/topic/"
	store := NewMemoryStore()
	model := &recordingModel{}
	pipeline := NewPipeline(mapFetcher{url: `<html><head><title>Topic</title></head><body><main><h1>Topic</h1><p>Guidance.</p></main></body></html>`}, model, store)

	_, err := pipeline.Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: url, AllowedHost: "source.example.test", Scope: "clinical", MaxPages: 1, SkillVersion: 1, Mode: "snapshot", Model: "test-model", PromptVersion: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	candidate := store.Candidates()[0]
	if candidate.Model != "test-model" || candidate.PromptVersion != "p1" || len(candidate.RouteResponse) == 0 || len(candidate.BlockResponse) == 0 {
		t.Fatalf("candidate lacks reproducibility evidence: %#v", candidate)
	}
}

func TestBuildExtractionPromptIncludesReviewerPolicyHints(t *testing.T) {
	prompt := buildExtractionPrompt(
		CrawlConfig{
			Scope: "clinical",
			SourceSkill: SourceSkill{
				Content: "Keep metadata and media.",
			},
		},
		PageVersion{URL: "https://source.example.test/topic/"},
		[]CandidateBlock{
			{ID: "b0001", Tag: "h1", Text: "Clinical heading"},
			{ID: "b0002", Tag: "figure", Text: "Clinical image"},
		},
		[]SkillRule{{Version: 2, Scope: RuleScopeSource, Instruction: "drop share controls"}},
	)

	for _, expected := range []string{
		"Select relevant numbered source blocks without rewriting them.",
		"Preserve wording, qualifications, exceptions, dates, units, table headers and relationships",
		"If the source skill asks for metadata, breadcrumbs, or media, include those blocks when present.",
		"v2 source: drop share controls",
		"b0002 <figure> role_hint=media type_hint=none text=Clinical image",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
}

func TestPipelineRejectsInvalidModelExtractionBeforeSavingCandidate(t *testing.T) {
	url := "https://source.example.test/topic/"
	store := NewMemoryStore()
	model := &invalidExtractionModel{}
	pipeline := NewPipeline(mapFetcher{url: `<html><body><main><h1>Topic</h1></main></body></html>`}, model, store)

	summary, err := pipeline.Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: url, AllowedHost: "source.example.test", Scope: "clinical", MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Failed != 1 || len(store.Candidates()) != 0 {
		t.Fatalf("invalid extraction should fail without candidate: summary=%#v candidates=%#v", summary, store.Candidates())
	}
}

func TestPipelineFinalizesTrackedRunWhenReturningEarly(t *testing.T) {
	url := "https://source.example.test/topic/"
	store := &failingTrackedStore{MemoryStore: NewMemoryStore()}
	pipeline := NewPipeline(mapFetcher{url: `<html><body><main><h1>Topic</h1></main></body></html>`}, &recordingModel{}, store)

	if _, err := pipeline.Run(context.Background(), CrawlConfig{SourceID: 1, SeedURL: url, AllowedHost: "source.example.test", Scope: "clinical", MaxPages: 1}); err == nil {
		t.Fatal("expected save failure")
	}
	if !strings.Contains(store.finishedError, "save failed") {
		t.Fatalf("tracked run was not finalized with error: %q", store.finishedError)
	}
}
