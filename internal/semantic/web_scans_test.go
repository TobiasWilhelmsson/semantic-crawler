package semantic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebScanStartStatusCancelAndReportRemoval(t *testing.T) {
	_, store := newCrawlRecoveryStore(t)
	source := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2, Name: "Sample", SeedURL: "https://example.test/", MaxPages: 10, MaxDepth: 2, FixtureDirectory: "fixtures"}}
	path := filepath.Join(t.TempDir(), "sample.db")
	workspace := &SourceWorkspace{Source: source, DBPath: path, Store: store, Reviews: NewReviewService(store)}
	workspaces := NewSourceWorkspaces(NewSourceRegistry(t.TempDir()), source, path, func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error) { return workspace, nil })
	manager := NewScanManager(context.Background(), func(ctx context.Context, _ PreparedScan, _ int64) (CrawlSummary, error) {
		<-ctx.Done()
		return CrawlSummary{}, ctx.Err()
	})
	defer manager.Shutdown(context.Background())
	router := NewReviewRouterWithOptions(store, workspace.Reviews, nil, ReviewRouterOptions{CSRFToken: "token", Workspaces: workspaces, Scans: manager, Prepare: func(ctx context.Context, request ScanRequest) (PreparedScan, error) {
		return PreparedScan{Source: source, DBPath: path, Request: request, Config: CrawlConfig{SourceID: 2}, Identity: "identity", OpenWorkspace: func(context.Context, bool) (*SourceWorkspace, error) { return workspace, nil }}, nil
	}})
	get := httptest.NewRecorder()
	router.ServeHTTP(get, newLocalReviewRequest("GET", "/semantic/sources/sample/scans/new", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), "Start scan") {
		t.Fatalf("scan form: %d %s", get.Code, get.Body.String())
	}
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, newLocalReviewRequest("POST", "/semantic/sources/sample/scans", strings.NewReader("csrf_token=wrong")))
	if bad.Code != 403 {
		t.Fatalf("CSRF %d", bad.Code)
	}
	form := url.Values{"csrf_token": {"token"}, "request_key": {"one"}, "model_mode": {"fixture"}, "max_pages": {"10"}, "max_depth": {"2"}}
	post := httptest.NewRecorder()
	request := newLocalReviewRequest("POST", "/semantic/sources/sample/scans", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(post, request)
	if post.Code != 303 {
		t.Fatalf("start: %d %s", post.Code, post.Body.String())
	}
	location := post.Header().Get("Location")
	status := httptest.NewRecorder()
	router.ServeHTTP(status, newLocalReviewRequest("GET", location+"/status", nil))
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"run_id":1`) {
		t.Fatalf("status: %d %s", status.Code, status.Body.String())
	}
	cancel := httptest.NewRecorder()
	request = newLocalReviewRequest(http.MethodPost, location+"/cancel", strings.NewReader("csrf_token=token"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(cancel, request)
	if cancel.Code != 303 {
		t.Fatalf("cancel: %d %s", cancel.Code, cancel.Body.String())
	}
	for _, route := range []string{"/semantic/reports", "/semantic/reports/benchmark-html"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newLocalReviewRequest("GET", route, nil))
		if response.Code != 404 {
			t.Fatalf("report route %s still available: %d", route, response.Code)
		}
	}
}
