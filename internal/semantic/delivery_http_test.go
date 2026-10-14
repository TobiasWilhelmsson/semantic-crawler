package semantic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPDeliveryClientSendsApprovedPayload(t *testing.T) {
	var received DeliveryPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()

	client := NewHTTPDeliveryClient(nil)
	status, body, err := client.Send(context.Background(), server.URL, DeliveryPayload{CandidateID: 42, Origin: "https://example.test"})
	if err != nil || status != http.StatusAccepted || body != `{"accepted":true}` || received.CandidateID != 42 {
		t.Fatalf("unexpected delivery: status=%d body=%s received=%#v err=%v", status, body, received, err)
	}
}
