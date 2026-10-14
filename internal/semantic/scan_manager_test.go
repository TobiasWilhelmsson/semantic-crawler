package semantic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestScanLockCanonicalDatabaseAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireScanLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	alias := filepath.Join(filepath.Dir(path), "alias.db")
	if err = os.Symlink(path, alias); err != nil {
		t.Skip(err)
	}
	if _, err = AcquireScanLock(alias); !errors.Is(err, ErrScanBusy) {
		t.Fatalf("alias lock: %v", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireScanLock(alias)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}
func TestScanManagerDuplicateCancelAndDisconnect(t *testing.T) {
	_, store := newCrawlRecoveryStore(t)
	var calls atomic.Int32
	manager := NewScanManager(context.Background(), func(ctx context.Context, scan PreparedScan, id int64) (CrawlSummary, error) {
		calls.Add(1)
		<-ctx.Done()
		return CrawlSummary{Discovered: 1}, ctx.Err()
	})
	defer manager.Shutdown(context.Background())
	entry := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2}}
	scan := PreparedScan{Source: entry, DBPath: filepath.Join(t.TempDir(), "db"), Request: ScanRequest{SourceSlug: "sample", RequestKey: "one", ModelMode: "fixture"}, Config: CrawlConfig{SourceID: 2}, Identity: "identity", OpenWorkspace: func(context.Context, bool) (*SourceWorkspace, error) {
		return &SourceWorkspace{Source: entry, Store: store}, nil
	}}
	requestCtx, cancel := context.WithCancel(context.Background())
	run, err := manager.Start(requestCtx, scan)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	again, err := manager.Start(context.Background(), scan)
	if err != nil || again.ID != run.ID {
		t.Fatalf("duplicate: %#v %v", again, err)
	}
	changed := scan
	changed.Identity = "changed"
	if _, err = manager.Start(context.Background(), changed); !errors.Is(err, ErrScanRequestConflict) {
		t.Fatalf("changed: %v", err)
	}
	other := scan
	other.Request.RequestKey = "other"
	if _, err = manager.Start(context.Background(), other); !errors.Is(err, ErrScanBusy) {
		t.Fatalf("busy: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("worker calls %d", calls.Load())
	}
	if err = manager.Cancel(context.Background(), "sample", run.ID); err != nil {
		t.Fatal(err)
	}
	if err = manager.Cancel(context.Background(), "sample", run.ID); err != nil {
		t.Fatal(err)
	}
	if err = manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = manager.Cancel(context.Background(), "sample", run.ID); err != nil {
		t.Fatalf("completed cancellation was not idempotent: %v", err)
	}
	saved, err := store.ScanRun(context.Background(), run.ID)
	if err != nil || saved.State != ScanCancelled {
		t.Fatalf("terminal: %#v %v", saved, err)
	}
}

func TestScanLockDanglingAliasSharesTargetIdentity(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.db")
	alias := filepath.Join(filepath.Dir(target), "alias.db")
	if err := os.Symlink(target, alias); err != nil {
		t.Skip(err)
	}
	lease, err := AcquireScanLock(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err = os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if next, err := AcquireScanLock(target); !errors.Is(err, ErrScanBusy) {
		if next != nil {
			next.Close()
		}
		t.Fatalf("dangling alias bypassed target lock: %v", err)
	}
}

func TestScanManagerPartialFailureRetainsCandidatesAndReleasesLock(t *testing.T) {
	_, store := newCrawlRecoveryStore(t)
	ctx := context.Background()
	source := SourceEntry{Slug: "sample", Config: SourceConfig{SourceID: 2}}
	path := filepath.Join(t.TempDir(), "source.db")
	scan := PreparedScan{Source: source, DBPath: path, Request: ScanRequest{SourceSlug: "sample", RequestKey: "partial", ModelMode: "fixture"}, Identity: "partial-identity", Config: CrawlConfig{SourceID: 2, SeedURL: "https://example.test/", AllowedHost: "example.test", MaxPages: 2, MaxDepth: 1}, Fetcher: mapFetcher{"https://example.test/": `<main><h1>Captured source</h1><a href="/missing/">Missing page</a></main>`}, Model: &crawlRecoveryModel{action: RouteExtract}, OpenWorkspace: func(context.Context, bool) (*SourceWorkspace, error) {
		return &SourceWorkspace{Source: source, DBPath: path, Store: store}, nil
	}}
	manager := NewScanManager(ctx, nil)
	defer manager.Shutdown(ctx)
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
			t.Fatal("partial scan did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	saved, err := store.ScanRun(ctx, run.ID)
	if err != nil || saved.State != ScanFailed || saved.Summary.Failed != 1 {
		t.Fatalf("partial failure: %#v %v", saved, err)
	}
	candidates, err := store.CandidatesForRun(ctx, run.ID)
	if err != nil || len(candidates) != 1 || candidates[0].Status != CandidatePendingReview {
		t.Fatalf("saved candidate lost: %#v %v", candidates, err)
	}
	lease, err := AcquireScanLock(path)
	if err != nil {
		t.Fatalf("failed worker retained lock: %v", err)
	}
	lease.Close()
}
