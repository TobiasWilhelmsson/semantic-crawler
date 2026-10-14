package semantic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHumanParityProofReachesExactHumanGoldWithReviewerPrompt(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping := `{
		"ignore": [{"pattern": "\\[\\d+\\]"}],
		"urls": [{
			"url": "https://example.test/topic/a/",
			"identifier": [{"name": "title", "role": "header", "container": "h1", "parse_mode": true}]
		}]
	}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1>Human title [1]</h1><p>Extra semantic content.</p></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunHumanParityProof(context.Background(), HumanParityOptions{
		FixtureSet:       "test",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Scope:            "clinical",
		Model:            &FixtureModel{},
	})
	if err != nil {
		t.Fatalf("run proof: %v", err)
	}
	if !report.Passed || report.FinalExactParityRate != 1 || !report.FinalAggregate.ExactParity {
		t.Fatalf("expected strict final parity proof to pass: %#v", report)
	}
	if len(report.ReviewerPrompts) != 1 || !strings.Contains(report.ReviewerPrompts[0].Instruction, "exactly match the human-approved selector baseline") {
		t.Fatalf("expected reviewer prompt transcript: %#v", report.ReviewerPrompts)
	}
	if report.Pages[0].InitialMetrics.ExactParity {
		t.Fatalf("test should prove review repaired a real mismatch: %#v", report.Pages[0])
	}
	for _, name := range []string{"human-parity-proof.json", "human-parity-proof.html", "reviewer-prompts.md", "human-gold.json"} {
		if _, err := os.Stat(filepath.Join(directory, "reports", name)); err != nil {
			t.Fatalf("expected report artifact %s: %v", name, err)
		}
	}
}

func TestHumanParityProofFailsWhenMappedSnapshotIsMissing(t *testing.T) {
	directory := t.TempDir()
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunHumanParityProof(context.Background(), HumanParityOptions{
		FixtureSet:       "test",
		FixtureDirectory: filepath.Join(directory, "fixtures"),
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Scope:            "clinical",
		Model:            &FixtureModel{},
	})
	if err != nil {
		t.Fatalf("run proof: %v", err)
	}
	if report.Passed || len(report.MissingURLs) != 1 || report.FinalExactParityRate != 0 {
		t.Fatalf("missing mapped fixture should fail proof: %#v", report)
	}
}

func TestHumanParityProofFailsWhenInitialSemanticExtractionFails(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Human title</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunHumanParityProof(context.Background(), HumanParityOptions{
		FixtureSet:       "test",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Scope:            "clinical",
		Model:            failingExtractModel{},
	})
	if err != nil {
		t.Fatalf("run proof: %v", err)
	}
	if report.Passed || report.Pages[0].InitialError == "" {
		t.Fatalf("broken initial extraction should fail proof: %#v", report)
	}
}

func TestHumanParityProofFailsForSubsetRuns(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title-a","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title-b","role":"header","container":"h1","parse_mode":true}]}
	]}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunHumanParityProof(context.Background(), HumanParityOptions{
		FixtureSet:       "test",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Scope:            "clinical",
		Model:            &FixtureModel{},
		MaxPages:         1,
	})
	if err != nil {
		t.Fatalf("run proof: %v", err)
	}
	if report.Passed || !report.Partial {
		t.Fatalf("subset proof should be marked partial and fail: %#v", report)
	}
}

func TestHumanParityProofFailsWhenGoldSelectorIsMissing(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"missing","role":"header","container":"h2","parse_mode":true}]}]}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Human title</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunHumanParityProof(context.Background(), HumanParityOptions{
		FixtureSet:       "test",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Scope:            "clinical",
		Model:            &FixtureModel{},
	})
	if err != nil {
		t.Fatalf("run proof: %v", err)
	}
	if report.Passed || len(report.Failures) == 0 || !strings.Contains(report.Failures[0], "matched no elements") {
		t.Fatalf("missing gold selector should fail proof: %#v", report)
	}
}

type failingExtractModel struct{}

func (failingExtractModel) Route(context.Context, string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1}, ModelUsage{}, nil
}

func (failingExtractModel) Extract(context.Context, string, []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	return ExtractionResponse{}, ModelUsage{}, fmt.Errorf("forced extraction failure")
}
