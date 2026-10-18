package semantic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestSourceDetailSurfacesStorageErrors(t *testing.T) {
	for _, failure := range []string{"invalid database", "candidate query", "scan history query"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			path := filepath.Join(root, "source.db")
			if failure == "invalid database" {
				if err := os.WriteFile(path, []byte("existing but broken database"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				store, err := OpenReviewStore(ctx, path, true)
				if err != nil {
					t.Fatal(err)
				}
				table := "semantic_candidates"
				if failure == "scan history query" {
					table = "semantic_crawl_runs"
				}
				err = store.db.Exec("DROP TABLE " + table).Error
				store.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			source := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2, Name: "Existing sample", SeedURL: "https://example.test/", Scope: "Retain source instructions"}}
			workspaces := NewSourceWorkspaces(NewSourceRegistry(root), source, path, func(ctx context.Context, source SourceEntry, path string, create bool) (*SourceWorkspace, error) {
				store, err := OpenReviewStore(ctx, path, create)
				if err != nil {
					return nil, err
				}
				return &SourceWorkspace{Source: source, DBPath: path, Store: store, Reviews: NewReviewService(store)}, nil
			})
			defer workspaces.Close()
			rootStore := NewMemoryStore()
			router := NewReviewRouterWithOptions(rootStore, NewReviewService(rootStore), nil, ReviewRouterOptions{Workspaces: workspaces, SourceID: 2})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, "/semantic/sources/sample", nil))
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(response.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusInternalServerError || doc.Find("#workspace-error[role=alert]").Text() == "" {
				t.Errorf("storage failure has no visible error: status=%d", response.Code)
			}
			if doc.Find(".metric").Length() != 0 || strings.Contains(doc.Text(), "first scan") {
				t.Errorf("unreadable source presented as empty: %s", doc.Find(".app-main").Text())
			}
			for _, retained := range []string{source.Config.Name, source.Config.SeedURL, source.Config.Scope, path} {
				if !strings.Contains(doc.Text(), retained) {
					t.Errorf("source configuration missing %q", retained)
				}
			}
		})
	}
}
