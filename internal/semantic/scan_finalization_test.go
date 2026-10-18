package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"semantic-crawler/internal/models"
)

type failedFinalizationFixture struct {
	manager *ScanManager
	store   *GormStore
	scan    PreparedScan
	run     ScanRun
	router  http.Handler
	calls   *atomic.Int32
}

func newFailedFinalizationFixture(t *testing.T, cause error) failedFinalizationFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "source.db")
	store, err := OpenReviewStore(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.db.Exec(`CREATE TRIGGER fail_terminal BEFORE UPDATE OF status ON semantic_crawl_runs WHEN NEW.status IN ('completed','failed','cancelled') BEGIN SELECT RAISE(FAIL, 'private sqlite diagnostic'); END`).Error; err != nil {
		t.Fatal(err)
	}
	source := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2, Name: "Sample", SeedURL: "https://example.test/"}}
	workspace := &SourceWorkspace{Source: source, DBPath: path, Store: store, Reviews: NewReviewService(store)}
	scan := PreparedScan{
		Source:   source,
		DBPath:   path,
		Request:  ScanRequest{SourceSlug: "sample", RequestKey: "one", ModelMode: "fixture"},
		Config:   CrawlConfig{SourceID: 2},
		Identity: "identity",
		OpenWorkspace: func(context.Context, bool) (*SourceWorkspace, error) {
			return workspace, nil
		},
	}
	calls := &atomic.Int32{}
	manager := NewScanManager(ctx, func(context.Context, PreparedScan, int64) (CrawlSummary, error) {
		calls.Add(1)
		return CrawlSummary{Discovered: 3, PendingReview: 2, Unchanged: 1}, cause
	})
	t.Cleanup(func() { manager.Shutdown(ctx) })
	run, err := manager.Start(ctx, scan)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, _, active := manager.Active()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	workspaces := NewSourceWorkspaces(NewSourceRegistry(t.TempDir()), source, path, func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error) { return workspace, nil })
	router := NewReviewRouterWithOptions(store, workspace.Reviews, nil, ReviewRouterOptions{CSRFToken: "token", Workspaces: workspaces, Scans: manager})
	return failedFinalizationFixture{manager: manager, store: store, scan: scan, run: run, router: router, calls: calls}
}

func (fixture failedFinalizationFixture) request(method, suffix, token string) *httptest.ResponseRecorder {
	target := fmt.Sprintf("/semantic/sources/sample/scans/%d%s", fixture.run.ID, suffix)
	request := newLocalReviewRequest(method, target, nil)
	if method == http.MethodPost {
		request = newLocalReviewRequest(method, target, strings.NewReader(url.Values{"csrf_token": {token}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response
}

func TestScanFinalizationFailureIsVisibleAndRecoverable(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint("executor_failed=", failed), func(t *testing.T) {
			var cause error
			expectedState := ScanCompleted
			if failed {
				cause = errors.New("synthetic crawl failure")
				expectedState = ScanFailed
			}
			fixture := newFailedFinalizationFixture(t, cause)
			status := fixture.request(http.MethodGet, "/status", "")
			var data struct {
				State string `json:"state"`
				Error string `json:"finalization_error"`
			}
			if err := json.Unmarshal(status.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if status.Code != 200 || data.State != "finalization_failed" || data.Error == "" || strings.Contains(status.Body.String(), "private sqlite diagnostic") {
				t.Errorf("failed finalization is not reported safely: %d %s", status.Code, status.Body.String())
			}
			detail := fixture.request(http.MethodGet, "", "")
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(detail.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			retry := doc.Find("form#retry-finalization")
			if _, hidden := retry.Attr("hidden"); retry.Length() != 1 || hidden || retry.Find(`input[name=csrf_token]`).AttrOr("value", "") != "token" {
				t.Error("failed finalization has no visible CSRF-protected recovery action")
			}
			if strings.Contains(doc.Text(), "External scan:") || doc.Find("#cancel-scan").Length() != 0 || doc.Find("#scan-progress").AttrOr("data-active", "") != "false" {
				t.Error("stopped local scan is still presented as active or external")
			}
			next := fixture.scan
			next.Request.RequestKey = "two"
			if _, err := fixture.manager.Start(context.Background(), next); err == nil {
				t.Fatal("new scan overwrote pending finalization")
			}
			if response := fixture.request(http.MethodPost, "/retry-finalization", "wrong"); response.Code != 403 {
				t.Fatalf("retry accepted invalid CSRF: %d", response.Code)
			}
			if response := fixture.request(http.MethodPost, "/retry-finalization", "token"); response.Code != 503 || strings.Contains(response.Body.String(), "private sqlite diagnostic") {
				t.Fatalf("failed retry not actionable/safe: %d %s", response.Code, response.Body.String())
			}
			if err := fixture.store.db.Exec("DROP TRIGGER fail_terminal").Error; err != nil {
				t.Fatal(err)
			}
			lease, err := AcquireScanLock(fixture.scan.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			response := fixture.request(http.MethodPost, "/retry-finalization", "token")
			lease.Close()
			if response.Code != 409 {
				t.Fatalf("retry ignored another scan owner: %d", response.Code)
			}
			if response := fixture.request(http.MethodPost, "/retry-finalization", "token"); response.Code != 303 {
				t.Fatalf("retry failed after storage recovered: %d %s", response.Code, response.Body.String())
			}
			saved, err := fixture.store.ScanRun(context.Background(), fixture.run.ID)
			if err != nil || saved.State != expectedState || saved.Summary.Discovered != 3 || saved.Summary.PendingReview != 2 || saved.Summary.Unchanged != 1 {
				t.Fatalf("original terminal result not preserved: %#v %v", saved, err)
			}
			if fixture.calls.Load() != 1 {
				t.Fatalf("retry repeated crawl: calls=%d", fixture.calls.Load())
			}
			status = fixture.request(http.MethodGet, "/status", "")
			if strings.Contains(status.Body.String(), "finalization_error") || strings.Contains(status.Body.String(), "finalization_failed") {
				t.Fatalf("resolved failure still exposed: %s", status.Body.String())
			}
			if err := fixture.manager.Shutdown(context.Background()); err != nil {
				t.Fatalf("resolved failure still returned on shutdown: %v", err)
			}
		})
	}
}

func TestScanFinalizationShutdownReportsUnresolvedFailure(t *testing.T) {
	fixture := newFailedFinalizationFixture(t, nil)
	if err := fixture.manager.Shutdown(context.Background()); err == nil || !strings.Contains(err.Error(), "private sqlite diagnostic") {
		t.Fatalf("unresolved finalization lost on shutdown: %v", err)
	}
}

func TestScanFinalizationRetryPreservesAlreadyTerminalRecord(t *testing.T) {
	fixture := newFailedFinalizationFixture(t, nil)
	ctx := context.Background()
	// A different owner can reconcile the abandoned record before this server retries.
	if err := fixture.store.db.Model(&models.SemanticCrawlRun{}).Where("id = ?", fixture.run.ID).Updates(map[string]any{"status": ScanInterrupted, "error": "external recovery retained"}).Error; err != nil {
		t.Fatal(err)
	}
	response := fixture.request(http.MethodPost, "/retry-finalization", "token")
	if response.Code != 303 {
		t.Fatalf("terminal record did not clear pending failure: %d %s", response.Code, response.Body.String())
	}
	var row models.SemanticCrawlRun
	if err := fixture.store.db.First(&row, fixture.run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "interrupted" || row.Error != "external recovery retained" {
		t.Fatalf("already terminal record overwritten: %#v", row)
	}
	if fixture.calls.Load() != 1 {
		t.Fatalf("retry repeated crawl: calls=%d", fixture.calls.Load())
	}
	if err := fixture.manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestScanFinalizationPendingViewsDoNotClaimActive(t *testing.T) {
	fixture := newFailedFinalizationFixture(t, nil)
	for _, target := range []string{"/semantic/sources/sample", "/semantic/sources/sample/scans"} {
		response := httptest.NewRecorder()
		fixture.router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, target, nil))
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(response.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		content := doc.Find(".app-main").Text()
		if response.Code != 200 || !strings.Contains(content, "Result not saved") || strings.Contains(content, "running") || strings.Contains(content, "In progress") {
			t.Errorf("pending finalization is hidden at %s: status=%d text=%s", target, response.Code, content)
		}
	}
}

func TestScanFinalizationRetryRejectsDifferentRun(t *testing.T) {
	fixture := newFailedFinalizationFixture(t, nil)
	for _, target := range []struct {
		slug string
		id   int64
	}{{"other-source", fixture.run.ID}, {"sample", fixture.run.ID + 1}} {
		if err := fixture.manager.RetryFinalization(context.Background(), target.slug, target.id); !errors.Is(err, ErrScanFinalizationNotPending) {
			t.Fatalf("retry accepted another source/run: %s/%d: %v", target.slug, target.id, err)
		}
	}
	if !fixture.manager.PendingFinalization("sample", fixture.run.ID) || fixture.calls.Load() != 1 {
		t.Fatal("invalid retry changed pending run")
	}
}
