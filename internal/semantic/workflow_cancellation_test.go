package semantic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type workflowCancellationResult struct {
	pages       int
	fetchFailed int
	err         error
}

func runCancellableWorkflow(ctx context.Context, action, mapping, directory string, fetcher Fetcher, model SemanticModel) workflowCancellationResult {
	switch action {
	case "benchmark":
		report, err := RunBenchmark(ctx, BenchmarkOptions{MappingPath: mapping, ReportDirectory: directory, Model: model, LiveFetch: true, Fetcher: fetcher})
		return workflowCancellationResult{pages: len(report.Pages), fetchFailed: len(report.MissingURLs), err: err}
	case "selector-health":
		report, err := RunSelectorHealth(ctx, SelectorHealthOptions{MappingPath: mapping, ReportDirectory: directory, Fetcher: fetcher})
		return workflowCancellationResult{pages: len(report.Pages), fetchFailed: report.FetchFailedPages, err: err}
	default:
		report, err := RunPromptDiagnostics(ctx, PromptDiagnosticsOptions{MappingPath: mapping, ReportDirectory: directory, LiveFetch: true, Fetcher: fetcher})
		failed := 0
		for _, page := range report.Pages {
			if page.FetchError != "" {
				failed++
			}
		}
		return workflowCancellationResult{pages: len(report.Pages), fetchFailed: failed, err: err}
	}
}

func workflowCancellationFixture(t *testing.T) (string, mapFetcher) {
	t.Helper()
	mapping := filepath.Join(t.TempDir(), "mapping.json")
	data := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/c/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(mapping, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return mapping, mapFetcher{
		"https://example.test/topic/a/": `<main><h1>A</h1></main>`,
		"https://example.test/topic/b/": `<main><h1>B</h1></main>`,
		"https://example.test/topic/c/": `<main><h1>C</h1></main>`,
	}
}

func TestReportWorkflowsStopCanceledFetchWithoutReplacingCompleteReport(t *testing.T) {
	for _, action := range []string{"benchmark", "selector-health", "prompt-diagnostics"} {
		for _, test := range []struct {
			name         string
			cancelAt     int
			returnResult bool
		}{
			{"first fetch", 1, false},
			{"final fetch", 3, false},
			{"final fetch returns content", 3, true},
		} {
			t.Run(action+"/"+test.name, func(t *testing.T) {
				mapping, fixtures := workflowCancellationFixture(t)
				directory := t.TempDir()
				completed := runCancellableWorkflow(context.Background(), action, mapping, directory, fixtures, &FixtureModel{})
				if completed.err != nil || completed.pages != 3 {
					t.Fatalf("create completed report: %+v", completed)
				}
				path := filepath.Join(directory, action+".json")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				fetcher := crawlRecoveryFetcher(func(ctx context.Context, url string) (FetchResult, error) {
					calls++
					if calls == test.cancelAt {
						cancel()
						if !test.returnResult {
							return FetchResult{}, ctx.Err()
						}
					}
					return fixtures.Fetch(ctx, url)
				})
				result := runCancellableWorkflow(ctx, action, mapping, directory, fetcher, &FixtureModel{})
				if !errors.Is(result.err, context.Canceled) || calls != test.cancelAt {
					t.Errorf("cancellation was swallowed or requested later pages: result=%+v fetches=%d", result, calls)
				}
				if result.fetchFailed != 0 || result.pages != test.cancelAt-1 {
					t.Errorf("cancellation became source evidence: %+v", result)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Errorf("canceled run replaced completed report: err=%v", err)
				}
			})
		}
	}
}

func TestReportWorkflowsHonorCancellationAtEntry(t *testing.T) {
	for _, action := range []string{"benchmark", "selector-health", "prompt-diagnostics"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result := runCancellableWorkflow(ctx, action, "", "", nil, nil)
			if !errors.Is(result.err, context.Canceled) {
				t.Fatalf("canceled workflow continued into setup: %+v", result)
			}
		})
	}
}

type workflowCancelModel struct {
	FixtureModel
	cancel context.CancelFunc
	stage  string
	url    string
}

func (model *workflowCancelModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	if model.stage == "route" && strings.Contains(prompt, "URL: "+model.url+"\n") {
		model.cancel()
	}
	return model.FixtureModel.Route(ctx, prompt)
}

func (model *workflowCancelModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	feedback := strings.Contains(prompt, "Benchmark feedback:")
	if strings.Contains(prompt, "URL: "+model.url+"\n") && ((model.stage == "extract" && !feedback) || (model.stage == "feedback" && feedback)) {
		model.cancel()
	}
	return model.FixtureModel.Extract(ctx, prompt, blocks)
}

func TestBenchmarkStopsCanceledModelWithoutReplacingCompleteReport(t *testing.T) {
	for _, stage := range []string{"route", "extract", "feedback"} {
		for _, target := range []string{"https://example.test/topic/a/", "https://example.test/topic/c/"} {
			t.Run(stage+"/"+target, func(t *testing.T) {
				mapping, fixtures := workflowCancellationFixture(t)
				directory := t.TempDir()
				if result := runCancellableWorkflow(context.Background(), "benchmark", mapping, directory, fixtures, &FixtureModel{}); result.err != nil {
					t.Fatal(result.err)
				}
				path := filepath.Join(directory, "benchmark.json")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				model := &workflowCancelModel{cancel: cancel, stage: stage, url: target}
				lastURL := ""
				fetcher := crawlRecoveryFetcher(func(ctx context.Context, url string) (FetchResult, error) {
					lastURL = url
					return fixtures.Fetch(ctx, url)
				})
				result := runCancellableWorkflow(ctx, "benchmark", mapping, directory, fetcher, model)
				if !errors.Is(result.err, context.Canceled) || lastURL != target {
					t.Errorf("model cancellation was swallowed or traversal continued: result=%+v lastURL=%s", result, lastURL)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(before) != string(after) {
					t.Errorf("canceled model run replaced complete benchmark: err=%v", err)
				}
			})
		}
	}
}

type workflowCancelValidationModel struct {
	FixtureModel
	cancel context.CancelFunc
	calls  int
}

func (model *workflowCancelValidationModel) Extract(context.Context, string, []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.calls++
	model.cancel()
	return ExtractionResponse{}, ModelUsage{}, errors.New("decode structured model content: incomplete")
}

func TestExtractionValidationRetryStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &workflowCancelValidationModel{cancel: cancel}
	_, _, err := extractWithValidationRetry(ctx, model, "prompt", nil)
	if !errors.Is(err, context.Canceled) || model.calls != 1 {
		t.Fatalf("canceled validation response started more model requests: err=%v calls=%d", err, model.calls)
	}
}

func TestEvaluationAndParityCancellationPreserveReports(t *testing.T) {
	for _, action := range []string{"evaluation", "parity"} {
		for _, stage := range []string{"entry", "route", "extract"} {
			if action == "parity" && stage == "route" {
				continue
			}
			t.Run(action+"/"+stage, func(t *testing.T) {
				mapping, fixtures := workflowCancellationFixture(t)
				fixtureDir := t.TempDir()
				for url, html := range fixtures {
					slug := strings.TrimSuffix(url, "/")
					slug = slug[strings.LastIndex(slug, "/")+1:]
					if err := os.WriteFile(filepath.Join(fixtureDir, slug+".html"), []byte(html), 0600); err != nil {
						t.Fatal(err)
					}
				}
				gold := filepath.Join(fixtureDir, "gold.json")
				if err := os.WriteFile(gold, []byte(`{"pages":[{"url":"https://example.test/topic/a/","slug":"a","route":"extract"},{"url":"https://example.test/topic/c/","slug":"c","route":"extract"}]}`), 0600); err != nil {
					t.Fatal(err)
				}
				directory := t.TempDir()
				filename := "evaluation.json"
				if action == "parity" {
					filename = "human-parity-proof.json"
				}
				reportPath := filepath.Join(directory, filename)
				before := []byte(`{"previous":"complete evidence"}`)
				if err := os.WriteFile(reportPath, before, 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				model := &workflowCancelModel{cancel: cancel, stage: stage, url: "https://example.test/topic/c/"}
				if stage == "entry" {
					cancel()
				}
				var err error
				if action == "evaluation" {
					_, err = RunEvaluation(ctx, EvaluationOptions{FixtureDirectory: fixtureDir, GoldPath: gold, ReportDirectory: directory, Model: model})
				} else {
					_, err = RunHumanParityProof(ctx, HumanParityOptions{FixtureDirectory: fixtureDir, MappingPath: mapping, ReportDirectory: directory, Model: model})
				}
				if !errors.Is(err, context.Canceled) {
					t.Errorf("cancellation must propagate, got %v", err)
				}
				after, readErr := os.ReadFile(reportPath)
				if readErr != nil || string(after) != string(before) {
					t.Errorf("cancellation replaced completed evidence: %v", readErr)
				}
			})
		}
	}
}
