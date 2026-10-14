package semantic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPFetcherRetriesRateLimitedResponses(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer server.Close()

	result, err := (&HTTPFetcher{MaxRetries: 1, RetryDelay: time.Millisecond}).Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if attempts != 2 || result.StatusCode != http.StatusOK || result.HTML != "<html>ok</html>" {
		t.Fatalf("expected retry to return final success, attempts=%d result=%#v", attempts, result)
	}
}
