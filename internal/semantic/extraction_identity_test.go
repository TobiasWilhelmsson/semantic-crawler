package semantic

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractionIdentityReprocessesChangedPolicy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	model := &recordingModel{}
	u := "https://example.test/page"
	pipeline := NewPipeline(mapFetcher{u: `<main><p>Fee 25 SEK except students.</p></main>`}, model, store)
	config := CrawlConfig{SourceID: 1, SeedURL: u, AllowedHost: "example.test", Scope: "fees", MaxPages: 1, Model: "one", PromptVersion: "one"}
	if _, err := pipeline.Run(ctx, config); err != nil {
		t.Fatal(err)
	}
	same, err := pipeline.Run(ctx, config)
	if err != nil || same.Unchanged != 1 {
		t.Fatalf("same config: %#v %v", same, err)
	}
	for _, change := range []func(){func() { config.Scope = "fees and exceptions" }, func() { config.SourceSkill.Content = "Keep dates" }, func() { config.Model = "two" }, func() { config.PromptVersion = "two" }} {
		change()
		before := len(model.extractCalls)
		summary, err := pipeline.Run(ctx, config)
		if err != nil || summary.Unchanged != 0 || len(model.extractCalls) != before+1 {
			t.Errorf("changed policy incorrectly reused: %#v %v", summary, err)
		}
	}
}
func TestExtractionIdentityIncludesRulesAndPreservesApprovals(t *testing.T) {
	ctx := context.Background()
	store, err := OpenReviewStore(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := &recordingModel{}
	u := "https://example.test/page"
	pipeline := NewPipeline(mapFetcher{u: `<main><p>Keep exception.</p></main>`}, model, store)
	config := CrawlConfig{SourceID: 1, SeedURL: u, AllowedHost: "example.test", Scope: "all", MaxPages: 1}
	pipeline.Run(ctx, config)
	c, err := store.Candidate(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c.Status = CandidateApproved
	c.ApprovedAt = &now
	store.SaveCandidate(ctx, c)
	store.AddRule(ctx, SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "Keep deadlines", Status: "active", Version: 2})
	result, err := pipeline.Run(ctx, config)
	if err != nil || result.Unchanged != 0 || result.PendingReview != 1 {
		t.Fatalf("new rule ignored: %#v %v", result, err)
	}
	c, _ = store.Candidate(ctx, 1)
	if c.Status != CandidateApproved {
		t.Fatal("approval changed")
	}
}

func TestExtractionIdentityLegacyAndForcedReextract(t *testing.T) {
	ctx := context.Background()
	u := "https://example.test/page"
	body := `<main><p>Keep deadline.</p></main>`
	model := &recordingModel{}
	store := NewMemoryStore()
	blocks, _, _ := BuildCandidateBlocks(strings.NewReader(body), u)
	store.PutPageVersion(PageVersion{SourceID: 1, URL: u, ContentHash: ContentHash(blocks), Processed: true})
	config := CrawlConfig{SourceID: 1, SeedURL: u, AllowedHost: "example.test", Scope: "all", MaxPages: 1}
	pipeline := NewPipeline(mapFetcher{u: body}, model, store)
	result, err := pipeline.Run(ctx, config)
	if err != nil || result.PendingReview != 1 {
		t.Fatalf("legacy identity was trusted: %#v %v", result, err)
	}
	config.ForceReextract = true
	result, err = pipeline.Run(ctx, config)
	if err != nil || result.PendingReview != 1 {
		t.Fatalf("forced extraction skipped: %#v %v", result, err)
	}
}
func TestExtractionIdentityExcludesCredentialsAndOperationalLimits(t *testing.T) {
	c := CrawlConfig{Scope: "content"}
	m := NewOpenAIClient(OpenAIConfig{Provider: "custom", BaseURL: "https://api.example.test", Model: "model", APIKey: "one", MaxTokens: 100})
	a := extractionIdentity(c, nil, m)
	m.config.APIKey = "two"
	m.config.Timeout = time.Minute
	c.MaxPages = 40
	c.ForceReextract = true
	if a != extractionIdentity(c, nil, m) {
		t.Fatal("credentials/operational settings invalidate extraction")
	}
	m.config.MaxTokens = 200
	if a == extractionIdentity(c, nil, m) {
		t.Fatal("token budget ignored")
	}
}
