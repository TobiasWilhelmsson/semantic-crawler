package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProofReadinessPassesWhenRequiredArtifactsAreHealthy(t *testing.T) {
	root := t.TempDir()
	writeReadinessFixture(t, root, "sample-selector-health", "selector-health.json", `{"total_urls":164,"valid_gold_pages":163,"partial_gold_pages":1,"fetch_failed_pages":0,"zero_gold_pages":0}`)
	writeReadinessFixture(t, root, "sample-prompt-diagnostics", "prompt-diagnostics.json", `{"expected_urls":1,"discovered_urls":1,"max_prompt_chars":8164,"max_block_count":25}`)
	writeReadinessFixture(t, root, "sample-model-compatibility", "model-compatibility.json", `{"ok":true,"model":"openai","request_timeout":"1m0s","checks":[{"name":"models","ok":true},{"name":"simple_chat","ok":true},{"name":"json_schema_chat","ok":true}]}`)
	writeReadinessFixture(t, root, "sample-model-preflight", "model-preflight.json", `{"ok":true,"usage":{"input_tokens":10,"output_tokens":2}}`)

	report, err := LoadProofReadiness(root)
	if err != nil {
		t.Fatalf("load readiness: %v", err)
	}
	if !report.Ready || len(report.Blockers) != 0 {
		t.Fatalf("expected readiness to pass: %#v", report)
	}
	if !report.SelectorHealth.OK || !report.PromptDiagnostics.OK || !report.ModelCompatibility.OK || !report.ModelPreflight.OK {
		t.Fatalf("individual gates should pass: %#v", report)
	}
}

func TestLoadProofReadinessBlocksWhenModelPreflightFailed(t *testing.T) {
	root := t.TempDir()
	writeReadinessFixture(t, root, "sample-selector-health", "selector-health.json", `{"total_urls":164,"valid_gold_pages":163,"partial_gold_pages":1,"fetch_failed_pages":0,"zero_gold_pages":0}`)
	writeReadinessFixture(t, root, "sample-prompt-diagnostics", "prompt-diagnostics.json", `{"expected_urls":1,"discovered_urls":1,"max_prompt_chars":8164,"max_block_count":25}`)
	writeReadinessFixture(t, root, "sample-model-compatibility", "model-compatibility.json", `{"ok":true,"model":"openai","request_timeout":"1m0s","checks":[{"name":"models","ok":true},{"name":"simple_chat","ok":true},{"name":"json_schema_chat","ok":true}]}`)
	writeReadinessFixture(t, root, "sample-model-preflight", "model-preflight.json", `{"ok":false,"error":"provider timeout"}`)

	report, err := LoadProofReadiness(root)
	if err != nil {
		t.Fatalf("load readiness: %v", err)
	}
	if report.Ready || report.ModelPreflight.OK {
		t.Fatalf("failed preflight should block readiness: %#v", report)
	}
	if len(report.Blockers) == 0 || !strings.Contains(strings.Join(report.Blockers, "\n"), "provider timeout") {
		t.Fatalf("blockers should explain failed Model preflight: %#v", report.Blockers)
	}
}

func TestWriteProofReadinessReportPersistsArtifact(t *testing.T) {
	root := t.TempDir()
	path, err := WriteProofReadinessReport(root, ProofReadinessReport{
		Ready:     true,
		Artifacts: map[string]string{"model_preflight": "model-preflight.json"},
	})
	if err != nil {
		t.Fatalf("write readiness: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read readiness artifact: %v", err)
	}
	if filepath.Base(path) != "proof-readiness.json" {
		t.Fatalf("unexpected readiness artifact path: %s", path)
	}
	if !strings.Contains(string(content), `"ready": true`) || !strings.Contains(string(content), `"model_preflight"`) {
		t.Fatalf("readiness artifact should contain report content: %s", content)
	}
}

func writeReadinessFixture(t *testing.T, root, directory, name, content string) {
	t.Helper()
	path := filepath.Join(root, directory)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
