package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type OpenAICompatibilityOptions struct {
	Config          OpenAIConfig
	ReportDirectory string
}

type OpenAICompatibilityReport struct {
	OK             bool                       `json:"ok"`
	Model          string                     `json:"model"`
	BaseURLHost    string                     `json:"base_url_host,omitempty"`
	StartedAt      time.Time                  `json:"started_at"`
	CompletedAt    time.Time                  `json:"completed_at"`
	LatencyMS      int64                      `json:"latency_ms"`
	RequestTimeout string                     `json:"request_timeout,omitempty"`
	MaxTokens      int                        `json:"max_tokens,omitempty"`
	Checks         []OpenAICompatibilityCheck `json:"checks"`
	Error          string                     `json:"error,omitempty"`
}

type OpenAICompatibilityCheck struct {
	Name             string       `json:"name"`
	OK               bool         `json:"ok"`
	Method           string       `json:"method"`
	Path             string       `json:"path"`
	StatusCode       int          `json:"status_code,omitempty"`
	LatencyMS        int64        `json:"latency_ms"`
	RequestShape     string       `json:"request_shape,omitempty"`
	ContentPresent   bool         `json:"content_present,omitempty"`
	ReasoningPresent bool         `json:"reasoning_present,omitempty"`
	FinishReason     string       `json:"finish_reason,omitempty"`
	ResponsePreview  string       `json:"response_preview,omitempty"`
	Usage            UsageSummary `json:"usage,omitempty"`
	Error            string       `json:"error,omitempty"`
}

func RunOpenAICompatibility(ctx context.Context, options OpenAICompatibilityOptions) (OpenAICompatibilityReport, error) {
	config := options.Config
	if config.BaseURL == "" {
		return OpenAICompatibilityReport{}, fmt.Errorf("base URL is required")
	}
	if config.Model == "" {
		return OpenAICompatibilityReport{}, fmt.Errorf("model is required")
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: config.Timeout, CheckRedirect: noHTTPRedirects}
	started := time.Now().UTC()
	report := OpenAICompatibilityReport{
		OK:             true,
		Model:          config.Model,
		BaseURLHost:    redactedBaseURL(config.BaseURL),
		StartedAt:      started,
		RequestTimeout: config.Timeout.String(),
		MaxTokens:      config.MaxTokens,
	}

	report.Checks = append(report.Checks, runCompatibilityModelsCheck(ctx, client, config))
	report.Checks = append(report.Checks, runCompatibilityChatCheck(ctx, client, config, "plain"))
	report.Checks = append(report.Checks, runCompatibilityChatCheck(ctx, client, config, "json_object"))
	report.Checks = append(report.Checks, runCompatibilityChatCheck(ctx, client, config, "json_schema"))
	for _, check := range report.Checks {
		if check.OK {
			continue
		}
		report.OK = false
		if report.Error == "" {
			report.Error = fmt.Sprintf("%s failed: %s", check.Name, check.Error)
		}
	}
	report.CompletedAt = time.Now().UTC()
	report.LatencyMS = report.CompletedAt.Sub(started).Milliseconds()
	if options.ReportDirectory != "" {
		if writeErr := WriteOpenAICompatibilityReport(options.ReportDirectory, report); writeErr != nil {
			return report, writeErr
		}
	}
	if !report.OK {
		return report, fmt.Errorf("OpenAI compatibility failed: %s", report.Error)
	}
	return report, nil
}

func runCompatibilityModelsCheck(ctx context.Context, client *http.Client, config OpenAIConfig) OpenAICompatibilityCheck {
	check := OpenAICompatibilityCheck{Name: "models", Method: http.MethodGet, Path: "/models", RequestShape: "models_list"}
	request, err := newCompatibilityRequest(ctx, config, http.MethodGet, "/models", nil)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	return performCompatibilityRequest(client, request, check)
}

func runCompatibilityChatCheck(ctx context.Context, client *http.Client, config OpenAIConfig, format string) OpenAICompatibilityCheck {
	check := OpenAICompatibilityCheck{Name: "simple_chat", Method: http.MethodPost, Path: "/chat/completions", RequestShape: "plain_chat_completion"}
	payload := map[string]any{
		"model": config.Model,
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with the word ok."},
		},
	}
	setCompletionParameters(payload, config, compatibilityMaxTokens(config))
	switch format {
	case "json_object":
		check.Name = "json_object_chat"
		check.RequestShape = "chat_completion_json_object"
		payload["messages"] = []map[string]string{
			{"role": "system", "content": "Return only a JSON object."},
			{"role": "user", "content": `Return {"action":"extract","confidence":0.99,"reason":"ok"}.`},
		}
		payload["response_format"] = map[string]any{"type": "json_object"}
	case "json_schema":
		check.Name = "json_schema_chat"
		check.RequestShape = "chat_completion_json_schema"
		format, schemaPrompt, err := structuredOutputFormat(config.Provider, "semantic_route", routeJSONSchema())
		if err != nil {
			check.Error = err.Error()
			return check
		}
		if format["type"] == "json_object" {
			check.Name = "configured_json_mode_chat"
			check.RequestShape = "chat_completion_json_object_local_schema"
		}
		payload["messages"] = []map[string]string{
			{"role": "system", "content": "Return only data matching the supplied JSON schema." + schemaPrompt},
			{"role": "user", "content": "Classify this compatibility check as extract."},
		}
		payload["response_format"] = format
	}
	body, err := json.Marshal(payload)
	if err != nil {
		check.Error = fmt.Sprintf("encode request: %v", err)
		return check
	}
	request, err := newCompatibilityRequest(ctx, config, http.MethodPost, "/chat/completions", body)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	return performCompatibilityRequest(client, request, check)
}

func compatibilityMaxTokens(config OpenAIConfig) int {
	if config.MaxTokens > 0 {
		return config.MaxTokens
	}
	return 32
}

func newCompatibilityRequest(ctx context.Context, config OpenAIConfig, method, path string, body []byte) (*http.Request, error) {
	endpoint := strings.TrimRight(config.BaseURL, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	return request, nil
}

func performCompatibilityRequest(client *http.Client, request *http.Request, check OpenAICompatibilityCheck) OpenAICompatibilityCheck {
	started := time.Now()
	response, err := client.Do(request)
	check.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		check.Error = fmt.Sprintf("perform request: %v", err)
		return check
	}
	defer response.Body.Close()
	check.StatusCode = response.StatusCode
	raw, err := readBoundedResponse(response.Body, defaultMaxModelBytes)
	if err != nil {
		check.Error = fmt.Sprintf("read response: %v", err)
		return check
	}
	check.ResponsePreview = truncateForReport(strings.TrimSpace(string(raw)), 500)
	check.Usage = UsageSummary{LatencyMS: check.LatencyMS, Attempts: 1}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		check.Error = fmt.Sprintf("HTTP %d: %s", response.StatusCode, check.ResponsePreview)
		return check
	}
	if check.Method == http.MethodGet {
		if err := validateModelsList(raw); err != nil {
			check.Error = fmt.Sprintf("decode models list: %v", err)
			return check
		}
	} else {
		var envelope openAICompletionEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			check.Error = fmt.Sprintf("decode model envelope: %v", err)
			return check
		}
		check.Usage.InputTokens = envelope.Usage.PromptTokens
		check.Usage.OutputTokens = envelope.Usage.CompletionTokens
		check.Usage.TotalTokens = envelope.Usage.TotalTokens
		if len(envelope.Choices) > 0 {
			choice := envelope.Choices[0]
			check.ContentPresent = choice.Message.Content != nil && strings.TrimSpace(*choice.Message.Content) != ""
			check.ReasoningPresent = strings.TrimSpace(choice.Message.Reasoning) != ""
			_ = json.Unmarshal(choice.FinishReason, &check.FinishReason)
		}
		content, err := envelope.structuredContent()
		if err != nil {
			check.Error = err.Error()
			if len(envelope.Choices) <= 1 && !check.ContentPresent {
				check.Error = "assistant content was empty"
			}
			return check
		}
		var schema map[string]any
		switch check.RequestShape {
		case "chat_completion_json_object":
			schema = map[string]any{"type": "object"}
		case "chat_completion_json_schema", "chat_completion_json_object_local_schema":
			schema = routeJSONSchema()
		}
		if schema != nil {
			if err := validateStructuredContent(content, schema); err != nil {
				check.Error = fmt.Sprintf("decode structured model content: %v", err)
				return check
			}
		}
	}
	check.OK = true
	return check
}

func validateModelsList(raw []byte) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	var models []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &models); err != nil {
		return err
	}
	if models == nil {
		return fmt.Errorf("expected data array")
	}
	for index, model := range models {
		if strings.TrimSpace(model.ID) == "" {
			return fmt.Errorf("data[%d]: model id is required", index)
		}
	}
	return nil
}

func WriteOpenAICompatibilityReport(directory string, report OpenAICompatibilityReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create compatibility directory: %w", err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode compatibility report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "model-compatibility.json"), append(content, '\n'), 0o644); err != nil {
		return fmt.Errorf("write compatibility report: %w", err)
	}
	return nil
}

func redactedBaseURL(raw string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if slash := strings.Index(trimmed, "/"); slash >= 0 {
		trimmed = trimmed[:slash]
	}
	return trimmed
}

func truncateForReport(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength] + "...[truncated]"
}
