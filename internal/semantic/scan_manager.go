package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"semantic-crawler/internal/models"
)

type ScanExecutor func(context.Context, PreparedScan, int64) (CrawlSummary, error)
type scanWorker struct {
	slug            string
	id              int64
	store           *GormStore
	cancel          context.CancelFunc
	done            chan struct{}
	cancelRequested bool
}
type ScanManager struct {
	mu       sync.Mutex
	parent   context.Context
	executor ScanExecutor
	worker   *scanWorker
	last     *scanWorker
	closed   bool
	failure  error
}

func NewScanManager(parent context.Context, executor ScanExecutor) *ScanManager {
	if executor == nil {
		executor = executePreparedScan
	}
	return &ScanManager{parent: parent, executor: executor}
}
func (manager *ScanManager) Start(ctx context.Context, scan PreparedScan) (ScanRun, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ScanRun{}, err
	}
	if manager.closed || manager.parent.Err() != nil {
		return ScanRun{}, fmt.Errorf("scan server is shutting down")
	}
	if scan.OpenWorkspace == nil || scan.Request.RequestKey == "" || scan.Identity == "" {
		return ScanRun{}, fmt.Errorf("scan preparation is incomplete")
	}
	existingWorkspace, err := scan.OpenWorkspace(ctx, false)
	if err != nil && !errors.Is(err, ErrWorkspaceEmpty) {
		return ScanRun{}, err
	}
	if err == nil {
		if run, found, err := existingWorkspace.Store.requestScan(ctx, scan.Config.SourceID, scan.Request.RequestKey, scan.Identity); err != nil || found {
			return run, err
		}
	}
	if manager.worker != nil {
		return ScanRun{}, ErrScanBusy
	}
	lock, err := AcquireScanLock(scan.DBPath)
	if err != nil {
		return ScanRun{}, err
	}
	release := true
	defer func() {
		if release {
			lock.Close()
		}
	}()
	workspace, err := scan.OpenWorkspace(ctx, true)
	if err != nil {
		return ScanRun{}, err
	}
	scan.Workspace = workspace
	if err = recoverScanRecords(ctx, workspace.Store, scan.Config.SourceID); err != nil {
		return ScanRun{}, err
	}
	run, found, err := workspace.Store.ReserveScan(ctx, scan.Config, scan.Request, scan.Identity)
	if err != nil || found {
		return run, err
	}
	configuration, err := json.Marshal(scanConfiguration{CrawlConfig: scan.Config, SourceSlug: scan.Request.SourceSlug, ModelMode: scan.Request.ModelMode, Fetch: scan.Fetch})
	if err == nil {
		err = workspace.Store.db.WithContext(ctx).Model(&models.SemanticCrawlRun{}).Where("id = ?", run.ID).Update("configuration_json", datatypes.JSON(configuration)).Error
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return ScanRun{}, errors.Join(err, workspace.Store.FinishScan(cleanup, run.ID, ScanFailed, CrawlSummary{}, err))
	}
	workerCtx, cancel := context.WithCancel(manager.parent)
	worker := &scanWorker{slug: scan.Request.SourceSlug, id: run.ID, store: workspace.Store, cancel: cancel, done: make(chan struct{})}
	manager.worker = worker
	release = false
	go manager.execute(workerCtx, scan, worker, lock)
	run.Fetch = scan.Fetch
	return run, nil
}
func (manager *ScanManager) execute(ctx context.Context, scan PreparedScan, worker *scanWorker, lock *ScanLock) {
	summary := CrawlSummary{}
	err := worker.store.BeginScan(ctx, worker.id, scan.Identity)
	if err == nil {
		summary, err = manager.executor(ctx, scan, worker.id)
	}
	manager.mu.Lock()
	state := ScanCompleted
	if worker.cancelRequested || ctx.Err() != nil {
		state = ScanCancelled
		err = errors.Join(err, ctx.Err())
	} else if err != nil || summary.Failed > 0 {
		state = ScanFailed
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	finishErr := worker.store.FinishScan(cleanup, worker.id, state, summary, err)
	cancel()
	manager.failure = errors.Join(manager.failure, finishErr, lock.Close())
	worker.cancel()
	manager.last = worker
	manager.worker = nil
	close(worker.done)
	manager.mu.Unlock()
}
func (manager *ScanManager) Cancel(ctx context.Context, slug string, id int64) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.worker == nil || manager.worker.slug != slug || manager.worker.id != id {
		if manager.last != nil && manager.last.slug == slug && manager.last.id == id {
			return nil
		}
		return ErrScanNotOwned
	}
	worker := manager.worker
	if worker.cancelRequested {
		return nil
	}
	if err := worker.store.db.WithContext(ctx).Model(&models.SemanticCrawlRun{}).Where("id = ? AND status IN ?", id, []string{"queued", "running"}).Update("status", ScanCancelling).Error; err != nil {
		return err
	}
	worker.cancelRequested = true
	worker.cancel()
	return nil
}
func (manager *ScanManager) Active() (string, int64, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.worker == nil {
		return "", 0, false
	}
	return manager.worker.slug, manager.worker.id, true
}
func (manager *ScanManager) Shutdown(ctx context.Context) error {
	manager.mu.Lock()
	manager.closed = true
	worker := manager.worker
	if worker != nil {
		worker.cancelRequested = true
		worker.cancel()
	}
	manager.mu.Unlock()
	if worker != nil {
		select {
		case <-worker.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.failure
}
func recoverScanRecords(ctx context.Context, store *GormStore, sourceID int64) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []int64
		if err := tx.Model(&models.SemanticCrawlRun{}).Where("source_id = ? AND status IN ?", sourceID, []string{"queued", "running", "cancelling"}).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		updates := map[string]any{"status": QueueFailed, "last_error": "Previous scan owner exited before finalizing this page.", "processed_at": time.Now().UTC()}
		if tx.Migrator().HasColumn(&models.SemanticQueueItem{}, "outcome") {
			updates["outcome"] = "failed"
		}
		if err := tx.Model(&models.SemanticQueueItem{}).Where("run_id IN ? AND status = ?", ids, QueueProcessing).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(&models.SemanticCrawlRun{}).Where("id IN ?", ids).Updates(map[string]any{"status": ScanInterrupted, "completed_at": time.Now().UTC(), "error": "Previous scan owner exited before finalizing the run."}).Error
	})
}

type preparedScanStore struct {
	*GormStore
	id int64
}

func (store *preparedScanStore) StartCrawlRun(ctx context.Context, config CrawlConfig) (int64, error) {
	run, err := store.ScanRun(ctx, store.id)
	if err != nil {
		return 0, err
	}
	if run.SourceID != config.SourceID || run.State != ScanRunning {
		return 0, fmt.Errorf("reserved scan does not match pipeline")
	}
	return store.id, nil
}
func (store *preparedScanStore) FinishCrawlRun(ctx context.Context, id int64, summary CrawlSummary, message string) error {
	return store.SaveScanMetrics(ctx, id, summary, message)
}
func executePreparedScan(ctx context.Context, scan PreparedScan, id int64) (CrawlSummary, error) {
	return NewPipeline(scan.Fetcher, scan.Model, &preparedScanStore{GormStore: scan.Workspace.Store, id: id}).Run(ctx, scan.Config)
}
