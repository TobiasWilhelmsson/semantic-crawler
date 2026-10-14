package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"semantic-crawler/internal/models"
)

func (store *GormStore) ReserveScan(ctx context.Context, config CrawlConfig, request ScanRequest, identity string) (ScanRun, bool, error) {
	if request.RequestKey == "" || identity == "" {
		return ScanRun{}, false, fmt.Errorf("scan request identity is required")
	}
	encoded, err := json.Marshal(scanConfiguration{CrawlConfig: config, SourceSlug: request.SourceSlug, ModelMode: request.ModelMode})
	if err != nil {
		return ScanRun{}, false, err
	}
	row := models.SemanticCrawlRun{SourceID: config.SourceID, RequestKey: &request.RequestKey, RequestIdentity: identity, Mode: config.Mode, Status: string(ScanQueued), Model: config.Model, PromptVersion: config.PromptVersion, SkillVersion: config.SkillVersion, ConfigurationJSON: datatypes.JSON(encoded), StartedAt: time.Now().UTC()}
	result := store.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return ScanRun{}, false, result.Error
	}
	existing := result.RowsAffected == 0
	if existing {
		if err = store.db.WithContext(ctx).Where("source_id = ? AND request_key = ?", config.SourceID, request.RequestKey).First(&row).Error; err != nil {
			return ScanRun{}, false, err
		}
	}
	if row.RequestIdentity != identity {
		return ScanRun{}, false, ErrScanRequestConflict
	}
	run, err := scanRunFromRecord(row)
	return run, existing, err
}
func (store *GormStore) requestScan(ctx context.Context, sourceID int64, key, identity string) (ScanRun, bool, error) {
	// Old databases have no UI request keys until an explicit writable upgrade.
	if !store.db.Migrator().HasColumn(&models.SemanticCrawlRun{}, "request_key") {
		return ScanRun{}, false, nil
	}
	var row models.SemanticCrawlRun
	err := store.db.WithContext(ctx).Where("source_id = ? AND request_key = ?", sourceID, key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ScanRun{}, false, nil
	}
	if err != nil {
		return ScanRun{}, false, err
	}
	if row.RequestIdentity != identity {
		return ScanRun{}, false, ErrScanRequestConflict
	}
	run, err := scanRunFromRecord(row)
	return run, true, err
}
func (store *GormStore) BeginScan(ctx context.Context, id int64, identity string) error {
	result := store.db.WithContext(ctx).Model(&models.SemanticCrawlRun{}).Where("id = ? AND status = ? AND request_identity = ?", id, ScanQueued, identity).Update("status", ScanRunning)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("scan is no longer queued")
	}
	return nil
}
func (store *GormStore) FinishScan(ctx context.Context, id int64, state ScanState, summary CrawlSummary, cause error) error {
	if state != ScanCompleted && state != ScanFailed && state != ScanCancelled && state != ScanInterrupted {
		return fmt.Errorf("invalid terminal scan state")
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	result := store.db.WithContext(ctx).Model(&models.SemanticCrawlRun{}).Where("id = ? AND request_key IS NOT NULL AND status IN ?", id, []string{string(ScanQueued), string(ScanRunning), string(ScanCancelling)}).Updates(map[string]any{"status": state, "metrics_json": datatypes.JSON(encoded), "error": message, "completed_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("scan is already terminal or not managed by the UI")
	}
	return nil
}
func (store *GormStore) SaveScanMetrics(ctx context.Context, id int64, summary CrawlSummary, message string) error {
	encoded, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return store.db.WithContext(ctx).Model(&models.SemanticCrawlRun{}).Where("id = ? AND status IN ?", id, []string{"queued", "running", "cancelling"}).Updates(map[string]any{"metrics_json": datatypes.JSON(encoded), "error": message}).Error
}
func (store *GormStore) ScanRun(ctx context.Context, id int64) (ScanRun, error) {
	var row models.SemanticCrawlRun
	if err := store.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return ScanRun{}, err
	}
	return scanRunFromRecord(row)
}
func (store *GormStore) ScanRuns(ctx context.Context, sourceID int64, limit int) ([]ScanRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var rows []models.SemanticCrawlRun
	if err := store.db.WithContext(ctx).Where("source_id = ?", sourceID).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	runs := make([]ScanRun, 0, len(rows))
	for _, row := range rows {
		run, err := scanRunFromRecord(row)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}
func scanRunFromRecord(row models.SemanticCrawlRun) (ScanRun, error) {
	var config scanConfiguration
	var summary CrawlSummary
	if len(row.ConfigurationJSON) > 0 {
		if err := json.Unmarshal(row.ConfigurationJSON, &config); err != nil {
			return ScanRun{}, fmt.Errorf("scan configuration is unreadable")
		}
	}
	if len(row.MetricsJSON) > 0 {
		if err := json.Unmarshal(row.MetricsJSON, &summary); err != nil {
			return ScanRun{}, fmt.Errorf("scan summary is unreadable")
		}
	}
	message := ""
	if row.Error != "" {
		message = "Scan encountered an error. Check source and model configuration before scanning again."
	}
	if ScanState(row.Status) == ScanCancelled {
		message = "Scan cancelled. Review completed pages or scan again to retry unfinished work."
	}
	return ScanRun{ID: row.ID, SourceID: row.SourceID, SourceSlug: config.SourceSlug, State: ScanState(row.Status), Config: config.CrawlConfig, ModelMode: config.ModelMode, Fetch: config.Fetch, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, Summary: summary, Error: message}, nil
}
func (store *GormStore) RecordQueueOutcome(ctx context.Context, id int64, url, outcome string) error {
	result := store.db.WithContext(ctx).Model(&models.SemanticQueueItem{}).Where("run_id = ? AND canonical_url = ?", id, url).Update("outcome", outcome)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("queue item not found")
	}
	return nil
}
func (store *GormStore) CandidatesForRun(ctx context.Context, id int64) ([]Candidate, error) {
	var rows []models.SemanticCandidate
	if err := store.db.WithContext(ctx).Where("page_version_id IN (?)", store.db.Model(&models.SemanticPageVersion{}).Select("id").Where("run_id = ?", id)).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		candidate, err := candidateFromModel(row)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}
func (store *GormStore) ScanProgress(ctx context.Context, id int64) (ScanProgress, error) {
	run, err := store.ScanRun(ctx, id)
	if err != nil {
		return ScanProgress{}, err
	}
	progress := ScanProgress{RunID: id, State: run.State, Elapsed: scanElapsed(run)}
	var groups []struct {
		Status, Outcome string
		Count           int
	}
	outcomeColumn := "outcome"
	if !store.db.Migrator().HasColumn(&models.SemanticQueueItem{}, "outcome") {
		outcomeColumn = "''"
	}
	query := store.db.WithContext(ctx).Model(&models.SemanticQueueItem{}).Where("run_id = ?", id).Select("status, " + outcomeColumn + " AS outcome, COUNT(*) AS count").Group("status")
	if outcomeColumn != "''" {
		query = query.Group("outcome")
	}
	if err = query.Scan(&groups).Error; err != nil {
		return progress, err
	}
	for _, group := range groups {
		progress.Discovered += group.Count
		switch QueueStatus(group.Status) {
		case QueuePending:
			progress.Waiting += group.Count
		case QueueProcessing:
			progress.Processing += group.Count
		case QueueCompleted:
			progress.Finished += group.Count
			if group.Outcome == "" {
				progress.Unknown += group.Count
			}
		case QueueFailed:
			progress.Finished += group.Count
			progress.Failed += group.Count
		}
		switch group.Outcome {
		case "unchanged":
			progress.Unchanged += group.Count
		case "ignored":
			progress.Ignored += group.Count
		case "needs_review":
			progress.NeedsReview += group.Count
		}
	}
	var rows []struct {
		CanonicalURL, Status, Outcome string
		HasError                      bool
	}
	if err = store.db.WithContext(ctx).Model(&models.SemanticQueueItem{}).Where("run_id = ?", id).Select("canonical_url, status, " + outcomeColumn + " AS outcome, last_error != '' AS has_error").Order("id ASC").Limit(100).Scan(&rows).Error; err != nil {
		return progress, err
	}
	for _, row := range rows {
		message := ""
		if row.HasError {
			message = "Page processing failed; check source or model settings."
			if run.State == ScanCancelled {
				message = "Page unfinished in a cancelled scan. Scan again to retry it."
			}
		}
		progress.Pages = append(progress.Pages, ScanPage{URL: row.CanonicalURL, Status: row.Status, Outcome: row.Outcome, Error: message})
	}
	if progress.Processing > 0 {
		var current string
		if err = store.db.WithContext(ctx).Model(&models.SemanticQueueItem{}).Where("run_id = ? AND status = ?", id, QueueProcessing).Select("canonical_url").Limit(1).Scan(&current).Error; err != nil {
			return progress, err
		}
		progress.CurrentURL = current
	}
	var count int64
	if err = store.db.WithContext(ctx).Model(&models.SemanticCandidate{}).Where("page_version_id IN (?)", store.db.Model(&models.SemanticPageVersion{}).Select("id").Where("run_id = ?", id)).Count(&count).Error; err != nil {
		return progress, err
	}
	progress.Candidates = int(count)
	return progress, nil
}
