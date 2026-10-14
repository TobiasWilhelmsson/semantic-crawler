package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"semantic-crawler/internal/models"
)

type GormStore struct {
	db *gorm.DB
}

func NewGormStore(db *gorm.DB) *GormStore {
	return &GormStore{db: db}
}

func (store *GormStore) WithReviewTransaction(ctx context.Context, action func(ReviewRepository) error) error {
	return store.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		return action(NewGormStore(transaction))
	})
}

func (store *GormStore) LatestPageVersion(ctx context.Context, sourceID int64, url string) (PageVersion, bool, error) {
	var record models.SemanticPageVersion
	err := store.db.WithContext(ctx).Where("source_id = ? AND canonical_url = ?", sourceID, url).Order("id DESC").First(&record).Error
	if err == gorm.ErrRecordNotFound {
		return PageVersion{}, false, nil
	}
	if err != nil {
		return PageVersion{}, false, fmt.Errorf("load latest semantic page: %w", err)
	}
	return pageFromModel(record), true, nil
}

func (store *GormStore) PageVersionByID(ctx context.Context, id int64) (PageVersion, error) {
	var record models.SemanticPageVersion
	if err := store.db.WithContext(ctx).First(&record, id).Error; err != nil {
		return PageVersion{}, fmt.Errorf("load semantic page %d: %w", id, err)
	}
	return pageFromModel(record), nil
}

func (store *GormStore) CandidatesByStatus(ctx context.Context, statuses ...CandidateStatus) ([]Candidate, error) {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		values = append(values, string(status))
	}
	var records []models.SemanticCandidate
	query := store.db.WithContext(ctx).Order("id ASC")
	if len(values) > 0 {
		query = query.Where("status IN ?", values)
	}
	if err := query.Find(&records).Error; err != nil {
		return nil, fmt.Errorf("load semantic candidates: %w", err)
	}
	result := make([]Candidate, 0, len(records))
	for _, record := range records {
		candidate, err := candidateFromModel(record)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (store *GormStore) LatestApprovedCandidate(ctx context.Context, sourceID int64, url string, excludeID int64) (Candidate, bool, error) {
	var record models.SemanticCandidate
	err := store.db.WithContext(ctx).Where("source_id = ? AND origin_url = ? AND status = ? AND id != ?", sourceID, url, string(CandidateApproved), excludeID).Order("id DESC").First(&record).Error
	if err == gorm.ErrRecordNotFound {
		return Candidate{}, false, nil
	}
	if err != nil {
		return Candidate{}, false, fmt.Errorf("load previous approved candidate: %w", err)
	}
	candidate, err := candidateFromModel(record)
	return candidate, err == nil, err
}

func (store *GormStore) SavePageVersion(ctx context.Context, page PageVersion) (PageVersion, error) {
	record := models.SemanticPageVersion{ExtractionIdentity: page.ExtractionIdentity, ID: page.ID, SourceID: page.SourceID, RunID: page.RunID, CanonicalURL: page.URL, Title: page.Title, StatusCode: page.StatusCode, RawHTML: page.RawHTML, SnapshotHTML: page.SnapshotHTML, CoreText: page.CoreText, ContentHash: page.ContentHash, PreviousHash: page.PreviousHash, FetchMode: page.FetchMode, Processed: page.Processed, LastCheckedAt: page.CheckedAt, ChangedAt: page.ChangedAt}
	if err := store.db.WithContext(ctx).Create(&record).Error; err != nil {
		return PageVersion{}, fmt.Errorf("save semantic page: %w", err)
	}
	return pageFromModel(record), nil
}

func (store *GormStore) Candidate(ctx context.Context, id int64) (Candidate, error) {
	var record models.SemanticCandidate
	if err := store.db.WithContext(ctx).First(&record, id).Error; err != nil {
		return Candidate{}, fmt.Errorf("load semantic candidate %d: %w", id, err)
	}
	return candidateFromModel(record)
}

func (store *GormStore) SaveCandidate(ctx context.Context, candidate Candidate) (Candidate, error) {
	sections, err := json.Marshal(candidate.Sections)
	if err != nil {
		return Candidate{}, fmt.Errorf("encode candidate sections: %w", err)
	}
	record := models.SemanticCandidate{ExtractionIdentity: candidate.ExtractionIdentity, ID: candidate.ID, SourceID: candidate.SourceID, PageVersionID: candidate.PageVersionID, OriginURL: candidate.URL, Status: string(candidate.Status), SupersedesID: candidate.SupersedesID, Sections: datatypes.JSON(sections), Markdown: candidate.Markdown, RouteResponse: datatypes.JSON(candidate.RouteResponse), BlockResponse: datatypes.JSON(candidate.BlockResponse), PromptVersion: candidate.PromptVersion, SkillVersion: candidate.SkillVersion, SourceSkillHash: candidate.SourceSkillHash, SourceSkillVersion: candidate.SourceSkillVersion, AppliedRules: datatypes.JSON(candidate.AppliedRules), Model: candidate.Model, InputTokens: candidate.InputTokens, OutputTokens: candidate.OutputTokens, LatencyMS: candidate.LatencyMS, RawModelResponse: candidate.RawResponse, ApprovedBy: candidate.ApprovedBy, ApprovedAt: candidate.ApprovedAt, CreatedAt: candidate.CreatedAt}
	if candidate.ID == 0 {
		err = store.db.WithContext(ctx).Create(&record).Error
	} else {
		err = store.db.WithContext(ctx).Save(&record).Error
	}
	if err != nil {
		return Candidate{}, fmt.Errorf("save semantic candidate: %w", err)
	}
	return candidateFromModel(record)
}

func (store *GormStore) SetCandidateStatus(ctx context.Context, id int64, status CandidateStatus) error {
	result := store.db.WithContext(ctx).Model(&models.SemanticCandidate{}).Where("id = ?", id).Update("status", string(status))
	if result.Error != nil {
		return fmt.Errorf("update semantic candidate status: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("candidate %d not found", id)
	}
	return nil
}

func (store *GormStore) AddRule(ctx context.Context, rule SkillRule) (SkillRule, error) {
	record := models.SemanticSkillRule{SourceID: rule.SourceID, OriginURL: rule.OriginURL, Scope: string(rule.Scope), Instruction: rule.Instruction, Version: rule.Version, Status: rule.Status, CreatedBy: rule.CreatedBy, CreatedAt: rule.CreatedAt}
	if err := store.db.WithContext(ctx).Create(&record).Error; err != nil {
		return SkillRule{}, fmt.Errorf("save semantic skill rule: %w", err)
	}
	rule.ID = record.ID
	rule.CreatedAt = record.CreatedAt
	return rule, nil
}

func (store *GormStore) Rules(ctx context.Context, sourceID int64, url string) ([]SkillRule, error) {
	var records []models.SemanticSkillRule
	err := store.db.WithContext(ctx).Where("source_id = ? AND status = ? AND (scope = ? OR (scope = ? AND origin_url = ?))", sourceID, "active", string(RuleScopeSource), string(RuleScopeURL), url).Order("version ASC").Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("load semantic skill rules: %w", err)
	}
	rules := make([]SkillRule, 0, len(records))
	for _, record := range records {
		rules = append(rules, SkillRule{ID: record.ID, SourceID: record.SourceID, OriginURL: record.OriginURL, Scope: RuleScope(record.Scope), Instruction: record.Instruction, Version: record.Version, Status: record.Status, CreatedBy: record.CreatedBy, CreatedAt: record.CreatedAt})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Version < rules[j].Version })
	return rules, nil
}

func (store *GormStore) AllRules(ctx context.Context) ([]SkillRule, error) {
	var records []models.SemanticSkillRule
	if err := store.db.WithContext(ctx).Order("source_id ASC, version ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("load all semantic skill rules: %w", err)
	}
	rules := make([]SkillRule, 0, len(records))
	for _, record := range records {
		rules = append(rules, SkillRule{ID: record.ID, SourceID: record.SourceID, OriginURL: record.OriginURL, Scope: RuleScope(record.Scope), Instruction: record.Instruction, Version: record.Version, Status: record.Status, CreatedBy: record.CreatedBy, CreatedAt: record.CreatedAt})
	}
	return rules, nil
}

func (store *GormStore) CurrentSkillVersion(ctx context.Context, sourceID int64) (int, error) {
	var version int
	if err := store.db.WithContext(ctx).Model(&models.SemanticSkillRule{}).Where("source_id = ?", sourceID).Select("COALESCE(MAX(version), 1)").Scan(&version).Error; err != nil {
		return 0, fmt.Errorf("load current skill version: %w", err)
	}
	if version < 1 {
		version = 1
	}
	return version, nil
}

func (store *GormStore) AddReview(ctx context.Context, review Review) error {
	edited, err := json.Marshal(review.EditedSections)
	if err != nil {
		return fmt.Errorf("encode review sections: %w", err)
	}
	record := models.SemanticReview{CandidateID: review.CandidateID, Reviewer: review.Reviewer, Action: review.Action, EditedSections: datatypes.JSON(edited), Instruction: review.Instruction, InstructionScope: string(review.InstructionScope), Comment: review.Comment, CreatedAt: review.CreatedAt}
	if err := store.db.WithContext(ctx).Create(&record).Error; err != nil {
		return fmt.Errorf("save semantic review: %w", err)
	}
	return nil
}

func (store *GormStore) DisableRule(ctx context.Context, id int64) error {
	result := store.db.WithContext(ctx).Model(&models.SemanticSkillRule{}).Where("id = ?", id).Update("status", "disabled")
	if result.Error != nil {
		return fmt.Errorf("disable semantic rule: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("rule %d not found", id)
	}
	return nil
}

func (store *GormStore) AddDelivery(ctx context.Context, delivery DeliveryRecord) error {
	record := models.SemanticDelivery{SchemaVersion: delivery.SchemaVersion, CandidateID: delivery.CandidateID, Endpoint: delivery.Endpoint, PayloadHash: delivery.PayloadHash, Status: delivery.Status, StatusCode: delivery.StatusCode, ResponseBody: delivery.Body, AttemptedAt: delivery.AttemptedAt}
	if err := store.db.WithContext(ctx).Create(&record).Error; err != nil {
		return fmt.Errorf("save semantic delivery: %w", err)
	}
	return nil
}

func (store *GormStore) StartCrawlRun(ctx context.Context, config CrawlConfig) (int64, error) {
	configuration, _ := json.Marshal(config)
	record := models.SemanticCrawlRun{SourceID: config.SourceID, Mode: config.Mode, Status: "running", Model: config.Model, PromptVersion: config.PromptVersion, SkillVersion: config.SkillVersion, ConfigurationJSON: datatypes.JSON(configuration), StartedAt: time.Now().UTC()}
	if err := store.db.WithContext(ctx).Create(&record).Error; err != nil {
		return 0, fmt.Errorf("start semantic crawl run: %w", err)
	}
	return record.ID, nil
}

func (store *GormStore) UpsertQueueItem(ctx context.Context, item QueueItem) error {
	record := models.SemanticQueueItem{RunID: item.RunID, CanonicalURL: item.URL, ParentURL: item.ParentURL, Depth: item.Depth, Status: string(item.Status), DiscoveredAt: time.Now().UTC()}
	if err := store.db.WithContext(ctx).Where("run_id = ? AND canonical_url = ?", item.RunID, item.URL).FirstOrCreate(&record).Error; err != nil {
		return fmt.Errorf("save semantic queue item: %w", err)
	}
	return nil
}

func (store *GormStore) UpdateQueueItem(ctx context.Context, runID int64, url string, status QueueStatus, action RouteAction, lastError string) error {
	updates := map[string]any{"status": string(status), "route_action": string(action), "last_error": lastError}
	if status == QueueProcessing {
		updates["attempt_count"] = gorm.Expr("attempt_count + 1")
	}
	if status == QueueCompleted || status == QueueFailed {
		updates["processed_at"] = time.Now().UTC()
	}
	result := store.db.WithContext(ctx).Model(&models.SemanticQueueItem{}).Where("run_id = ? AND canonical_url = ?", runID, url).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update semantic queue item: %w", result.Error)
	}
	return nil
}

func (store *GormStore) TouchPageCheckedAt(ctx context.Context, pageID int64, checkedAt time.Time) error {
	if err := store.db.WithContext(ctx).Model(&models.SemanticPageVersion{}).Where("id = ?", pageID).Update("last_checked_at", checkedAt).Error; err != nil {
		return fmt.Errorf("touch semantic page: %w", err)
	}
	return nil
}

func (store *GormStore) FinishCrawlRun(ctx context.Context, runID int64, summary CrawlSummary, runError string) error {
	metrics, _ := json.Marshal(summary)
	status := "completed"
	if runError != "" || summary.Failed > 0 {
		status = "failed"
	}
	completed := time.Now().UTC()
	if err := store.db.WithContext(ctx).Model(&models.SemanticCrawlRun{}).Where("id = ?", runID).Updates(map[string]any{"status": status, "metrics_json": datatypes.JSON(metrics), "error": runError, "completed_at": completed}).Error; err != nil {
		return fmt.Errorf("finish semantic crawl run: %w", err)
	}
	return nil
}

func pageFromModel(record models.SemanticPageVersion) PageVersion {
	return PageVersion{ExtractionIdentity: record.ExtractionIdentity, ID: record.ID, SourceID: record.SourceID, RunID: record.RunID, URL: record.CanonicalURL, Title: record.Title, StatusCode: record.StatusCode, RawHTML: record.RawHTML, SnapshotHTML: record.SnapshotHTML, CoreText: record.CoreText, ContentHash: record.ContentHash, PreviousHash: record.PreviousHash, CheckedAt: record.LastCheckedAt, ChangedAt: record.ChangedAt, FetchMode: record.FetchMode, Processed: record.Processed}
}

func candidateFromModel(record models.SemanticCandidate) (Candidate, error) {
	var sections []Section
	if len(record.Sections) > 0 {
		if err := json.Unmarshal(record.Sections, &sections); err != nil {
			return Candidate{}, fmt.Errorf("decode candidate sections: %w", err)
		}
	}
	return Candidate{ExtractionIdentity: record.ExtractionIdentity, ID: record.ID, SourceID: record.SourceID, PageVersionID: record.PageVersionID, URL: record.OriginURL, Status: CandidateStatus(record.Status), SupersedesID: record.SupersedesID, Sections: sections, Markdown: record.Markdown, RouteResponse: json.RawMessage(record.RouteResponse), BlockResponse: json.RawMessage(record.BlockResponse), PromptVersion: record.PromptVersion, SkillVersion: record.SkillVersion, SourceSkillHash: record.SourceSkillHash, SourceSkillVersion: record.SourceSkillVersion, AppliedRules: json.RawMessage(record.AppliedRules), Model: record.Model, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, LatencyMS: record.LatencyMS, RawResponse: record.RawModelResponse, ApprovedBy: record.ApprovedBy, ApprovedAt: record.ApprovedAt, CreatedAt: record.CreatedAt}, nil
}

func (store *GormStore) MarkPageProcessed(ctx context.Context, pageID int64) error {
	result := store.db.WithContext(ctx).Model(&models.SemanticPageVersion{}).Where("id = ?", pageID).Update("processed", true)
	if result.Error != nil {
		return fmt.Errorf("mark page processing complete: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("page version %d not found", pageID)
	}
	return nil
}
