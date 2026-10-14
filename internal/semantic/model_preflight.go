package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ModelPreflightOptions struct {
	Model           SemanticModel
	ModelName       string
	Scope           string
	URL             string
	RequestTimeout  time.Duration
	MaxTokens       int
	MaxRetries      int
	RetryDelay      time.Duration
	ReportDirectory string
}

type ModelPreflightReport struct {
	OK             bool                `json:"ok"`
	Model          string              `json:"model"`
	URL            string              `json:"url"`
	StartedAt      time.Time           `json:"started_at"`
	CompletedAt    time.Time           `json:"completed_at"`
	LatencyMS      int64               `json:"latency_ms"`
	RequestTimeout string              `json:"request_timeout,omitempty"`
	MaxTokens      int                 `json:"max_tokens,omitempty"`
	MaxRetries     int                 `json:"max_retries"`
	RetryDelay     string              `json:"retry_delay,omitempty"`
	Route          RouteResponse       `json:"route,omitempty"`
	Extraction     *ExtractionResponse `json:"extraction,omitempty"`
	Usage          UsageSummary        `json:"usage"`
	Error          string              `json:"error,omitempty"`
}

type UsageSummary struct {
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	TotalTokens  int   `json:"total_tokens"`
	LatencyMS    int64 `json:"latency_ms"`
	Attempts     int   `json:"attempts,omitempty"`
}

func RunModelPreflight(ctx context.Context, options ModelPreflightOptions) (ModelPreflightReport, error) {
	if options.Model == nil {
		return ModelPreflightReport{}, fmt.Errorf("preflight model is required")
	}
	started := time.Now().UTC()
	prompt := "Classify this structured-output compatibility check as extract."
	route, usage, err := options.Model.Route(ctx, prompt)
	if usage.Attempts == 0 {
		usage.Attempts = 1
	}
	var extraction *ExtractionResponse
	if err == nil {
		blocks := []CandidateBlock{{ID: "b0001", Tag: "p", Text: "Compatibility example.", HTML: "<p>Compatibility example.</p>"}}
		extractionPrompt := `Extract this structured-output compatibility check. Select b0001 with include=true, role=content, type=none, reason=ok. Use page_kind=article and uncertainties=[]. Source block b0001: <p>Compatibility example.</p>`
		response, extractionUsage, extractionErr := options.Model.Extract(ctx, extractionPrompt, blocks)
		if extractionUsage.Attempts == 0 {
			extractionUsage.Attempts = 1
		}
		usage.InputTokens += extractionUsage.InputTokens
		usage.OutputTokens += extractionUsage.OutputTokens
		usage.TotalTokens += extractionUsage.TotalTokens
		usage.Latency += extractionUsage.Latency
		usage.Attempts += extractionUsage.Attempts
		if extractionErr == nil {
			extractionErr = ValidateExtraction(response, blocks)
		}
		if extractionErr != nil {
			err = fmt.Errorf("model preflight extraction: %w", extractionErr)
		} else {
			extraction = &response
		}
	}
	completed := time.Now().UTC()
	report := ModelPreflightReport{
		OK:             err == nil,
		Model:          options.ModelName,
		URL:            options.URL,
		StartedAt:      started,
		CompletedAt:    completed,
		LatencyMS:      completed.Sub(started).Milliseconds(),
		RequestTimeout: durationString(options.RequestTimeout),
		MaxTokens:      options.MaxTokens,
		MaxRetries:     options.MaxRetries,
		RetryDelay:     durationString(options.RetryDelay),
		Route:          route,
		Extraction:     extraction,
		Usage: UsageSummary{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
			TotalTokens:  usage.TotalTokens,
			LatencyMS:    usage.Latency.Milliseconds(),
			Attempts:     usage.Attempts,
		},
	}
	if err != nil {
		report.Error = err.Error()
	}
	if options.ReportDirectory != "" {
		if writeErr := WriteModelPreflightReport(options.ReportDirectory, report); writeErr != nil {
			return report, writeErr
		}
	}
	if err != nil {
		return report, err
	}
	return report, nil
}

func durationString(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}

func WriteModelPreflightReport(directory string, report ModelPreflightReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create model preflight directory: %w", err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model preflight report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "model-preflight.json"), content, 0o644); err != nil {
		return fmt.Errorf("write model preflight report: %w", err)
	}
	return nil
}
