package semantic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingSecondExtractModel struct {
	FixtureModel
	extracts int
}

func (model *failingSecondExtractModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extracts++
	if model.extracts > 2 {
		return ExtractionResponse{}, ModelUsage{}, context.Canceled
	}
	return model.FixtureModel.Extract(ctx, prompt, blocks)
}

type usageModel struct {
	FixtureModel
}

func (model *usageModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	response, _, err := model.FixtureModel.Route(ctx, prompt)
	return response, ModelUsage{InputTokens: 11, OutputTokens: 3, TotalTokens: 14, Latency: 2 * time.Millisecond}, err
}

func (model *usageModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	response, _, err := model.FixtureModel.Extract(ctx, prompt, blocks)
	return response, ModelUsage{InputTokens: 17, OutputTokens: 5, TotalTokens: 22, Latency: 3 * time.Millisecond}, err
}

type timeoutExtractModel struct {
	calls int
}

func (model *timeoutExtractModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1}, ModelUsage{}, nil
}

func (model *timeoutExtractModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.calls++
	return ExtractionResponse{}, ModelUsage{}, context.DeadlineExceeded
}

type validationThenSuccessModel struct {
	calls int
}

func (model *validationThenSuccessModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1}, ModelUsage{}, nil
}

func (model *validationThenSuccessModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.calls++
	if model.calls == 1 {
		return ExtractionResponse{}, ModelUsage{}, fmt.Errorf("validate extraction response: unknown block_id")
	}
	response := ExtractionResponse{PageKind: "article", Blocks: []BlockDecision{{BlockID: blocks[0].ID, Include: true, Role: RoleHeader, Type: TypeNone, ParseMode: true, Reason: "retry success"}}}
	return response, ModelUsage{}, nil
}

type emptyStructuredThenSuccessModel struct {
	calls int
}

func (model *emptyStructuredThenSuccessModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1}, ModelUsage{}, nil
}

func (model *emptyStructuredThenSuccessModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.calls++
	if model.calls == 1 {
		return ExtractionResponse{}, ModelUsage{InputTokens: 10, OutputTokens: 0, TotalTokens: 10, Latency: time.Millisecond, RawResponse: `{"choices":[{"message":{"content":null}}]}`}, fmt.Errorf("model response contained no structured content")
	}
	response := ExtractionResponse{PageKind: "article", Blocks: []BlockDecision{{BlockID: blocks[0].ID, Include: true, Role: RoleHeader, Type: TypeNone, ParseMode: true, Reason: "retry success"}}}
	return response, ModelUsage{InputTokens: 12, OutputTokens: 4, TotalTokens: 16, Latency: time.Millisecond}, nil
}

type decodeStructuredTwiceThenSuccessModel struct {
	calls int
}

func (model *decodeStructuredTwiceThenSuccessModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1}, ModelUsage{}, nil
}

func (model *decodeStructuredTwiceThenSuccessModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.calls++
	if model.calls <= 2 {
		return ExtractionResponse{}, ModelUsage{InputTokens: 8, OutputTokens: 8, TotalTokens: 16, Latency: time.Millisecond}, fmt.Errorf("decode structured model content: unexpected end of JSON input")
	}
	response := ExtractionResponse{PageKind: "article", Blocks: []BlockDecision{{BlockID: blocks[0].ID, Include: true, Role: RoleHeader, Type: TypeNone, ParseMode: true, Reason: "ok"}}}
	return response, ModelUsage{InputTokens: 9, OutputTokens: 3, TotalTokens: 12, Latency: time.Millisecond}, nil
}

func TestExtractWithValidationRetryDoesNotRetryContextOrTransportErrors(t *testing.T) {
	model := &timeoutExtractModel{}
	_, _, err := extractWithValidationRetry(context.Background(), model, "prompt", []CandidateBlock{{ID: "b0001"}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if model.calls != 1 {
		t.Fatalf("timeout/transport errors should not be retried as schema failures, calls=%d", model.calls)
	}
}

func TestExtractWithValidationRetryRetriesValidationErrors(t *testing.T) {
	model := &validationThenSuccessModel{}
	_, _, err := extractWithValidationRetry(context.Background(), model, "prompt", []CandidateBlock{{ID: "b0001"}})
	if err != nil {
		t.Fatalf("validation retry should succeed: %v", err)
	}
	if model.calls != 2 {
		t.Fatalf("validation-style errors should be retried once, calls=%d", model.calls)
	}
}

func TestExtractWithValidationRetryRetriesEmptyStructuredContent(t *testing.T) {
	model := &emptyStructuredThenSuccessModel{}
	_, usage, err := extractWithValidationRetry(context.Background(), model, "prompt", []CandidateBlock{{ID: "b0001"}})
	if err != nil {
		t.Fatalf("empty structured content retry should succeed: %v", err)
	}
	if model.calls != 2 {
		t.Fatalf("empty structured content should be retried once, calls=%d", model.calls)
	}
	if usage.InputTokens != 22 || usage.OutputTokens != 4 || usage.TotalTokens != 26 {
		t.Fatalf("retry usage should be accumulated, got %#v", usage)
	}
}

func TestExtractWithValidationRetryRetriesDecodeStructuredContentTwice(t *testing.T) {
	model := &decodeStructuredTwiceThenSuccessModel{}
	_, usage, err := extractWithValidationRetry(context.Background(), model, "prompt", []CandidateBlock{{ID: "b0001"}})
	if err != nil {
		t.Fatalf("decode structured content should get an extra compact retry: %v", err)
	}
	if model.calls != 3 {
		t.Fatalf("decode structured content should be attempted three times total, calls=%d", model.calls)
	}
	if usage.InputTokens != 25 || usage.OutputTokens != 19 || usage.TotalTokens != 44 {
		t.Fatalf("retry usage should be accumulated across all attempts, got %#v", usage)
	}
}

func TestBenchmarkSectionsUsesMediaMetadataWhenHTMLTextIsEmpty(t *testing.T) {
	sections := []Section{{
		BlockID:   "media",
		Role:      RoleMedia,
		Type:      TypeNone,
		ParseMode: false,
		Content:   `<figure><img src="/globalassets/haklyft.jpg" alt="Bild som visar haklyft"></figure>`,
	}}

	benchmarked := benchmarkSections(sections)
	if len(benchmarked) != 1 {
		t.Fatalf("expected one benchmark section, got %#v", benchmarked)
	}
	if !strings.Contains(benchmarked[0].Text, "Bild som visar haklyft") || strings.Contains(benchmarked[0].Text, "globalassets") {
		t.Fatalf("media benchmark section should preserve image metadata fallback: %#v", benchmarked[0])
	}
}

func TestBenchmarkRunnerProducesReportFromGoldMappingsAndSnapshots(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><head><title>A</title></head><body><main><h1>A</h1><aside>Critical warning</aside></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.ExpectedURLs != 1 || report.DiscoveredURLs != 1 || len(report.Pages) != 1 || report.SchemaValidRate != 1 || report.FeedbackInstruction == "" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.AfterFeedbackAggregate.GoldSections == 0 {
		t.Fatalf("after-feedback aggregate missing: %#v", report.AfterFeedbackAggregate)
	}
	if !report.SimulatedBaselineAggregate.ExactParity || report.SimulatedBaselineAggregate.RelevantTextF1 != 1 {
		t.Fatalf("simulated baseline correction should show parity: %#v", report.SimulatedBaselineAggregate)
	}
	if report.ModelMode != "fixture" || report.TokenUsageAvailable || report.UsageNote == "" {
		t.Fatalf("fixture usage should be labeled as synthetic/no-token: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(directory, "reports", "benchmark.html")); err != nil {
		t.Fatalf("HTML report missing: %v", err)
	}
}

func TestBenchmarkRunnerCountsModelUsageWhenModelReturnsIt(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &usageModel{}, ModelMode: "openai"})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.ModelInputTokens != 45 || report.ModelOutputTokens != 13 || !report.TokenUsageAvailable || report.RawUsageMissing {
		t.Fatalf("model usage was not counted correctly: %#v", report)
	}
}

func TestBenchmarkRunnerMarksZeroGoldPagesAsStaleBaseline(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"missing","role":"header","container":"#missing","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.StaleSelectorPages != 1 || report.UsableGoldPages != 0 || report.Pages[0].BaselineStatus != BaselineZeroGold {
		t.Fatalf("zero-gold page was not marked as stale baseline: %#v", report)
	}
	if report.Pages[0].Metrics.ExactParity {
		t.Fatalf("zero-gold page must not count as exact parity: %#v", report.Pages[0])
	}
}

func TestBenchmarkRunnerAppliesAuditedGoldOverridesOnlyForMatchingURL(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"missing","role":"header","container":"#missing","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"missing","role":"header","container":"#missing","parse_mode":true}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.html", "b.html"} {
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name), []byte(`<html><body><main><h1>Current audited title</h1></main></body></html>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	overridePath := filepath.Join(directory, "overrides.json")
	overrides := `{"overrides":[{"url":"https://example.test/topic/a/","identifier_name":"missing","original_selector":"#missing","reason":"selector stale","audited_text":"Current audited title","role":"header","type":"none","parse_mode":true,"auditor_note":"matched current h1","audited_at":"2026-06-18T00:00:00Z"}]}`
	if err := os.WriteFile(overridePath, []byte(overrides), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}, GoldOverridePath: overridePath})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.RepairedGoldPages != 1 || report.Pages[0].BaselineStatus != BaselineAuditedRepair || report.Pages[1].BaselineStatus != BaselineZeroGold {
		t.Fatalf("gold override was not scoped to matching URL: %#v", report.Pages)
	}
	if report.Pages[0].Expected[0].Text != "Current audited title" {
		t.Fatalf("audited text was not used: %#v", report.Pages[0].Expected)
	}
}

func TestBenchmarkRunnerCanLimitPagesForModelSmokeRuns(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.html", "b.html"} {
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name), []byte(`<html><body><main><h1>Title</h1></main></body></html>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}, MaxPages: 1})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.ExpectedURLs != 1 || len(report.Pages) != 1 {
		t.Fatalf("page limit was not applied: %#v", report)
	}
}

func TestBenchmarkRunnerCanFetchMissingFixturePages(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := mapFetcher{"https://example.test/topic/b/": `<html><body><main><h1>B</h1></main></body></html>`}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}, Fetcher: fetcher})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.ExpectedURLs != 2 || report.DiscoveredURLs != 2 || len(report.Pages) != 2 || len(report.MissingURLs) != 0 {
		t.Fatalf("missing fixture should be fetched and benchmarked: %#v", report)
	}
}

func TestBenchmarkRunnerCanPreferLiveFetchOverCollidingFixtureSlug(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/other-topic/oversikt/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "oversikt.html"), []byte(`<html><body><main><h1>Wrong fixture</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := mapFetcher{"https://example.test/other-topic/oversikt/": `<html><body><main><h1>Live correct page</h1></main></body></html>`}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/other-topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}, Fetcher: fetcher, LiveFetch: true})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if len(report.Pages) != 1 || report.Pages[0].Expected[0].Text != "Live correct page" {
		t.Fatalf("live fetch should win over colliding fixture slug: %#v", report.Pages)
	}
}

func TestBenchmarkRunnerCanStartAtPageOffset(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.html", "b.html"} {
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name), []byte(`<html><body><main><h1>Title</h1></main></body></html>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}, StartPage: 2, MaxPages: 1})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.ExpectedURLs != 1 || report.Pages[0].URL != "https://example.test/topic/b/" {
		t.Fatalf("start offset was not applied: %#v", report)
	}
}

func TestBenchmarkRunnerWritesIncrementalReport(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.html", "b.html"} {
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name), []byte(`<html><body><main><h1>Title</h1></main></body></html>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reportDirectory := filepath.Join(directory, "reports")

	_, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", FixtureDirectory: fixtureDirectory, MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: reportDirectory, Model: &failingSecondExtractModel{}})
	if err != nil {
		t.Fatalf("benchmark should record extraction failures instead of aborting: %v", err)
	}
	reportBytes, err := os.ReadFile(filepath.Join(reportDirectory, "benchmark.json"))
	if err != nil {
		t.Fatalf("incremental report missing: %v", err)
	}
	if len(reportBytes) == 0 {
		t.Fatal("incremental report is empty")
	}
}
