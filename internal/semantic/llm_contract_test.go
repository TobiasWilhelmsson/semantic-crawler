package semantic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func completionTestEnvelope(content any, finishReason any) map[string]any {
	return map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": finishReason}},
		"usage":   map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
	}
}

func completionTestServer(t *testing.T, envelope any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestOpenAIClientRejectsMissingNullAndInvalidSchemaFields(t *testing.T) {
	cases := []struct {
		name       string
		extraction bool
		content    string
	}{
		{"extraction_null", true, `null`},
		{"extraction_empty_object", true, `{}`},
		{"missing_page_kind", true, `{"blocks":[],"uncertainties":[]}`},
		{"invalid_page_kind", true, `{"page_kind":"invented","blocks":[],"uncertainties":[]}`},
		{"null_blocks", true, `{"page_kind":"article","blocks":null,"uncertainties":[]}`},
		{"missing_uncertainties", true, `{"page_kind":"article","blocks":[]}`},
		{"null_uncertainties", true, `{"page_kind":"article","blocks":[],"uncertainties":null}`},
		{"wrong_uncertainty_type", true, `{"page_kind":"article","blocks":[],"uncertainties":[null]}`},
		{"missing_include", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","role":"content","type":"none","reason":"ok"}],"uncertainties":[]}`},
		{"obsolete_parse_mode", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","include":true,"parse_mode":true,"role":"content","type":"none","reason":"ok"}],"uncertainties":[]}`},
		{"missing_reason", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","include":true,"role":"content","type":"none"}],"uncertainties":[]}`},
		{"invalid_excluded_role", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","include":false,"role":"invented","type":"none","reason":"ok"}],"uncertainties":[]}`},
		{"null_decision_bools", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","include":null,"role":"content","type":"none","reason":"ok"}],"uncertainties":[]}`},
		{"wrong_include_type", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","include":"true","role":"content","type":"none","reason":"ok"}],"uncertainties":[]}`},
		{"extra_extraction_property", true, `{"page_kind":"article","blocks":[],"uncertainties":[],"rewritten_content":"invented"}`},
		{"extra_decision_property", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","include":true,"role":"content","type":"none","reason":"ok","text":"invented"}],"uncertainties":[]}`},
		{"overlong_decision_reason", true, `{"page_kind":"article","blocks":[{"block_id":"b0001","include":true,"role":"content","type":"none","reason":"` + strings.Repeat("x", 121) + `"}],"uncertainties":[]}`},
		{"missing_route_fields", false, `{"action":"extract"}`},
		{"null_route_fields", false, `{"action":"extract","confidence":null,"reason":null}`},
		{"extra_route_property", false, `{"action":"ignore","confidence":1,"reason":"ok","invented":true}`},
		{"wrong_route_field_type", false, `{"action":"ignore","confidence":"1","reason":"ok"}`},
		{"confidence_above_maximum", false, `{"action":"extract","confidence":1.1,"reason":"ok"}`},
		{"confidence_below_minimum", false, `{"action":"extract","confidence":-0.1,"reason":"ok"}`},
		{"overlong_route_reason", false, `{"action":"extract","confidence":1,"reason":"` + strings.Repeat("x", 161) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := completionTestServer(t, completionTestEnvelope(tc.content, "stop"))
			client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "test"})
			var usage ModelUsage
			var err error
			if tc.extraction {
				_, usage, err = client.Extract(context.Background(), "extract", []CandidateBlock{{ID: "b0001"}})
			} else {
				_, usage, err = client.Route(context.Background(), "route")
			}
			if err == nil {
				t.Fatal("schema-violating content was accepted")
			}
			if usage.InputTokens != 7 || usage.OutputTokens != 3 || usage.TotalTokens != 10 || usage.RawResponse == "" || usage.Attempts != 1 {
				t.Fatalf("validation failure must retain model diagnostics: %#v", usage)
			}
			if tc.extraction && !shouldRetryExtractionValidationError(err) {
				t.Fatalf("schema failure must retain validation-retry classification: %v", err)
			}
		})
	}
}

func TestOpenAIClientPreservesEmptySelectionsAndExplicitFalseFields(t *testing.T) {
	for _, content := range []string{
		`{"page_kind":"article","blocks":[],"uncertainties":[]}`,
		`{"page_kind":"article","blocks":[{"block_id":"b0001","include":false,"role":"content","type":"none","reason":"` + strings.Repeat("å", 120) + `"}],"uncertainties":[]}`,
	} {
		server := completionTestServer(t, completionTestEnvelope(content, "stop"))
		client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "test"})
		response, _, err := client.Extract(context.Background(), "extract", []CandidateBlock{{ID: "b0001"}})
		if err != nil {
			t.Fatalf("valid extraction rejected: %v", err)
		}
		if response.Blocks == nil || response.Uncertainties == nil || response.PageKind != "article" {
			t.Fatalf("valid empty arrays and page kind must be retained: %#v", response)
		}
		if len(response.Blocks) > 0 && (response.Blocks[0].Include || !response.Blocks[0].ParseMode) {
			t.Fatalf("include=false must be retained and rendering must be application-controlled: %#v", response.Blocks[0])
		}
	}
}

func TestOpenAIClientRejectsIncompleteAndAmbiguousCompletions(t *testing.T) {
	valid := `{"action":"extract","confidence":1,"reason":"ok"}`
	cases := []struct {
		name     string
		envelope any
	}{
		{"length", completionTestEnvelope(valid, "length")},
		{"content_filter", completionTestEnvelope(valid, "content_filter")},
		{"tool_calls", completionTestEnvelope(valid, "tool_calls")},
		{"unknown_finish_reason", completionTestEnvelope(valid, "invented")},
		{"empty_finish_reason", completionTestEnvelope(valid, "")},
		{"null_finish_reason", completionTestEnvelope(valid, nil)},
		{"numeric_finish_reason", completionTestEnvelope(valid, 3)},
		{"multiple_choices", map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": valid}, "finish_reason": "stop"}, map[string]any{"message": map[string]any{"content": "malformed"}, "finish_reason": "length"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := completionTestServer(t, tc.envelope)
			client := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "test"})
			_, _, err := client.Route(context.Background(), "route")
			if err == nil {
				t.Fatal("incomplete or ambiguous completion accepted")
			}
			if !shouldRetryExtractionValidationError(err) {
				t.Fatalf("completion failure must retain validation-retry classification: %v", err)
			}
		})
	}
}
