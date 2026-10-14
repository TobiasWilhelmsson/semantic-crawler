package semantic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchmarkExtractionFailurePreservesZeroGoldStatus(t *testing.T) {
	dir := t.TempDir()
	fixtures := filepath.Join(dir, "fixtures")
	if err := os.Mkdir(fixtures, 0700); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"missing","role":"header","container":"#missing","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "mapping.json"), []byte(mapping), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "a.html"), []byte("<main><h1>A</h1></main>"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureDirectory: fixtures, MappingPath: filepath.Join(dir, "mapping.json"), ReportDirectory: filepath.Join(dir, "reports"), Model: &timeoutExtractModel{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("stale_selector_pages=%d page_status=%q simulated_exact=%v simulated_f1=%v", report.StaleSelectorPages, report.Pages[0].BaselineStatus, report.SimulatedBaselineAggregate.ExactParity, report.SimulatedBaselineAggregate.RelevantTextF1)
	if report.Pages[0].BaselineStatus != BaselineZeroGold || report.SimulatedBaselineAggregate.ExactParity {
		t.Fatal("failed extraction with zero gold contributes a perfect simulated baseline")
	}
}

type feedbackFailureModel struct{ FixtureModel }

func (m *feedbackFailureModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	if strings.Contains(prompt, "URL: https://example.test/topic/b/") {
		if strings.Contains(prompt, "Benchmark feedback:") {
			return ExtractionResponse{}, ModelUsage{}, errors.New("provider down")
		}
		response, usage, err := m.FixtureModel.Extract(ctx, prompt, blocks)
		for i := range response.Blocks {
			response.Blocks[i].Include = false
		}
		return response, usage, err
	}
	return m.FixtureModel.Extract(ctx, prompt, blocks)
}
func TestBenchmarkAfterFeedbackAggregateIncludesFailedPages(t *testing.T) {
	dir := t.TempDir()
	fixtures := filepath.Join(dir, "fixtures")
	if err := os.Mkdir(fixtures, 0700); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "mapping.json"), []byte(mapping), 0600); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(fixtures, slug+".html"), []byte("<main><h1>"+slug+"</h1></main>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureDirectory: fixtures, MappingPath: filepath.Join(dir, "mapping.json"), ReportDirectory: filepath.Join(dir, "reports"), Model: &feedbackFailureModel{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first_f1=%v after_f1=%v first_gold_sections=%d after_gold_sections=%d", report.Aggregate.RelevantTextF1, report.AfterFeedbackAggregate.RelevantTextF1, report.Aggregate.GoldSections, report.AfterFeedbackAggregate.GoldSections)
	if report.AfterFeedbackAggregate.GoldSections != 2 || report.AfterFeedbackAggregate.RelevantTextF1 != 0.5 || report.AfterFeedbackAggregate.ExactParity {
		t.Fatal("failed feedback run vanished and made correction appear perfect")
	}
}

type firstPassFailureModel struct {
	FixtureModel
	failRoute bool
}

func (model *firstPassFailureModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	if model.failRoute && strings.Contains(prompt, "URL: https://example.test/topic/b/") {
		return RouteResponse{}, ModelUsage{}, context.DeadlineExceeded
	}
	return model.FixtureModel.Route(ctx, prompt)
}

func (model *firstPassFailureModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	if strings.Contains(prompt, "URL: https://example.test/topic/b/") {
		return ExtractionResponse{}, ModelUsage{}, context.DeadlineExceeded
	}
	return model.FixtureModel.Extract(ctx, prompt, blocks)
}

func TestBenchmarkAfterFeedbackAggregateIncludesFirstPassFailures(t *testing.T) {
	for _, failRoute := range []bool{true, false} {
		name := "extraction"
		if failRoute {
			name = "route"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fixtures := filepath.Join(dir, "fixtures")
			if err := os.Mkdir(fixtures, 0o700); err != nil {
				t.Fatal(err)
			}
			mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
			if err := os.WriteFile(filepath.Join(dir, "mapping.json"), []byte(mapping), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, slug := range []string{"a", "b"} {
				if err := os.WriteFile(filepath.Join(fixtures, slug+".html"), []byte("<main><h1>"+slug+"</h1></main>"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureDirectory: fixtures, MappingPath: filepath.Join(dir, "mapping.json"), ReportDirectory: filepath.Join(dir, "reports"), Model: &firstPassFailureModel{failRoute: failRoute}})
			if err != nil {
				t.Fatal(err)
			}
			if report.AfterFeedbackAggregate.GoldSections != 2 || report.AfterFeedbackAggregate.RelevantTextF1 != 0.5 || report.AfterFeedbackAggregate.ExactParity {
				t.Fatalf("first-pass failure vanished from feedback denominator: %#v", report.AfterFeedbackAggregate)
			}
		})
	}
}
