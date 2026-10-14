package semantic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceWorkspaceMissingDatabaseAndInvalidSibling(t *testing.T) {
	root := t.TempDir()
	entry := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2, Name: "Sample"}}
	calls := 0
	resolver := NewSourceWorkspaces(NewSourceRegistry(root), entry, filepath.Join(root, "custom.db"), func(ctx context.Context, source SourceEntry, path string, create bool) (*SourceWorkspace, error) {
		calls++
		if !create {
			return nil, ErrWorkspaceEmpty
		}
		return &SourceWorkspace{Source: source, DBPath: path}, nil
	})
	if _, err := resolver.Resolve(context.Background(), "sample", false); !errors.Is(err, ErrWorkspaceEmpty) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "custom.db")); !os.IsNotExist(err) {
		t.Fatalf("browsing created storage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	statuses, err := NewSourceRegistry(root).ListStatus()
	if err != nil || len(statuses) != 1 || statuses[0].Error == "" {
		t.Fatalf("invalid source: %#v %v", statuses, err)
	}
	_, path, err := resolver.Lookup("sample")
	if err != nil || path != filepath.Join(root, "custom.db") {
		t.Fatalf("startup config: %s %v", path, err)
	}
	if calls != 1 {
		t.Fatalf("unexpected opening calls %d", calls)
	}
}
func TestPrepareScanRejectsSettingsBeforeModelOrStorage(t *testing.T) {
	entry := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2, MaxPages: 10, MaxDepth: 2}}
	resolver := NewSourceWorkspaces(NewSourceRegistry(t.TempDir()), entry, "unused.db", nil)
	calls := 0
	models := func(SourceEntry, string) (SemanticModel, string, error) { calls++; return nil, "fixture", nil }
	for _, request := range []ScanRequest{{SourceSlug: "sample", ModelMode: "fixture", MaxPages: 1001}, {SourceSlug: "sample", ModelMode: "openai", MaxPages: 1, MaxDepth: 11}, {SourceSlug: "sample", ModelMode: "bad", MaxPages: 1}} {
		if _, err := PrepareScan(context.Background(), resolver, models, request, FetchSettings{}); err == nil {
			t.Fatalf("accepted invalid request %#v", request)
		}
	}
	if calls != 0 {
		t.Fatalf("model resolver called %d times", calls)
	}
}

func TestWorkspaceRetryUsesCandidateRunMode(t *testing.T) {
	_, store := newCrawlRecoveryStore(t)
	ctx := context.Background()
	workspace := &SourceWorkspace{Source: SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2}}, Store: store}
	modes := []string{}
	resolver := func(_ SourceEntry, mode string) (SemanticModel, string, error) {
		modes = append(modes, mode)
		name := "deterministic-fixture-model"
		if mode == "openai" {
			name = "provider"
		}
		return &recordingModel{}, name, nil
	}
	retry := NewWorkspaceRetrier(workspace, resolver, "fixture")
	for _, mode := range []string{"fixture", "openai"} {
		name := "deterministic-fixture-model"
		if mode == "openai" {
			name = "provider"
		}
		run, err := store.StartCrawlRun(ctx, CrawlConfig{SourceID: 2, Model: name, Scope: "Keep headings"})
		if err != nil {
			t.Fatal(err)
		}
		page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 2, RunID: run, URL: "https://example.test/", RawHTML: "<main><h1>Source</h1></main>"})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := store.SaveCandidate(ctx, Candidate{SourceID: 2, PageVersionID: page.ID, Status: CandidateQueued, Model: name})
		if err != nil {
			t.Fatal(err)
		}
		if err = retry.Retry(ctx, candidate.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(modes) != 2 || modes[0] != "fixture" || modes[1] != "openai" {
		t.Fatalf("wrong retry modes: %v", modes)
	}
}

func TestSourceWorkspaceRecoveryKeepsActiveOwnerAndClosesAbandonedPage(t *testing.T) {
	_, store := newCrawlRecoveryStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	source := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2}}
	resolver := NewSourceWorkspaces(NewSourceRegistry(t.TempDir()), source, path, func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error) {
		return &SourceWorkspace{Source: source, DBPath: path, Store: store}, nil
	})
	run, _, err := store.ReserveScan(ctx, CrawlConfig{SourceID: 2}, ScanRequest{SourceSlug: "sample", RequestKey: "one"}, "identity")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.BeginScan(ctx, run.ID, "identity"); err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertQueueItem(ctx, QueueItem{RunID: run.ID, URL: "https://example.test/", Status: QueueProcessing}); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireScanLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = resolver.Recover(ctx, "sample"); err != nil {
		t.Fatal(err)
	}
	active, _ := store.ScanRun(ctx, run.ID)
	if active.State != ScanRunning {
		t.Fatal("live owner marked interrupted")
	}
	lease.Close()
	if err = resolver.Recover(ctx, "sample"); err != nil {
		t.Fatal(err)
	}
	progress, err := store.ScanProgress(ctx, run.ID)
	if err != nil || progress.State != ScanInterrupted || progress.Processing != 0 || progress.Failed != 1 {
		t.Fatalf("abandoned work still processing: %#v %v", progress, err)
	}
}

func TestWorkspaceRetryModeSurvivesStoreReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "source.db")
	store, err := OpenReviewStore(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, mode := range []string{"fixture", "openai"} {
		name := "deterministic-fixture-model"
		if mode == "openai" {
			name = "provider"
		}
		run, _, err := store.ReserveScan(ctx, CrawlConfig{SourceID: 2, Model: name, Scope: "Keep source headings"}, ScanRequest{SourceSlug: "sample", RequestKey: mode, ModelMode: mode}, mode)
		if err != nil {
			t.Fatal(err)
		}
		page, err := store.SavePageVersion(ctx, PageVersion{SourceID: 2, RunID: run.ID, URL: "https://example.test/" + mode + "/", RawHTML: "<main><h1>Source</h1></main>"})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := store.SaveCandidate(ctx, Candidate{SourceID: 2, PageVersionID: page.ID, Status: CandidateQueued, Model: name})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, candidate.ID)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReviewStore(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	workspace := &SourceWorkspace{Source: SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2}}, Store: reopened}
	modes := []string{}
	retry := NewWorkspaceRetrier(workspace, func(_ SourceEntry, mode string) (SemanticModel, string, error) {
		modes = append(modes, mode)
		name := "deterministic-fixture-model"
		if mode == "openai" {
			name = "provider"
		}
		return &recordingModel{}, name, nil
	}, "fixture")
	for _, id := range ids {
		if err = retry.Retry(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if len(modes) != 2 || modes[0] != "fixture" || modes[1] != "openai" {
		t.Fatalf("mode metadata lost on restart: %v", modes)
	}
}
