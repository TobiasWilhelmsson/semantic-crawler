package semantic

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLocalReviewRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Host = "localhost:8091"
	return request
}

func TestReviewRouterRejectsUnapprovedHostsBeforeServingTokens(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{URL: "https://example.test/article", SnapshotHTML: "<p>Source</p>"})
	candidate := store.PutCandidate(Candidate{PageVersionID: page.ID, Status: CandidatePendingReview})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	for _, host := range []string{"attacker.example:8091", "localhost.attacker.example:8091", "user@localhost:8091", "localhost:8091/path"} {
		t.Run(host, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/semantic/review/%d", candidate.ID), nil)
			request.Host = host
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("unapproved Host exposed candidate/token: status=%d token=%t", response.Code, strings.Contains(response.Body.String(), "secret"))
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/semantic/review/%d/approve", candidate.ID), strings.NewReader("csrf_token=secret&reviewer=attacker"))
	request.Host = "attacker.example:8091"
	request.Header.Set("Origin", "http://attacker.example:8091")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	stored, _ := store.Candidate(context.Background(), candidate.ID)
	if response.Code != http.StatusForbidden || stored.Status != CandidatePendingReview {
		t.Fatalf("unapproved matching Host/Origin approved candidate: status=%d candidate=%s", response.Code, stored.Status)
	}
}

func TestReviewRouterUsesExplicitApprovedHosts(t *testing.T) {
	store := NewMemoryStore()
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{AllowedHosts: []string{"review.example.test"}})
	for _, test := range []struct {
		host string
		want int
	}{
		{"review.example.test:8091", http.StatusOK},
		{"REVIEW.EXAMPLE.TEST:8091", http.StatusOK},
		{"attacker.example:8091", http.StatusForbidden},
		{"localhost:8091", http.StatusForbidden},
		{"review.example.test.attacker.example:8091", http.StatusForbidden},
	} {
		request := httptest.NewRequest(http.MethodGet, "/semantic", nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("Host %q: got %d, want %d", test.host, response.Code, test.want)
		}
	}
}

func TestReviewRouterAcceptsDefaultLoopbackHosts(t *testing.T) {
	store := NewMemoryStore()
	router := NewReviewRouter(store, NewReviewService(store), nil)
	for _, host := range []string{"localhost:8091", "LOCALHOST:8091", "127.0.0.1:8091", "[::1]:8091"} {
		request := httptest.NewRequest(http.MethodGet, "/semantic", nil)
		request.Host = host
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("loopback Host %q was rejected: %d", host, response.Code)
		}
	}
}

func TestReviewRouterProtectsUIFromFramingAndAllowsItsSnapshotFrame(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{URL: "https://example.test/article", SnapshotHTML: "<p>Source</p>"})
	candidate := store.PutCandidate(Candidate{PageVersionID: page.ID, Status: CandidatePendingReview})
	reportRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(reportRoot, "human-parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportRoot, "human-parity", "human-parity-proof.html"), []byte("<p>Report</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{ReportRoot: reportRoot})
	for _, test := range []struct{ path, ancestors, frameOptions string }{
		{fmt.Sprintf("/semantic/review/%d", candidate.ID), "frame-ancestors 'none'", "DENY"},
		{fmt.Sprintf("/semantic/snapshots/%d", page.ID), "frame-ancestors 'self'", "SAMEORIGIN"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Host = "localhost:8091"
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Security-Policy"), test.ancestors) || response.Header().Get("X-Frame-Options") != test.frameOptions {
				t.Fatalf("unexpected frame protection: status=%d CSP=%q X-Frame-Options=%q", response.Code, response.Header().Get("Content-Security-Policy"), response.Header().Get("X-Frame-Options"))
			}
		})
	}
}
