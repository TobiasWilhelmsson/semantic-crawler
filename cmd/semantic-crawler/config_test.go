package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCLIProofUsesPortableBaselineAndURLFilter(t *testing.T) {
	command := exec.Command("go", "run", "./cmd/semantic-crawler",
		"-action", "prove-human-parity", "-benchmark-url-contains", "/preparation/", "-report-dir", t.TempDir())
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("portable proof failed: %v\n%s", err, output)
	}
	var report struct {
		Expected int  `json:"expected_urls"`
		Compared int  `json:"compared_urls"`
		Passed   bool `json:"passed"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	if report.Expected != 1 || report.Compared != 1 || !report.Passed {
		t.Fatalf("proof did not honor the caller's baseline filter: %#v", report)
	}
}

func TestLiveModelRequiresExplicitEndpointAndModel(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_PROVIDER", "custom")
	cases := []struct{ name, endpoint, model, wantError string }{
		{"missing endpoint", "", "test-model", "SEMANTIC_LLM_BASE_URL"},
		{"missing model", "http://127.0.0.1:1234/v1", "", "SEMANTIC_LLM_MODEL"},
		{"relative endpoint", "relative/v1", "test-model", "SEMANTIC_LLM_BASE_URL"},
		{"unsupported scheme", "ftp://example.test/v1", "test-model", "SEMANTIC_LLM_BASE_URL"},
		{"embedded credentials", "https://user:secret@example.test/v1", "test-model", "SEMANTIC_LLM_BASE_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SEMANTIC_LLM_API_KEY", "test-key")
			t.Setenv("SEMANTIC_LLM_BASE_URL", tc.endpoint)
			t.Setenv("SEMANTIC_LLM_MODEL", tc.model)
			_, err := openAIConfigFromEnv(time.Second)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected configuration error for %s, got %v", tc.wantError, err)
			}
		})
	}
}

func TestLiveModelUsesConfiguredOpenAICompatibleEndpoint(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_PROVIDER", "custom")
	t.Setenv("SEMANTIC_LLM_API_KEY", "test-key")
	t.Setenv("SEMANTIC_LLM_BASE_URL", "http://127.0.0.1:1234/v1")
	t.Setenv("SEMANTIC_LLM_MODEL", "local-model")
	config, err := openAIConfigFromEnv(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if config.BaseURL != "http://127.0.0.1:1234/v1" || config.Model != "local-model" {
		t.Fatalf("configured endpoint/model were not retained")
	}
	if _, err := semanticModel("openai"); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultSourceSelectionWorksWithBundledSyntheticExample(t *testing.T) {
	selection, err := resolveSourceSelection("", "", "", "../../configs/sources")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Slug != "generic-health-sample" || selection.Config.AllowedHost != "guidelines.example.test" {
		t.Fatalf("expected bundled synthetic source, got %#v", selection)
	}
}

func TestCLIStorePreservesLiteralDatabasePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows disallows question marks in filenames")
	}
	path := filepath.Join(t.TempDir(), "review?mode=rwc")
	store := mustStore(path)
	defer store.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database path was interpreted as SQLite options: %v", err)
	}
}
