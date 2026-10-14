package semantic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPromptDiagnosticsReportsPromptAndBlockSize(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1><p>Clinical paragraph A.</p><aside>Important warning.</aside></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunPromptDiagnostics(context.Background(), PromptDiagnosticsOptions{
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		Scope:            "clinical scope",
		SourceSkill:      SourceSkill{Content: "keep clinical content"},
	})
	if err != nil {
		t.Fatalf("run prompt diagnostics: %v", err)
	}
	if report.ExpectedURLs != 1 || len(report.Pages) != 1 {
		t.Fatalf("unexpected report shape: %#v", report)
	}
	page := report.Pages[0]
	if page.BlockCount != 3 || page.PromptChars == 0 || page.SourceBlockChars == 0 || page.TotalBlockTextChars == 0 {
		t.Fatalf("diagnostics should expose prompt and block sizes: %#v", page)
	}
	if page.TagCounts["h1"] != 1 || page.TagCounts["p"] != 1 || page.TagCounts["aside"] != 1 {
		t.Fatalf("diagnostics should count block tags: %#v", page.TagCounts)
	}
	if report.TotalPromptChars != page.PromptChars || report.MaxPromptChars != page.PromptChars {
		t.Fatalf("aggregate prompt diagnostics not recorded: %#v", report)
	}
}

func TestRunPromptDiagnosticsWritesReportWhenDirectoryProvided(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	reportDirectory := filepath.Join(directory, "reports")
	_, err := RunPromptDiagnostics(context.Background(), PromptDiagnosticsOptions{
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		Scope:            "clinical scope",
		SourceSkill:      SourceSkill{Content: "keep clinical content"},
		ReportDirectory:  reportDirectory,
	})
	if err != nil {
		t.Fatalf("run prompt diagnostics: %v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(reportDirectory, "prompt-diagnostics.json"))
	if readErr != nil {
		t.Fatalf("prompt diagnostics report was not written: %v", readErr)
	}
	if !strings.Contains(string(content), `"expected_urls": 1`) || !strings.Contains(string(content), `"block_count": 1`) {
		t.Fatalf("written prompt diagnostics report missing expected fields: %s", content)
	}
}
