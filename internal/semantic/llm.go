package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type OpenAIConfig struct {
	Provider         string `json:"provider,omitempty"`
	BaseURL          string
	APIKey           string
	Model            string
	Timeout          time.Duration
	MaxTokens        int
	MaxRetries       int
	RetryDelay       time.Duration
	MaxResponseBytes int64
}

type ModelUsage struct {
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	TotalTokens  int           `json:"total_tokens"`
	Latency      time.Duration `json:"latency"`
	Attempts     int           `json:"attempts,omitempty"`
	RawResponse  string        `json:"raw_response"`
}

type OpenAIClient struct {
	config OpenAIConfig
	http   *http.Client
}

func NewOpenAIClient(config OpenAIConfig) *OpenAIClient {
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	if config.MaxTokens <= 0 {
		config.MaxTokens = 8192
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = time.Second
	}
	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = defaultMaxModelBytes
	}
	return &OpenAIClient{config: config, http: &http.Client{Timeout: config.Timeout, CheckRedirect: noHTTPRedirects}}
}

func (client *OpenAIClient) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	var response RouteResponse
	usage, err := client.complete(ctx, "semantic_route", routeJSONSchema(), prompt, &response)
	if err != nil {
		return RouteResponse{}, usage, err
	}
	if !validRouteAction(response.Action) {
		return RouteResponse{}, usage, fmt.Errorf("invalid route action %q", response.Action)
	}
	if response.Confidence < 0 || response.Confidence > 1 {
		return RouteResponse{}, usage, fmt.Errorf("route confidence must be between 0 and 1")
	}
	return response, usage, nil
}

func (client *OpenAIClient) Extract(ctx context.Context, prompt string, candidates []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	var response ExtractionResponse
	usage, err := client.complete(ctx, "semantic_extraction", extractionJSONSchema(), prompt, &response)
	if err != nil {
		return ExtractionResponse{}, usage, err
	}
	if err := ValidateExtraction(response, candidates); err != nil {
		return ExtractionResponse{}, usage, fmt.Errorf("validate extraction response: %w", err)
	}
	// Rendering is an application policy, never a model selection decision.
	for i := range response.Blocks {
		response.Blocks[i].ParseMode = true
	}
	return response, usage, nil
}

func (client *OpenAIClient) complete(ctx context.Context, schemaName string, schema map[string]any, prompt string, destination any) (ModelUsage, error) {
	format, schemaPrompt, err := structuredOutputFormat(client.config.Provider, schemaName, schema)
	if err != nil {
		return ModelUsage{}, err
	}
	payload := map[string]any{
		"model": client.config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "Return only data matching the supplied JSON schema. Never invent source content. Keep every reason terse, preferably under 12 words." + schemaPrompt},
			{"role": "user", "content": prompt},
		},
		"response_format": format,
	}
	setCompletionParameters(payload, client.config, client.config.MaxTokens)
	body, err := json.Marshal(payload)
	if err != nil {
		return ModelUsage{}, fmt.Errorf("encode model request: %w", err)
	}

	var usage ModelUsage
	var lastErr error
	for attempt := 0; attempt <= client.config.MaxRetries; attempt++ {
		if attempt > 0 {
			if sleepErr := sleepContext(ctx, client.config.RetryDelay); sleepErr != nil {
				return usage, sleepErr
			}
		}
		usage, lastErr = client.performCompleteAttempt(ctx, body, schema, destination)
		usage.Attempts = attempt + 1
		if lastErr == nil || !retryableModelRequestError(lastErr) {
			return usage, lastErr
		}
	}
	return usage, lastErr
}

// The OpenAI preset uses the current completion-token parameter and leaves
// temperature to the model, including reasoning models that reject it.
// Budget presets disable thinking for their documented economical models.
// Gemini and custom services retain the existing compatible request shape.
func setCompletionParameters(payload map[string]any, config OpenAIConfig, maxTokens int) {
	if config.Provider == "openai" {
		payload["max_completion_tokens"] = maxTokens
		return
	}
	payload["max_tokens"] = maxTokens
	payload["temperature"] = 0
	switch config.Provider {
	case "deepseek", "zai":
		payload["thinking"] = map[string]string{"type": "disabled"}
	case "qwen":
		payload["enable_thinking"] = false
	}
}

func structuredOutputFormat(provider, name string, schema map[string]any) (map[string]any, string, error) {
	switch provider {
	case "deepseek", "qwen", "zai":
		encoded, err := json.Marshal(schema)
		if err != nil {
			return nil, "", fmt.Errorf("encode JSON-mode schema: %w", err)
		}
		// JSON mode guarantees syntax only. The full schema is included in the
		// instructions and independently validated before accepting any content.
		return map[string]any{"type": "json_object"}, "\nJSON Schema:\n" + string(encoded), nil
	default:
		return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": name, "strict": true, "schema": schema}}, "", nil
	}
}

func (client *OpenAIClient) performCompleteAttempt(ctx context.Context, body []byte, schema map[string]any, destination any) (ModelUsage, error) {
	endpoint := strings.TrimRight(client.config.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ModelUsage{}, fmt.Errorf("create model request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if client.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+client.config.APIKey)
	}

	started := time.Now()
	response, err := client.http.Do(request)
	latency := time.Since(started)
	if err != nil {
		return ModelUsage{Latency: latency}, fmt.Errorf("perform model request: %w", err)
	}
	defer response.Body.Close()
	raw, err := readBoundedResponse(response.Body, client.config.MaxResponseBytes)
	if err != nil {
		return ModelUsage{Latency: latency}, fmt.Errorf("read model response: %w", err)
	}
	usage := ModelUsage{Latency: latency, RawResponse: string(raw)}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Provider error bodies may echo credentials or request content. Keep the
		// private raw diagnostic in usage, never in an error rendered by the UI.
		return usage, fmt.Errorf("model returned HTTP %d", response.StatusCode)
	}

	var envelope openAICompletionEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return usage, fmt.Errorf("decode model envelope: %w", err)
	}
	usage.InputTokens = envelope.Usage.PromptTokens
	usage.OutputTokens = envelope.Usage.CompletionTokens
	usage.TotalTokens = envelope.Usage.TotalTokens
	content, err := envelope.structuredContent()
	if err != nil {
		return usage, err
	}
	if err := validateStructuredContent(content, schema); err != nil {
		return usage, fmt.Errorf("decode structured model content: %w", err)
	}
	if err := json.Unmarshal([]byte(content), destination); err != nil {
		return usage, fmt.Errorf("decode structured model content: %w", err)
	}
	return usage, nil
}

func retryableModelRequestError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "perform model request:") ||
		strings.Contains(message, "read model response:") ||
		strings.Contains(message, "model returned HTTP 429") ||
		strings.Contains(message, "model returned HTTP 500") ||
		strings.Contains(message, "model returned HTTP 502") ||
		strings.Contains(message, "model returned HTTP 503") ||
		strings.Contains(message, "model returned HTTP 504")
}

func validRouteAction(action RouteAction) bool {
	switch action {
	case RouteExtract, RouteNavigateOnly, RouteIgnore, RouteNeedsReview:
		return true
	default:
		return false
	}
}

func routeJSONSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"action", "confidence", "reason"},
		"properties": map[string]any{
			"action":     map[string]any{"type": "string", "enum": []string{"extract", "navigate_only", "ignore", "needs_review"}},
			"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"reason":     map[string]any{"type": "string", "maxLength": 160},
		},
	}
}

func extractionJSONSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"page_kind", "blocks", "uncertainties"},
		"properties": map[string]any{
			"page_kind":     map[string]any{"type": "string", "enum": []string{"article", "navigation", "mixed", "unknown"}},
			"uncertainties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"blocks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"block_id", "include", "role", "type", "reason"},
					"properties": map[string]any{
						"block_id": map[string]any{"type": "string"},
						"include":  map[string]any{"type": "boolean"},
						"role":     map[string]any{"type": "string", "enum": []string{"header", "content", "info_box", "metadata", "navigation", "table", "media", "media_group", "video"}},
						"type":     map[string]any{"type": "string", "enum": []string{"critical", "warning", "info", "none"}},
						"reason":   map[string]any{"type": "string", "maxLength": 120},
					},
				},
			},
		},
	}
}

type openAICompletionEnvelope struct {
	Choices []struct {
		FinishReason json.RawMessage `json:"finish_reason"`
		Message      struct {
			Content   *string `json:"content"`
			Reasoning string  `json:"reasoning"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (envelope openAICompletionEnvelope) structuredContent() (string, error) {
	if len(envelope.Choices) == 0 {
		return "", fmt.Errorf("model response contained no structured content")
	}
	if len(envelope.Choices) != 1 {
		return "", fmt.Errorf("decode structured model content: expected one completion choice, got %d", len(envelope.Choices))
	}
	choice := envelope.Choices[0]
	if choice.Message.Content == nil || strings.TrimSpace(*choice.Message.Content) == "" {
		return "", fmt.Errorf("model response contained no structured content")
	}
	if len(choice.FinishReason) > 0 {
		var finishReason *string
		if err := json.Unmarshal(choice.FinishReason, &finishReason); err != nil {
			return "", fmt.Errorf("decode structured model content: invalid completion finish_reason: %w", err)
		}
		if finishReason == nil || *finishReason != "stop" {
			return "", fmt.Errorf("decode structured model content: completion finish_reason was %s", choice.FinishReason)
		}
	}
	return *choice.Message.Content, nil
}

func validateStructuredContent(content string, schema map[string]any) error {
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return err
	}
	return validateJSONSchemaValue(value, schema, "$")
}

// This validator supports only the keywords used by the local model schemas.
// Reject new keywords until their validation is implemented as well.
func validateJSONSchemaValue(value any, schema map[string]any, path string) error {
	for keyword := range schema {
		switch keyword {
		case "type", "required", "additionalProperties", "properties", "items", "enum", "minimum", "maximum", "maxLength":
		default:
			return fmt.Errorf("%s: unsupported schema keyword %q", path, keyword)
		}
	}
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object", path)
		}
		required, _ := schema["required"].([]string)
		for _, name := range required {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s: missing required field %q", path, name)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, field := range object {
			property, known := properties[name]
			if !known {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s: unknown field %q", path, name)
				}
				continue
			}
			if err := validateJSONSchemaValue(field, property.(map[string]any), path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array", path)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, item := range array {
				if err := validateJSONSchemaValue(item, itemSchema, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: expected string", path)
		}
		if maximum, ok := schema["maxLength"].(int); ok && utf8.RuneCountInString(text) > maximum {
			return fmt.Errorf("%s: string exceeds maxLength %d", path, maximum)
		}
		if values, ok := schema["enum"].([]string); ok && !slices.Contains(values, text) {
			return fmt.Errorf("%s: invalid enum value %q", path, text)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean", path)
		}
	case "number":
		number, ok := value.(float64)
		if !ok {
			return fmt.Errorf("%s: expected number", path)
		}
		if minimum, ok := schema["minimum"].(int); ok && number < float64(minimum) {
			return fmt.Errorf("%s: number is below minimum %d", path, minimum)
		}
		if maximum, ok := schema["maximum"].(int); ok && number > float64(maximum) {
			return fmt.Errorf("%s: number is above maximum %d", path, maximum)
		}
	default:
		return fmt.Errorf("%s: unsupported schema type %q", path, schema["type"])
	}
	return nil
}
