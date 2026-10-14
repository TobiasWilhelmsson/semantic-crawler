package semantic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunOpenAICompatibilityWritesLayeredReport(t *testing.T) {
	var chatRequests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"zai-org/Model-4.7-FP8"}]}`))
		case "/chat/completions":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			chatRequests = append(chatRequests, request)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"choices":[{"message":{"content":"{\"action\":\"extract\",\"confidence\":0.99,\"reason\":\"ok\"}"}}],
				"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reportDirectory := t.TempDir()
	report, err := RunOpenAICompatibility(context.Background(), OpenAICompatibilityOptions{
		Config: OpenAIConfig{
			BaseURL: server.URL,
			APIKey:  "secret",
			Model:   "zai-org/Model-4.7-FP8",
			Timeout: time.Minute,
		},
		ReportDirectory: reportDirectory,
	})
	if err != nil {
		t.Fatalf("run compatibility: %v", err)
	}
	if !report.OK || report.RequestTimeout != "1m0s" || report.Model != "zai-org/Model-4.7-FP8" {
		t.Fatalf("unexpected compatibility report: %#v", report)
	}
	if len(report.Checks) != 4 {
		t.Fatalf("expected four checks, got %#v", report.Checks)
	}
	for _, check := range report.Checks {
		if !check.OK || check.LatencyMS < 0 || check.StatusCode != http.StatusOK {
			t.Fatalf("check should pass with status and latency: %#v", check)
		}
	}
	if len(chatRequests) != 3 {
		t.Fatalf("expected simple chat, json_object chat, and json_schema chat, got %d", len(chatRequests))
	}
	if _, ok := chatRequests[0]["response_format"]; ok {
		t.Fatalf("simple chat check should not send response_format: %#v", chatRequests[0])
	}
	if chatRequests[1]["response_format"] == nil {
		t.Fatalf("json object check should send response_format: %#v", chatRequests[1])
	}
	if chatRequests[2]["response_format"] == nil {
		t.Fatalf("json schema check should send response_format: %#v", chatRequests[2])
	}
	content, err := os.ReadFile(filepath.Join(reportDirectory, "model-compatibility.json"))
	if err != nil {
		t.Fatalf("compatibility report was not written: %v", err)
	}
	if strings.Contains(string(content), "secret") || !strings.Contains(string(content), `"ok": true`) || !strings.Contains(string(content), "json_schema_chat") {
		t.Fatalf("report should be written without secrets and include checks: %s", content)
	}
}

func TestRunOpenAICompatibilityRecordsPartialFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		http.Error(w, "schema mode unsupported", http.StatusBadRequest)
	}))
	defer server.Close()

	reportDirectory := t.TempDir()
	report, err := RunOpenAICompatibility(context.Background(), OpenAICompatibilityOptions{
		Config:          OpenAIConfig{BaseURL: server.URL, Model: "openai", Timeout: time.Minute},
		ReportDirectory: reportDirectory,
	})
	if err == nil {
		t.Fatal("expected compatibility failure")
	}
	if report.OK || len(report.Checks) != 4 {
		t.Fatalf("partial failure should be recorded: %#v", report)
	}
	if !report.Checks[0].OK || report.Checks[1].OK || report.Checks[2].OK || report.Checks[3].OK {
		t.Fatalf("expected models to pass and chat checks to fail: %#v", report.Checks)
	}
	content, readErr := os.ReadFile(filepath.Join(reportDirectory, "model-compatibility.json"))
	if readErr != nil {
		t.Fatalf("failure report was not written: %v", readErr)
	}
	if !strings.Contains(string(content), "schema mode unsupported") || !strings.Contains(string(content), `"ok": false`) {
		t.Fatalf("failure report missing evidence: %s", content)
	}
}

func TestRunOpenAICompatibilityRequiresAssistantContentForChatChecks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"openai"}]}`))
		case "/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"choices":[{"message":{"content":null,"reasoning":"thinking but no answer"},"finish_reason":"length"}],
				"usage":{"prompt_tokens":11,"completion_tokens":32,"total_tokens":43}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := RunOpenAICompatibility(context.Background(), OpenAICompatibilityOptions{
		Config: OpenAIConfig{BaseURL: server.URL, Model: "openai", Timeout: time.Minute},
	})
	if err == nil {
		t.Fatal("expected content-null chat responses to fail compatibility")
	}
	if report.OK {
		t.Fatalf("compatibility should fail when chat content is missing: %#v", report)
	}
	for _, check := range report.Checks {
		if check.Name == "models" {
			continue
		}
		if check.OK || check.ContentPresent || !check.ReasoningPresent || check.FinishReason != "length" || !strings.Contains(check.Error, "assistant content was empty") {
			t.Fatalf("chat checks should report content-null failure: %#v", check)
		}
	}
}

func TestRunOpenAICompatibilityValidatesStructuredContent(t *testing.T) {
	for _, content := range []string{"ok", "null", "[]", "{}", `{"action":"invented","confidence":2,"reason":"bad"}`, `{"action":"extract"`, `{"action":"extract","confidence":1,"reason":"ok"}{}`} {
		t.Run(content, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					_, _ = w.Write([]byte(`{"data":[]}`))
					return
				}
				_ = json.NewEncoder(w).Encode(completionTestEnvelope(content, "stop"))
			}))
			defer server.Close()
			report, err := RunOpenAICompatibility(context.Background(), OpenAICompatibilityOptions{Config: OpenAIConfig{BaseURL: server.URL, Model: "test"}})
			if err == nil || report.OK || report.Checks[3].OK || !report.Checks[1].OK || !report.Checks[3].ContentPresent {
				t.Fatalf("invalid schema content must fail schema check and retain diagnostics: %#v err=%v", report, err)
			}
			if content == "ok" || content == "null" || content == "[]" || content == `{"action":"extract","confidence":1,"reason":"ok"}{}` || content == `{"action":"extract"` {
				if report.Checks[2].OK {
					t.Fatalf("JSON-object mode accepted a non-object document: %#v", report.Checks[2])
				}
			}
		})
	}
}

func TestRunOpenAICompatibilityRejectsIncompleteAndAmbiguousCompletions(t *testing.T) {
	for _, finishReason := range []string{"length", "content_filter", "tool_calls", ""} {
		t.Run(finishReason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					_, _ = w.Write([]byte(`{"data":[]}`))
					return
				}
				_ = json.NewEncoder(w).Encode(completionTestEnvelope(`{"action":"extract","confidence":1,"reason":"ok"}`, finishReason))
			}))
			defer server.Close()
			report, err := RunOpenAICompatibility(context.Background(), OpenAICompatibilityOptions{Config: OpenAIConfig{BaseURL: server.URL, Model: "test"}})
			if err == nil || report.OK || report.Checks[3].OK || !report.Checks[3].ContentPresent || report.Checks[3].FinishReason != finishReason {
				t.Fatalf("incomplete completion must fail while retaining diagnostics: %#v err=%v", report, err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"},{"message":{"content":"different"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	report, err := RunOpenAICompatibility(context.Background(), OpenAICompatibilityOptions{Config: OpenAIConfig{BaseURL: server.URL, Model: "test"}})
	if err == nil || report.OK || report.Checks[1].OK {
		t.Fatalf("multiple choices must fail compatibility: %#v err=%v", report, err)
	}
}

func TestRunOpenAICompatibilityValidatesModelsList(t *testing.T) {
	for _, content := range []string{`<html>not models</html>`, `null`, `{}`, `{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{}]}`, `{"data":[{"id":3}]}`} {
		t.Run(content, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					_, _ = w.Write([]byte(content))
					return
				}
				_ = json.NewEncoder(w).Encode(completionTestEnvelope(`{"action":"extract","confidence":1,"reason":"ok"}`, "stop"))
			}))
			defer server.Close()
			report, err := RunOpenAICompatibility(context.Background(), OpenAICompatibilityOptions{Config: OpenAIConfig{BaseURL: server.URL, Model: "test"}})
			if err == nil || report.OK || report.Checks[0].OK || !report.Checks[3].OK {
				t.Fatalf("invalid models list must fail only models check: %#v err=%v", report, err)
			}
		})
	}
}
