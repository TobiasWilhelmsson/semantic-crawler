package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type preflightSuccessModel struct{}

func (model preflightSuccessModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	if !strings.Contains(prompt, "structured-output compatibility check") {
		return RouteResponse{}, ModelUsage{}, errors.New("missing preflight prompt text")
	}
	return RouteResponse{Action: RouteExtract, Confidence: 0.99, Reason: "ok"}, ModelUsage{InputTokens: 9, OutputTokens: 3, TotalTokens: 12, Latency: 2 * time.Millisecond}, nil
}

func (model preflightSuccessModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	return ExtractionResponse{PageKind: "article", Blocks: []BlockDecision{{BlockID: blocks[0].ID, Include: true, Role: RoleContent, Type: TypeNone, ParseMode: true, Reason: "ok"}}, Uncertainties: []string{}}, ModelUsage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7, Latency: time.Millisecond}, nil
}

type preflightFailModel struct{}

func (model preflightFailModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{}, ModelUsage{Latency: time.Millisecond}, errors.New("provider timeout")
}

func (model preflightFailModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	return ExtractionResponse{}, ModelUsage{}, nil
}

type preflightPromptCaptureModel struct {
	prompt           string
	extractionPrompt string
}

func (model *preflightPromptCaptureModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	model.prompt = prompt
	return RouteResponse{Action: RouteExtract, Confidence: 0.99, Reason: "ok"}, ModelUsage{}, nil
}

func (model *preflightPromptCaptureModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extractionPrompt = prompt
	return ExtractionResponse{PageKind: "article", Blocks: []BlockDecision{}, Uncertainties: []string{}}, ModelUsage{}, nil
}

func TestRunModelPreflightUsesSmallCompatibilityPrompt(t *testing.T) {
	model := &preflightPromptCaptureModel{}
	longScope := strings.Repeat("clinical scope sentence. ", 200)
	_, err := RunModelPreflight(context.Background(), ModelPreflightOptions{
		Model:     model,
		ModelName: "model-test",
		Scope:     longScope,
		URL:       "https://example.test/article",
	})
	if err != nil {
		t.Fatalf("run preflight: %v", err)
	}
	if len(model.extractionPrompt) > 1400 || strings.Contains(model.extractionPrompt, longScope) || strings.Contains(model.extractionPrompt, "https://example.test/article") || !strings.Contains(model.extractionPrompt, "b0001") {
		t.Fatalf("preflight extraction should use a tiny synthetic block: %s", model.extractionPrompt)
	}
	if len(model.prompt) > 1400 {
		t.Fatalf("preflight prompt should stay small, got %d chars", len(model.prompt))
	}
	if strings.Contains(model.prompt, longScope) || strings.Contains(model.prompt, "https://example.test/article") || !strings.Contains(model.prompt, "structured-output compatibility check") {
		t.Fatalf("preflight prompt should be a small compatibility prompt, got: %s", model.prompt)
	}
}

func TestRunModelPreflightWritesSuccessReport(t *testing.T) {
	reportDirectory := t.TempDir()
	report, err := RunModelPreflight(context.Background(), ModelPreflightOptions{
		Model:           preflightSuccessModel{},
		ModelName:       "model-test",
		Scope:           "clinical",
		URL:             "https://example.test/article",
		RequestTimeout:  60 * time.Second,
		MaxTokens:       4096,
		MaxRetries:      1,
		RetryDelay:      5 * time.Second,
		ReportDirectory: reportDirectory,
	})
	if err != nil {
		t.Fatalf("run preflight: %v", err)
	}
	if !report.OK || report.Route.Action != RouteExtract || report.Usage.InputTokens != 14 || report.Usage.OutputTokens != 5 || report.Usage.TotalTokens != 19 || report.Usage.LatencyMS != 3 || report.Usage.Attempts != 2 || report.Extraction == nil || len(report.Extraction.Blocks) != 1 || report.Error != "" {
		t.Fatalf("unexpected success report: %#v", report)
	}
	if report.RequestTimeout != "1m0s" || report.MaxTokens != 4096 || report.MaxRetries != 1 || report.RetryDelay != "5s" {
		t.Fatalf("preflight report should include request settings: %#v", report)
	}
	content, readErr := os.ReadFile(filepath.Join(reportDirectory, "model-preflight.json"))
	if readErr != nil {
		t.Fatalf("preflight report was not written: %v", readErr)
	}
	if !strings.Contains(string(content), `"ok": true`) || !strings.Contains(string(content), `"model": "model-test"`) || !strings.Contains(string(content), `"request_timeout": "1m0s"`) || !strings.Contains(string(content), `"max_tokens": 4096`) {
		t.Fatalf("written report missing success fields: %s", content)
	}
}

func TestRunModelPreflightWritesFailureReportBeforeReturningError(t *testing.T) {
	reportDirectory := t.TempDir()
	report, err := RunModelPreflight(context.Background(), ModelPreflightOptions{
		Model:           preflightFailModel{},
		ModelName:       "model-test",
		Scope:           "clinical",
		URL:             "https://example.test/article",
		ReportDirectory: reportDirectory,
	})
	if err == nil {
		t.Fatal("expected preflight error")
	}
	if report.OK || !strings.Contains(report.Error, "provider timeout") {
		t.Fatalf("unexpected failure report: %#v", report)
	}
	content, readErr := os.ReadFile(filepath.Join(reportDirectory, "model-preflight.json"))
	if readErr != nil {
		t.Fatalf("failure report was not written: %v", readErr)
	}
	if !strings.Contains(string(content), `"ok": false`) || !strings.Contains(string(content), "provider timeout") {
		t.Fatalf("written report missing failure fields: %s", content)
	}
}

func TestRunModelPreflightFailsWhenExtractionSchemaIsUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		format := request["response_format"].(map[string]any)["json_schema"].(map[string]any)
		if format["name"] == "semantic_extraction" {
			http.Error(w, "nested extraction schema unsupported", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(completionTestEnvelope(`{"action":"extract","confidence":1,"reason":"ok"}`, "stop"))
	}))
	defer server.Close()
	reportDirectory := t.TempDir()
	report, err := RunModelPreflight(context.Background(), ModelPreflightOptions{Model: NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "test"}), ModelName: "test", ReportDirectory: reportDirectory})
	if err == nil || report.OK || report.Route.Action != RouteExtract || !strings.Contains(report.Error, "extraction") || report.Usage.InputTokens != 7 {
		t.Fatalf("route-only provider must fail preflight with extraction diagnostics: %#v err=%v", report, err)
	}
	content, readErr := os.ReadFile(filepath.Join(reportDirectory, "model-preflight.json"))
	if readErr != nil || !strings.Contains(string(content), `"ok": false`) || !strings.Contains(string(content), "HTTP 400") || strings.Contains(string(content), "nested extraction schema unsupported") {
		t.Fatalf("extraction failure must be written: %s err=%v", content, readErr)
	}
}
