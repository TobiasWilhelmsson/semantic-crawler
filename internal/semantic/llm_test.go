package semantic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIClientUsesStrictJSONSchemaAndCapturesUsage(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"{\"action\":\"extract\",\"confidence\":0.98,\"reason\":\"clinical article\"}"}}],
			"usage":{"prompt_tokens":120,"completion_tokens":18,"total_tokens":138}
		}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIConfig{
		BaseURL: server.URL,
		APIKey:  "secret",
		Model:   "zai-org/Model-4.7-FP8",
	})
	response, usage, err := client.Route(context.Background(), "classify this page")
	if err != nil {
		t.Fatalf("route request: %v", err)
	}
	if response.Action != RouteExtract || usage.InputTokens != 120 || usage.OutputTokens != 18 {
		t.Fatalf("unexpected response or usage: %#v %#v", response, usage)
	}
	if request["model"] != "zai-org/Model-4.7-FP8" || request["temperature"] != float64(0) {
		t.Fatalf("unexpected model request: %#v", request)
	}
	format := request["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("structured output not requested: %#v", format)
	}
	schema := format["json_schema"].(map[string]any)
	if schema["strict"] != true || schema["name"] != "semantic_route" {
		t.Fatalf("strict schema not supplied: %#v", schema)
	}
	properties := schema["schema"].(map[string]any)["properties"].(map[string]any)
	reason := properties["reason"].(map[string]any)
	if reason["maxLength"] != float64(160) {
		t.Fatalf("route reason should be bounded: %#v", reason)
	}
}

func TestExtractionSchemaBoundsFreeFormReasons(t *testing.T) {
	schema := extractionJSONSchema()
	blockSchema := schema["properties"].(map[string]any)["blocks"].(map[string]any)["items"].(map[string]any)
	reason := blockSchema["properties"].(map[string]any)["reason"].(map[string]any)
	if reason["maxLength"] != 120 {
		t.Fatalf("extraction reason should be bounded: %#v", reason)
	}
}

func TestOpenAIClientRejectsSemanticallyInvalidStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"action\":\"invented\",\"confidence\":2,\"reason\":\"bad\"}"}}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "openai"})
	if _, _, err := client.Route(context.Background(), "classify"); err == nil {
		t.Fatal("expected invalid route action and confidence to be rejected")
	}
}

func TestOpenAIClientValidatesExtractionBlockIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"page_kind\":\"article\",\"blocks\":[{\"block_id\":\"b9999\",\"include\":true,\"role\":\"content\",\"type\":\"none\",\"reason\":\"x\"}],\"uncertainties\":[]}"}}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "openai"})
	if _, _, err := client.Extract(context.Background(), "extract", []CandidateBlock{{ID: "b0001"}}); err == nil {
		t.Fatal("expected unknown extraction block ID to be rejected")
	}
}

func TestOpenAIClientRetriesTransientHTTPFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"{\"action\":\"extract\",\"confidence\":0.98,\"reason\":\"clinical article\"}"}}],
			"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}
		}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "openai", MaxRetries: 1, RetryDelay: time.Millisecond})
	response, usage, err := client.Route(context.Background(), "classify")
	if err != nil {
		t.Fatalf("route should retry transient HTTP failure: %v", err)
	}
	if attempts != 2 || usage.Attempts != 2 || response.Action != RouteExtract || usage.InputTokens != 12 || usage.Latency <= 0 {
		t.Fatalf("unexpected retry result attempts=%d response=%#v usage=%#v", attempts, response, usage)
	}
}

func TestOpenAIClientDoesNotRetryStructuredContentErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not-json"}}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "openai", MaxRetries: 3, RetryDelay: time.Millisecond})
	if _, _, err := client.Route(context.Background(), "classify"); err == nil {
		t.Fatal("expected structured content error")
	}
	if attempts != 1 {
		t.Fatalf("structured content errors should not be retried, attempts=%d", attempts)
	}
}

func TestModelHTTPErrorsDoNotExposeProviderResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("rejected secret echoed-key-value"))
	}))
	defer server.Close()
	client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "test", APIKey: "echoed-key-value"})
	_, usage, err := client.Route(context.Background(), "test")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected actionable HTTP status: %v", err)
	}
	if strings.Contains(err.Error(), "echoed-key-value") {
		t.Fatalf("provider response leaked in operational error: %v", err)
	}
	if usage.RawResponse == "" {
		t.Fatal("private diagnostic response was lost")
	}
}
