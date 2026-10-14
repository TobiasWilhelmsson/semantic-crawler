package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"semantic-crawler/internal/semantic"
)

func TestProviderPresetsSupplyEndpointsWithOperatorModelAndKey(t *testing.T) {
	for _, tc := range []struct{ provider, endpoint string }{
		{"openai", "https://api.openai.com/v1"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta/openai"},
		{"deepseek", "https://api.deepseek.com"},
		{"qwen", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
		{"zai", "https://api.z.ai/api/paas/v4"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			t.Setenv("SEMANTIC_LLM_PROVIDER", tc.provider)
			t.Setenv("SEMANTIC_LLM_BASE_URL", "")
			t.Setenv("SEMANTIC_LLM_MODEL", "operator-selected-model")
			t.Setenv("SEMANTIC_LLM_API_KEY", "operator-key")
			config, err := openAIConfigFromEnv(time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if config.BaseURL != tc.endpoint || config.Model != "operator-selected-model" || config.APIKey != "operator-key" {
				t.Fatal("preset replaced the operator's model/key or selected the wrong endpoint")
			}
		})
	}
}

func TestProviderRejectsUnknownPreset(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_BASE_URL", "http://127.0.0.1:1234/v1")
	t.Setenv("SEMANTIC_LLM_MODEL", "operator-model")
	t.Setenv("SEMANTIC_LLM_API_KEY", "synthetic-key")
	t.Setenv("SEMANTIC_LLM_PROVIDER", "misspelled-provider")
	if _, err := openAIConfigFromEnv(time.Second); err == nil || !strings.Contains(err.Error(), "SEMANTIC_LLM_PROVIDER") {
		t.Fatalf("unknown provider must fail before requests: %v", err)
	}
}

func TestProviderRejectsInvalidKeys(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_BASE_URL", "http://127.0.0.1:1234/v1")
	t.Setenv("SEMANTIC_LLM_MODEL", "operator-model")
	t.Setenv("SEMANTIC_LLM_PROVIDER", "custom")
	for _, key := range []string{"", "   ", "private\nkey", "private\tkey"} {
		t.Setenv("SEMANTIC_LLM_API_KEY", key)
		if _, err := openAIConfigFromEnv(time.Second); err == nil || !strings.Contains(err.Error(), "SEMANTIC_LLM_API_KEY") || strings.Contains(err.Error(), "private") {
			t.Fatal("missing/invalid API key must fail without exposing its value")
		}
	}
}

func TestProviderRequestsUseConfiguredKeyAndCompatibleParameters(t *testing.T) {
	for _, provider := range []string{"openai", "gemini", "custom", ""} {
		t.Run("provider-"+provider, func(t *testing.T) {
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-provider-key" || r.URL.RawQuery != "" {
					t.Error("API key was missing from the header or was placed in a query")
				}
				if r.URL.Path == "/custom/v1/models" {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"data":[{"id":"operator-model"}]}`))
					return
				}
				if r.URL.Path != "/custom/v1/chat/completions" {
					t.Errorf("explicit endpoint path was replaced: %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				requests++
				if body["model"] != "operator-model" || body["api_key"] != nil {
					t.Error("model changed or API key was sent in the request body")
				}
				if provider == "openai" {
					if body["max_completion_tokens"] != float64(1024) || body["max_tokens"] != nil || body["temperature"] != nil {
						t.Error("OpenAI request must use completion tokens and omit fixed temperature")
					}
				} else if body["max_tokens"] != float64(1024) || body["max_completion_tokens"] != nil || body["temperature"] != float64(0) {
					t.Error("Gemini/custom request lost compatible token/temperature settings")
				}
				content := `{"action":"extract","confidence":1,"reason":"Synthetic protocol check"}`
				if format, ok := body["response_format"].(map[string]any); ok && format["type"] == "json_schema" {
					schema := format["json_schema"].(map[string]any)
					if schema["strict"] != true {
						t.Error("strict schema was lost")
					}
					if schema["name"] == "semantic_extraction" {
						content = `{"page_kind":"article","blocks":[{"block_id":"b0001","include":true,"role":"header","type":"none","reason":"Source heading"}],"uncertainties":[]}`
					}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
			}))
			defer server.Close()
			t.Setenv("SEMANTIC_LLM_PROVIDER", provider)
			t.Setenv("SEMANTIC_LLM_BASE_URL", server.URL+"/custom/v1/")
			t.Setenv("SEMANTIC_LLM_MODEL", "operator-model")
			t.Setenv("SEMANTIC_LLM_API_KEY", "synthetic-provider-key")
			config, err := openAIConfigFromEnv(time.Second)
			if err != nil {
				t.Fatal(err)
			}
			config.MaxTokens = 1024
			client := semantic.NewOpenAIClient(config)
			if _, _, err := client.Route(context.Background(), "Synthetic routing"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := client.Extract(context.Background(), "Synthetic extraction", []semantic.CandidateBlock{{ID: "b0001"}}); err != nil {
				t.Fatal(err)
			}
			if report, err := semantic.RunOpenAICompatibility(context.Background(), semantic.OpenAICompatibilityOptions{Config: config}); err != nil || !report.OK {
				t.Fatalf("compatibility probe: %v", err)
			}
			if requests != 5 {
				t.Fatalf("routing, extraction, and compatibility requests: %d", requests)
			}
		})
	}
}
