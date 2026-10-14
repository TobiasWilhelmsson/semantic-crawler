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

func TestBudgetProvidersUseJSONModeWithLocalSchemaValidation(t *testing.T) {
	for _, provider := range []string{"deepseek", "qwen", "zai"} {
		t.Run(provider, func(t *testing.T) {
			var schemas []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-key" || r.URL.RawQuery != "" {
					t.Error("provider key was not confined to Bearer authentication")
				}
				if r.URL.Path == "/models" {
					w.Write([]byte(`{"data":[{"id":"budget-model"}]}`))
					return
				}
				var body struct {
					Model          string                 `json:"model"`
					MaxTokens      int                    `json:"max_tokens"`
					Thinking       *struct{ Type string } `json:"thinking"`
					EnableThinking *bool                  `json:"enable_thinking"`
					ResponseFormat struct {
						Type string `json:"type"`
					} `json:"response_format"`
					Messages []struct{ Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if body.Model != "budget-model" || body.MaxTokens != 1024 {
					t.Error("operator model or output-token limit was lost")
				}
				if provider == "qwen" {
					if body.EnableThinking == nil || *body.EnableThinking || body.Thinking != nil {
						t.Error("Qwen must disable thinking with its documented parameter")
					}
				} else if body.Thinking == nil || body.Thinking.Type != "disabled" || body.EnableThinking != nil {
					t.Error("DeepSeek/GLM must use their documented non-thinking parameter")
				}
				if body.ResponseFormat.Type == "json_schema" {
					http.Error(w, "This synthetic budget endpoint supports JSON mode only", 400)
					return
				}
				content := `{"action":"extract","confidence":1,"reason":"Synthetic check"}`
				if body.ResponseFormat.Type != "" {
					if body.ResponseFormat.Type != "json_object" || len(body.Messages) == 0 || !strings.Contains(strings.ToLower(body.Messages[0].Content), "json") {
						t.Error("JSON mode was not explicitly requested and prompted")
					}
					if marker := strings.Index(body.Messages[0].Content, "JSON Schema:\n"); marker >= 0 {
						var schema struct {
							Required   []string       `json:"required"`
							Properties map[string]any `json:"properties"`
						}
						if err := json.Unmarshal([]byte(body.Messages[0].Content[marker+len("JSON Schema:\n"):]), &schema); err != nil || len(schema.Required) != 3 {
							t.Error("JSON-mode prompt lacks a usable complete schema")
						}
						if schema.Properties["page_kind"] != nil {
							schemas = append(schemas, "extraction")
							content = `{"page_kind":"article","blocks":[{"block_id":"b0001","include":true,"role":"header","type":"none","reason":"Source heading"}],"uncertainties":[]}`
						} else {
							schemas = append(schemas, "route")
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
			}))
			defer server.Close()
			t.Setenv("SEMANTIC_LLM_PROVIDER", provider)
			t.Setenv("SEMANTIC_LLM_BASE_URL", server.URL)
			t.Setenv("SEMANTIC_LLM_MODEL", "budget-model")
			t.Setenv("SEMANTIC_LLM_API_KEY", "synthetic-key")
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
			report, err := semantic.RunOpenAICompatibility(context.Background(), semantic.OpenAICompatibilityOptions{Config: config})
			if err != nil || !report.OK {
				t.Fatalf("configured JSON-mode compatibility check: %v", err)
			}
			if len(schemas) != 3 || schemas[0] != "route" || schemas[1] != "extraction" || schemas[2] != "route" {
				t.Fatalf("routing, extraction and configured-mode probe schemas: %v", schemas)
			}
			if report.Checks[3].RequestShape != "chat_completion_json_object_local_schema" || report.Checks[3].Name == "json_schema_chat" {
				t.Fatal("compatibility report falsely claims server-enforced JSON Schema")
			}
		})
	}
}

func TestBudgetProvidersRejectInvalidStructuredContent(t *testing.T) {
	for _, provider := range []string{"deepseek", "qwen", "zai"} {
		for _, content := range []string{`{}`, `{"action":"extract","confidence":1}`, `{"action":"extract","confidence":1,"reason":"ok","extra":true}`, "not JSON", ""} {
			t.Run(provider+"/"+content, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Format struct{ Type string } `json:"response_format"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Format.Type != "json_object" {
						t.Error("request did not use the budget provider's JSON mode")
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}})
				}))
				defer server.Close()
				client := semantic.NewOpenAIClient(semantic.OpenAIConfig{Provider: provider, BaseURL: server.URL, Model: "budget-model"})
				if _, _, err := client.Route(context.Background(), "Synthetic validation"); err == nil {
					t.Fatal("JSON mode bypassed local schema validation")
				}
			})
		}
	}
}

func TestBudgetCompatibilityRejectsJSONOutsideRoutingSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"data":[{"id":"budget-model"}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	for _, provider := range []string{"deepseek", "qwen", "zai"} {
		report, err := semantic.RunOpenAICompatibility(context.Background(), semantic.OpenAICompatibilityOptions{Config: semantic.OpenAIConfig{Provider: provider, BaseURL: server.URL, Model: "budget-model"}})
		if err == nil || report.OK || report.Checks[3].OK || !report.Checks[2].OK {
			t.Fatalf("%s accepted JSON syntax as schema compliance: %#v %v", provider, report.Checks, err)
		}
	}
}
