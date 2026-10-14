package semantic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ProofReadinessReport struct {
	Ready              bool               `json:"ready"`
	Blockers           []string           `json:"blockers,omitempty"`
	SelectorHealth     ProofReadinessGate `json:"selector_health"`
	PromptDiagnostics  ProofReadinessGate `json:"prompt_diagnostics"`
	ModelCompatibility ProofReadinessGate `json:"model_compatibility"`
	ModelPreflight     ProofReadinessGate `json:"model_preflight"`
	Artifacts          map[string]string  `json:"artifacts"`
}

type ProofReadinessGate struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
	Error   string `json:"error,omitempty"`
}

func LoadProofReadiness(root string) (ProofReadinessReport, error) {
	report := ProofReadinessReport{
		Ready: true,
		Artifacts: map[string]string{
			"selector_health":     filepath.Join(root, "sample-selector-health", "selector-health.json"),
			"prompt_diagnostics":  filepath.Join(root, "sample-prompt-diagnostics", "prompt-diagnostics.json"),
			"model_compatibility": filepath.Join(root, "sample-model-compatibility", "model-compatibility.json"),
			"model_preflight":     filepath.Join(root, "sample-model-preflight", "model-preflight.json"),
		},
	}
	report.SelectorHealth = selectorHealthReadiness(report.Artifacts["selector_health"])
	report.PromptDiagnostics = promptDiagnosticsReadiness(report.Artifacts["prompt_diagnostics"])
	report.ModelCompatibility = modelCompatibilityReadiness(report.Artifacts["model_compatibility"])
	report.ModelPreflight = modelPreflightReadiness(report.Artifacts["model_preflight"])
	for _, gate := range []ProofReadinessGate{report.SelectorHealth, report.PromptDiagnostics, report.ModelCompatibility, report.ModelPreflight} {
		if gate.OK {
			continue
		}
		report.Ready = false
		if gate.Error != "" {
			report.Blockers = append(report.Blockers, gate.Error)
		} else {
			report.Blockers = append(report.Blockers, gate.Summary)
		}
	}
	return report, nil
}

func WriteProofReadinessReport(root string, report ProofReadinessReport) (string, error) {
	path := filepath.Join(root, "proof-readiness.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func selectorHealthReadiness(path string) ProofReadinessGate {
	var report SelectorHealthReport
	if err := readJSONFile(path, &report); err != nil {
		return ProofReadinessGate{Summary: "selector health artifact missing or unreadable", Error: err.Error()}
	}
	ok := report.TotalURLs > 0 && report.FetchFailedPages == 0 && report.ZeroGoldPages == 0 && report.ValidGoldPages+report.PartialGoldPages > 0
	summary := fmt.Sprintf("%d URLs, %d valid gold, %d partial, %d fetch failed, %d zero gold", report.TotalURLs, report.ValidGoldPages, report.PartialGoldPages, report.FetchFailedPages, report.ZeroGoldPages)
	gate := ProofReadinessGate{OK: ok, Summary: summary}
	if !ok {
		gate.Error = "selector health is not proof-ready: " + summary
	}
	return gate
}

func promptDiagnosticsReadiness(path string) ProofReadinessGate {
	var report PromptDiagnosticsReport
	if err := readJSONFile(path, &report); err != nil {
		return ProofReadinessGate{Summary: "prompt diagnostics artifact missing or unreadable", Error: err.Error()}
	}
	ok := report.ExpectedURLs > 0 && report.DiscoveredURLs == report.ExpectedURLs && report.MaxPromptChars > 0 && report.MaxPromptChars <= 50000
	summary := fmt.Sprintf("%d/%d pages discovered, max prompt chars %d, max blocks %d", report.DiscoveredURLs, report.ExpectedURLs, report.MaxPromptChars, report.MaxBlockCount)
	gate := ProofReadinessGate{OK: ok, Summary: summary}
	if !ok {
		gate.Error = "prompt diagnostics are not proof-ready: " + summary
	}
	return gate
}

func modelPreflightReadiness(path string) ProofReadinessGate {
	var report ModelPreflightReport
	if err := readJSONFile(path, &report); err != nil {
		return ProofReadinessGate{Summary: "Model preflight artifact missing or unreadable", Error: err.Error()}
	}
	summary := fmt.Sprintf("ok=%t, model=%s, latency_ms=%d, attempts=%d, timeout=%s, max_tokens=%d, retries=%d, input_tokens=%d, output_tokens=%d", report.OK, report.Model, report.LatencyMS, report.Usage.Attempts, report.RequestTimeout, report.MaxTokens, report.MaxRetries, report.Usage.InputTokens, report.Usage.OutputTokens)
	gate := ProofReadinessGate{OK: report.OK, Summary: summary}
	if !report.OK {
		if report.Error != "" {
			gate.Error = "Model preflight failed: " + report.Error
		} else {
			gate.Error = "Model preflight failed"
		}
	}
	return gate
}

func modelCompatibilityReadiness(path string) ProofReadinessGate {
	var report OpenAICompatibilityReport
	if err := readJSONFile(path, &report); err != nil {
		return ProofReadinessGate{Summary: "Model compatibility artifact missing or unreadable", Error: err.Error()}
	}
	summary := fmt.Sprintf("ok=%t, model=%s, checks=%d, latency_ms=%d, timeout=%s", report.OK, report.Model, len(report.Checks), report.LatencyMS, report.RequestTimeout)
	gate := ProofReadinessGate{OK: report.OK, Summary: summary}
	if !report.OK {
		if report.Error != "" {
			gate.Error = "Model compatibility failed: " + report.Error
		} else {
			gate.Error = "Model compatibility failed"
		}
	}
	return gate
}

func readJSONFile(path string, destination any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(content, destination); err != nil {
		return err
	}
	return nil
}
