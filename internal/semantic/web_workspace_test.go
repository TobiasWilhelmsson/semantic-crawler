package semantic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebWorkspaceSourceIsolationAndScopedMutation(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	stores := map[string]*SourceWorkspace{}
	for _, slug := range []string{"a", "b"} {
		_, store := newCrawlRecoveryStore(t)
		config := SourceConfig{SourceID: 2, Name: slug, SeedURL: "https://example.test/", MaxPages: 10, MaxDepth: 2}
		writeTestSourceConfig(t, root, slug, config)
		page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 2, URL: "https://example.test/" + slug, SnapshotHTML: "<p>" + slug + "</p>"})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := store.SaveCandidate(ctx, Candidate{SourceID: 2, PageVersionID: page.ID, URL: page.URL, Status: CandidatePendingReview})
		if err != nil || candidate.ID != 1 {
			t.Fatalf("candidate IDs: %#v %v", candidate, err)
		}
		stores[slug] = &SourceWorkspace{Source: SourceEntry{Slug: slug, Config: config}, DBPath: filepath.Join(t.TempDir(), slug+".db"), Store: store, Reviews: NewReviewService(store)}
	}
	resolver := NewSourceWorkspaces(NewSourceRegistry(root), stores["a"].Source, stores["a"].DBPath, func(_ context.Context, entry SourceEntry, _ string, _ bool) (*SourceWorkspace, error) {
		return stores[entry.Slug], nil
	})
	router := NewReviewRouterWithOptions(stores["a"].Store, stores["a"].Reviews, nil, ReviewRouterOptions{CSRFToken: "token", Workspaces: resolver})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newLocalReviewRequest("GET", "/semantic/sources/b/review/1", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "https://example.test/b") || strings.Contains(response.Body.String(), "https://example.test/a") {
		t.Fatalf("wrong source %d %s", response.Code, response.Body.String())
	}
	post := httptest.NewRecorder()
	request := newLocalReviewRequest("POST", "/semantic/sources/b/review/1/approve", strings.NewReader(url.Values{"csrf_token": {"token"}, "reviewer": {"Tester"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(post, request)
	if post.Code != 303 || post.Header().Get("Location") != "/semantic/sources/b/review" {
		t.Fatalf("source redirect %d %s", post.Code, post.Header().Get("Location"))
	}
	a, _ := stores["a"].Store.Candidate(ctx, 1)
	b, _ := stores["b"].Store.Candidate(ctx, 1)
	if a.Status != CandidatePendingReview || b.Status != CandidateApproved {
		t.Fatalf("cross-source mutation %s/%s", a.Status, b.Status)
	}
	foreign, err := stores["b"].Store.SaveCandidate(ctx, Candidate{SourceID: 99, Status: CandidatePendingReview})
	if err != nil {
		t.Fatal(err)
	}
	hidden := httptest.NewRecorder()
	router.ServeHTTP(hidden, newLocalReviewRequest("GET", fmt.Sprintf("/semantic/sources/b/review/%d", foreign.ID), nil))
	if hidden.Code != 404 {
		t.Fatalf("foreign candidate visible: %d", hidden.Code)
	}
}
func TestWebWorkspaceRejectsMutationBeforeOpeningStore(t *testing.T) {
	root := t.TempDir()
	writeTestSourceConfig(t, root, "b", SourceConfig{SourceID: 2})
	calls := 0
	resolver := NewSourceWorkspaces(NewSourceRegistry(root), SourceEntry{Slug: "a"}, "unused", func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error) {
		calls++
		return nil, ErrWorkspaceEmpty
	})
	store := NewMemoryStore()
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "token", Workspaces: resolver})
	response := httptest.NewRecorder()
	request := newLocalReviewRequest(http.MethodPost, "/semantic/sources/b/review/1/approve", strings.NewReader("csrf_token=wrong"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(response, request)
	if response.Code != 403 || calls != 0 {
		t.Fatalf("unauthorized mutation accessed source storage: %d calls=%d", response.Code, calls)
	}
}

func TestWebWorkspaceFirstScanHasGuidanceWithoutDeadReviewActions(t *testing.T) {
	source := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2, Name: "New source", SeedURL: "https://example.test/"}}
	resolver := NewSourceWorkspaces(NewSourceRegistry(t.TempDir()), source, filepath.Join(t.TempDir(), "missing.db"), func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error) {
		return nil, ErrWorkspaceEmpty
	})
	store := NewMemoryStore()
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{Workspaces: resolver})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, "/semantic/sources/sample", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "Start your first scan to create review candidates and saved instructions.") || strings.Contains(body, ">Review candidates</a>") || strings.Contains(body, ">Saved instructions</a>") {
		t.Fatalf("first-scan guidance: %d %s", response.Code, body)
	}
}
