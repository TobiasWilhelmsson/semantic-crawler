package semantic

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebExportDownloadEnforcesSourceAndDraftSelection(t *testing.T) {
	store, s := newExportStore(t)
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{SourceID: s.Config.SourceID, SourceSlug: s.Slug, SourceName: s.Config.Name})
	for _, tc := range []struct {
		path     string
		code     int
		contains string
	}{{"/semantic/export", 200, "Download"}, {"/semantic/export?download=1&format=jsonl", 200, "document-v1"}, {"/semantic/export?download=1&candidate_id=999", 422, "candidate"}, {"/semantic/export?download=1&format=invalid", 422, "format"}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://localhost"+tc.path, nil)
		router.ServeHTTP(w, r)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.contains) {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://evil.test/semantic/export?download=1", nil)
	router.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("host boundary bypass")
	}
}
