package semantic

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebWorkspaceInvalidSourceCannotBypassIsolation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dataRoot := t.TempDir()
	store, openErr := OpenReviewStore(ctx, filepath.Join(dataRoot, "semantic-invalid.db"), true)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer store.Close()
	page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 99, URL: "https://foreign.test/", SnapshotHTML: "<p>foreign source</p>"})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.SaveCandidate(ctx, Candidate{SourceID: 99, PageVersionID: page.ID, URL: page.URL, Status: CandidatePendingReview})
	if err != nil {
		t.Fatal(err)
	}
	writeTestSourceConfig(t, root, "invalid", SourceConfig{Name: "Invalid source"})
	startup := SourceEntry{Slug: "startup", Config: SourceConfig{SourceID: 1}}
	resolver := NewSourceWorkspaces(NewSourceRegistry(root), startup, filepath.Join(t.TempDir(), "startup.db"), func(ctx context.Context, entry SourceEntry, path string, create bool) (*SourceWorkspace, error) {
		opened, err := OpenReviewStore(ctx, path, create)
		if err != nil {
			return nil, err
		}
		return &SourceWorkspace{Source: entry, DBPath: path, Store: opened, Reviews: NewReviewService(opened)}, nil
	}, dataRoot)
	defer resolver.Close()
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "token", Workspaces: resolver, SourceID: 1})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newLocalReviewRequest("GET", fmt.Sprintf("/semantic/sources/invalid/review/%d", candidate.ID), nil))
	t.Logf("Invalid source detail status = %d; foreign URL displayed = %v", response.Code, strings.Contains(response.Body.String(), page.URL))
	post := httptest.NewRecorder()
	request := newLocalReviewRequest("POST", fmt.Sprintf("/semantic/sources/invalid/review/%d/approve", candidate.ID), strings.NewReader(url.Values{"csrf_token": {"token"}, "reviewer": {"Tester"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(post, request)
	got, _ := store.Candidate(ctx, candidate.ID)
	if response.Code == 200 || got.Status == CandidateApproved {
		t.Fatalf("invalid source exposed foreign candidate, GET=%d POST=%d final status=%s", response.Code, post.Code, got.Status)
	}
}
func TestWebScanAgainKeepsMode(t *testing.T) {
	ctx := context.Background()
	_, store := newCrawlRecoveryStore(t)
	source := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2, Name: "Sample", FixtureDirectory: "fixtures", MaxPages: 10, MaxDepth: 2}}
	path := filepath.Join(t.TempDir(), "sample.db")
	workspace := &SourceWorkspace{Source: source, DBPath: path, Store: store, Reviews: NewReviewService(store)}
	resolver := NewSourceWorkspaces(NewSourceRegistry(t.TempDir()), source, path, func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error) { return workspace, nil })
	run, _, err := store.ReserveScan(ctx, CrawlConfig{SourceID: 2, MaxPages: 5, MaxDepth: 1}, ScanRequest{SourceSlug: "sample", RequestKey: "one", ModelMode: "openai"}, "identity")
	if err != nil {
		t.Fatal(err)
	}
	router := NewReviewRouterWithOptions(store, workspace.Reviews, nil, ReviewRouterOptions{CSRFToken: "token", Workspaces: resolver, Prepare: func(context.Context, ScanRequest) (PreparedScan, error) { return PreparedScan{}, nil }})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newLocalReviewRequest("GET", fmt.Sprintf("/semantic/sources/sample/scans/new?from=%d", run.ID), nil))
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	if !strings.Contains(response.Body.String(), `value="openai" selected`) {
		t.Fatal("Scan again for an openai run selected fixture instead")
	}
}

func TestSourceWorkspaceRejectsInvalidIdentitiesBeforeOpening(t *testing.T) {
	for _, id := range []int64{0, -1} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			root := t.TempDir()
			source := SourceEntry{Slug: "startup", Config: SourceConfig{SourceID: id}}
			writeTestSourceConfig(t, root, "registered", SourceConfig{SourceID: id})
			calls := 0
			workspaces := NewSourceWorkspaces(NewSourceRegistry(root), source, filepath.Join(root, "startup.db"), func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error) {
				calls++
				return nil, errors.New("should not open")
			})
			for _, slug := range []string{"startup", "registered"} {
				if _, err := workspaces.Resolve(context.Background(), slug, true); err == nil {
					t.Fatalf("opened invalid %s identity", slug)
				}
			}
			statuses, err := workspaces.List()
			if err != nil || len(statuses) != 2 {
				t.Fatalf("statuses: %v %v", statuses, err)
			}
			for _, status := range statuses {
				if status.Source != nil || status.Error == "" {
					t.Fatalf("invalid source advertised as usable: %#v", status)
				}
			}
			if calls != 0 {
				t.Fatalf("opened store %d times", calls)
			}
		})
	}
}

func TestWebScanAgainUnavailableModeRequiresExplicitChoice(t *testing.T) {
	form := scanForm{Mode: "fixture", Ready: true, Modes: []scanMode{{Value: "fixture", Label: "Offline sample", Ready: true}, {Value: "openai", Label: "Configured model", Reason: "Missing model configuration"}}}
	restoreScanMode(&form, ScanRun{ModelMode: "openai"})
	if form.Mode != "openai" || form.Error == "" || !form.Ready {
		t.Fatalf("mode silently changed or available alternative cannot be chosen: %#v", form)
	}
	restoreScanMode(&form, ScanRun{})
	if form.Mode != "" || form.Error == "" {
		t.Fatalf("unknown legacy mode silently substituted: %#v", form)
	}
	restoreScanMode(&form, ScanRun{Config: CrawlConfig{Model: "deterministic-fixture-model"}})
	if form.Mode != "fixture" {
		t.Fatalf("legacy fixture mode: %s", form.Mode)
	}
}
