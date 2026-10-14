package semantic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelResponseHasDecodedSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, strings.Repeat("x", 9*1024*1024)) }))
	defer server.Close()
	_, _, err := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "test"}).Route(context.Background(), "source")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("model response was not bounded before JSON parsing: %v", err)
	}
}

func TestDeliveryResponseHasDecodedSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, strings.Repeat("x", 2*1024*1024)) }))
	defer server.Close()
	_, _, err := NewHTTPDeliveryClient(nil).Send(context.Background(), server.URL, DeliveryPayload{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("delivery response was not bounded: %v", err)
	}
}

func TestModelRedirectDoesNotForwardSourcePrompt(t *testing.T) {
	received := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"action\":\"extract\",\"confidence\":1,\"reason\":\"test\"}"}}]}`)
	}))
	defer destination.Close()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer endpoint.Close()
	_, _, err := NewOpenAIClient(OpenAIConfig{BaseURL: endpoint.URL, APIKey: "test-key", Model: "test"}).Route(context.Background(), "private source prompt")
	if received != 0 || err == nil {
		t.Fatalf("redirect forwarded prompt: requests=%d err=%v", received, err)
	}
}
