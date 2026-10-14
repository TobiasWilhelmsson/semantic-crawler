package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type CandidateStatus string

const (
	CandidateQueued        CandidateStatus = "queued"
	CandidatePendingReview CandidateStatus = "pending_review"
	CandidateApproved      CandidateStatus = "approved"
	CandidateRejected      CandidateStatus = "rejected"
	CandidateEscalated     CandidateStatus = "escalated"
	CandidateSuperseded    CandidateStatus = "superseded"
)

type RuleScope string

const (
	RuleScopeURL    RuleScope = "url"
	RuleScopeSource RuleScope = "source"
)

type Section struct {
	BlockID   string    `json:"block_id"`
	Name      string    `json:"name"`
	Role      BlockRole `json:"role"`
	Type      BlockType `json:"type"`
	ParseMode bool      `json:"parse_mode"`
	Content   string    `json:"content"`
	Locator   string    `json:"locator"`
}

type Candidate struct {
	ExtractionIdentity string          `json:"extraction_identity,omitempty"`
	ID                 int64           `json:"id"`
	SourceID           int64           `json:"source_id"`
	PageVersionID      int64           `json:"page_version_id"`
	URL                string          `json:"url"`
	Status             CandidateStatus `json:"status"`
	SupersedesID       int64           `json:"supersedes_id,omitempty"`
	Sections           []Section       `json:"sections"`
	Markdown           string          `json:"markdown"`
	RouteResponse      json.RawMessage `json:"route_response,omitempty"`
	BlockResponse      json.RawMessage `json:"block_response,omitempty"`
	PromptVersion      string          `json:"prompt_version"`
	SkillVersion       int             `json:"skill_version"`
	SourceSkillHash    string          `json:"source_skill_hash,omitempty"`
	SourceSkillVersion int             `json:"source_skill_version,omitempty"`
	AppliedRules       json.RawMessage `json:"applied_rules,omitempty"`
	Model              string          `json:"model"`
	InputTokens        int             `json:"input_tokens"`
	OutputTokens       int             `json:"output_tokens"`
	LatencyMS          int64           `json:"latency_ms"`
	RawResponse        string          `json:"raw_model_response,omitempty"`
	ApprovedBy         string          `json:"approved_by,omitempty"`
	ApprovedAt         *time.Time      `json:"approved_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}

type Review struct {
	CandidateID      int64
	Reviewer         string
	Action           string
	EditedSections   []Section
	Instruction      string
	InstructionScope RuleScope
	Comment          string
	CreatedAt        time.Time
}

type DeliveryRecord struct {
	SchemaVersion string
	CandidateID   int64
	Endpoint      string
	PayloadHash   string
	Status        string
	StatusCode    int
	Body          string
	AttemptedAt   time.Time
}

type SkillRule struct {
	ID          int64     `json:"id"`
	SourceID    int64     `json:"source_id"`
	OriginURL   string    `json:"origin_url,omitempty"`
	Scope       RuleScope `json:"scope"`
	Instruction string    `json:"instruction"`
	Version     int       `json:"version"`
	Status      string    `json:"status"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type ReviewRepository interface {
	Candidate(ctx context.Context, id int64) (Candidate, error)
	SaveCandidate(ctx context.Context, candidate Candidate) (Candidate, error)
	SetCandidateStatus(ctx context.Context, id int64, status CandidateStatus) error
	AddRule(ctx context.Context, rule SkillRule) (SkillRule, error)
	Rules(ctx context.Context, sourceID int64, url string) ([]SkillRule, error)
	CurrentSkillVersion(ctx context.Context, sourceID int64) (int, error)
	AddReview(ctx context.Context, review Review) error
	DisableRule(ctx context.Context, id int64) error
	AddDelivery(ctx context.Context, delivery DeliveryRecord) error
}

type ReviewTransactor interface {
	WithReviewTransaction(ctx context.Context, action func(ReviewRepository) error) error
}

type ReviewService struct {
	store ReviewRepository
}

func NewReviewService(store ReviewRepository) *ReviewService {
	return &ReviewService{store: store}
}

func (service *ReviewService) withTransaction(ctx context.Context, action func(*ReviewService) error) error {
	transactional, ok := service.store.(ReviewTransactor)
	if !ok {
		return fmt.Errorf("review repository does not support transactions")
	}
	return transactional.WithReviewTransaction(ctx, func(repository ReviewRepository) error {
		return action(NewReviewService(repository))
	})
}

func (service *ReviewService) Approve(ctx context.Context, id int64, reviewer string) (Candidate, error) {
	var result Candidate
	err := service.withTransaction(ctx, func(transaction *ReviewService) error {
		var err error
		result, err = transaction.approve(ctx, id, reviewer)
		return err
	})
	if err != nil {
		return Candidate{}, err
	}
	return result, nil
}

func (service *ReviewService) EditAndApprove(ctx context.Context, id int64, reviewer string, sections []Section, comment string) (Candidate, error) {
	var result Candidate
	err := service.withTransaction(ctx, func(transaction *ReviewService) error {
		var err error
		result, err = transaction.editAndApprove(ctx, id, reviewer, sections, comment)
		return err
	})
	if err != nil {
		return Candidate{}, err
	}
	return result, nil
}

func (service *ReviewService) RemoveSelectedAndApprove(ctx context.Context, id int64, reviewer string, removeBlockIDs []string, comment string) (Candidate, error) {
	var result Candidate
	err := service.withTransaction(ctx, func(transaction *ReviewService) error {
		var err error
		result, err = transaction.removeSelectedAndApprove(ctx, id, reviewer, removeBlockIDs, comment)
		return err
	})
	if err != nil {
		return Candidate{}, err
	}
	return result, nil
}

func (service *ReviewService) Reject(ctx context.Context, id int64) error {
	return service.withTransaction(ctx, func(transaction *ReviewService) error {
		return transaction.reject(ctx, id)
	})
}

func (service *ReviewService) Escalate(ctx context.Context, id int64) error {
	return service.withTransaction(ctx, func(transaction *ReviewService) error {
		return transaction.escalate(ctx, id)
	})
}

func (service *ReviewService) RetryWithInstruction(ctx context.Context, id int64, reviewer, instruction string, scope RuleScope) (SkillRule, Candidate, error) {
	var rule SkillRule
	var candidate Candidate
	err := service.withTransaction(ctx, func(transaction *ReviewService) error {
		var err error
		rule, candidate, err = transaction.retryWithInstruction(ctx, id, reviewer, instruction, scope)
		return err
	})
	if err != nil {
		return SkillRule{}, Candidate{}, err
	}
	return rule, candidate, nil
}

func (service *ReviewService) DisableRule(ctx context.Context, id int64, reviewer string) error {
	return service.withTransaction(ctx, func(transaction *ReviewService) error {
		return transaction.disableRule(ctx, id, reviewer)
	})
}

func (service *ReviewService) approve(ctx context.Context, id int64, reviewer string) (Candidate, error) {
	candidate, err := service.store.Candidate(ctx, id)
	if err != nil {
		return Candidate{}, err
	}
	if candidate.Status != CandidatePendingReview && candidate.Status != CandidateEscalated {
		return Candidate{}, fmt.Errorf("candidate %d cannot be approved from status %s", id, candidate.Status)
	}
	now := time.Now().UTC()
	candidate.Status = CandidateApproved
	candidate.ApprovedBy = reviewer
	candidate.ApprovedAt = &now
	approved, err := service.store.SaveCandidate(ctx, candidate)
	if err == nil {
		err = service.store.AddReview(ctx, Review{CandidateID: approved.ID, Reviewer: reviewer, Action: "approve", CreatedAt: now})
	}
	return approved, err
}

func (service *ReviewService) editAndApprove(ctx context.Context, id int64, reviewer string, sections []Section, comment string) (Candidate, error) {
	original, err := service.store.Candidate(ctx, id)
	if err != nil {
		return Candidate{}, err
	}
	if original.Status != CandidatePendingReview && original.Status != CandidateEscalated {
		return Candidate{}, fmt.Errorf("candidate %d cannot be edited from status %s", id, original.Status)
	}
	now := time.Now().UTC()
	revision := original
	revision.ID = 0
	revision.Status = CandidateApproved
	revision.SupersedesID = original.ID
	revision.Sections = cloneSections(sections)
	markdown, err := markdownFromSections(revision.Sections)
	if err != nil {
		return Candidate{}, err
	}
	revision.Markdown = markdown
	revision.ApprovedBy = reviewer
	revision.ApprovedAt = &now
	revision.CreatedAt = now
	approved, err := service.store.SaveCandidate(ctx, revision)
	if err == nil {
		if err = service.store.SetCandidateStatus(ctx, original.ID, CandidateSuperseded); err == nil {
			err = service.store.AddReview(ctx, Review{CandidateID: approved.ID, Reviewer: reviewer, Action: "edit_approve", EditedSections: cloneSections(sections), Comment: comment, CreatedAt: now})
		}
	}
	return approved, err
}

func (service *ReviewService) removeSelectedAndApprove(ctx context.Context, id int64, reviewer string, removeBlockIDs []string, comment string) (Candidate, error) {
	original, err := service.store.Candidate(ctx, id)
	if err != nil {
		return Candidate{}, err
	}
	if original.Status != CandidatePendingReview && original.Status != CandidateEscalated {
		return Candidate{}, fmt.Errorf("candidate %d cannot be edited from status %s", id, original.Status)
	}
	known := make(map[string]bool, len(original.Sections))
	for _, section := range original.Sections {
		known[section.BlockID] = true
	}
	remove := make(map[string]struct{}, len(removeBlockIDs))
	orderedRemoved := make([]string, 0, len(removeBlockIDs))
	for _, blockID := range removeBlockIDs {
		trimmed := strings.TrimSpace(blockID)
		if trimmed == "" {
			continue
		}
		if !known[trimmed] {
			return Candidate{}, fmt.Errorf("selected block %q does not exist in candidate", trimmed)
		}
		if _, exists := remove[trimmed]; !exists {
			orderedRemoved = append(orderedRemoved, trimmed)
		}
		remove[trimmed] = struct{}{}
	}
	if len(remove) == 0 {
		return Candidate{}, fmt.Errorf("at least one block must be selected for removal")
	}
	sections := make([]Section, 0, len(original.Sections))
	for _, section := range original.Sections {
		if _, drop := remove[section.BlockID]; !drop {
			sections = append(sections, section)
		}
	}
	auditComment := strings.TrimSpace(comment)
	if auditComment != "" {
		auditComment += "; "
	}
	auditComment += "removed blocks: " + strings.Join(orderedRemoved, ", ")
	now := time.Now().UTC()
	revision := original
	revision.ID = 0
	revision.Status = CandidateApproved
	revision.SupersedesID = original.ID
	revision.Sections = cloneSections(sections)
	markdown, err := markdownFromSections(revision.Sections)
	if err != nil {
		return Candidate{}, err
	}
	revision.Markdown = markdown
	revision.ApprovedBy = reviewer
	revision.ApprovedAt = &now
	revision.CreatedAt = now
	approved, err := service.store.SaveCandidate(ctx, revision)
	if err == nil {
		if err = service.store.SetCandidateStatus(ctx, original.ID, CandidateSuperseded); err == nil {
			err = service.store.AddReview(ctx, Review{CandidateID: approved.ID, Reviewer: reviewer, Action: "remove_selected_approve", EditedSections: cloneSections(sections), Comment: auditComment, CreatedAt: now})
		}
	}
	return approved, err
}

func (service *ReviewService) reject(ctx context.Context, id int64) error {
	candidate, err := service.store.Candidate(ctx, id)
	if err != nil {
		return err
	}
	if candidate.Status != CandidatePendingReview && candidate.Status != CandidateEscalated {
		return fmt.Errorf("candidate %d cannot be rejected from status %s", id, candidate.Status)
	}
	if err := service.store.SetCandidateStatus(ctx, id, CandidateRejected); err != nil {
		return err
	}
	return service.store.AddReview(ctx, Review{CandidateID: id, Action: "reject", CreatedAt: time.Now().UTC()})
}

func (service *ReviewService) escalate(ctx context.Context, id int64) error {
	candidate, err := service.store.Candidate(ctx, id)
	if err != nil {
		return err
	}
	if candidate.Status != CandidatePendingReview {
		return fmt.Errorf("candidate %d cannot be escalated from status %s", id, candidate.Status)
	}
	if err := service.store.SetCandidateStatus(ctx, id, CandidateEscalated); err != nil {
		return err
	}
	return service.store.AddReview(ctx, Review{CandidateID: id, Action: "escalate", CreatedAt: time.Now().UTC()})
}

func (service *ReviewService) retryWithInstruction(ctx context.Context, id int64, reviewer, instruction string, scope RuleScope) (SkillRule, Candidate, error) {
	candidate, err := service.store.Candidate(ctx, id)
	if err != nil {
		return SkillRule{}, Candidate{}, err
	}
	if candidate.Status != CandidatePendingReview && candidate.Status != CandidateEscalated {
		return SkillRule{}, Candidate{}, fmt.Errorf("candidate %d cannot be retried from status %s", id, candidate.Status)
	}
	if strings.TrimSpace(instruction) == "" {
		return SkillRule{}, Candidate{}, fmt.Errorf("retry instruction is required")
	}
	if scope != RuleScopeURL && scope != RuleScopeSource {
		return SkillRule{}, Candidate{}, fmt.Errorf("invalid rule scope %q", scope)
	}
	version, err := service.store.CurrentSkillVersion(ctx, candidate.SourceID)
	if err != nil {
		return SkillRule{}, Candidate{}, err
	}
	version++
	rule := SkillRule{SourceID: candidate.SourceID, Scope: scope, Instruction: strings.TrimSpace(instruction), Version: version, Status: "active", CreatedBy: reviewer, CreatedAt: time.Now().UTC()}
	if scope == RuleScopeURL {
		rule.OriginURL = candidate.URL
	}
	rerun := candidate
	rerun.ID = 0
	rerun.Status = CandidateQueued
	rerun.SupersedesID = candidate.ID
	rerun.SkillVersion = version
	rerun.ApprovedBy = ""
	rerun.ApprovedAt = nil
	rerun.CreatedAt = time.Now().UTC()
	rule, err = service.store.AddRule(ctx, rule)
	if err != nil {
		return SkillRule{}, Candidate{}, err
	}
	rerun, err = service.store.SaveCandidate(ctx, rerun)
	if err == nil {
		if err = service.store.SetCandidateStatus(ctx, candidate.ID, CandidateSuperseded); err == nil {
			err = service.store.AddReview(ctx, Review{CandidateID: candidate.ID, Reviewer: reviewer, Action: "retry", Instruction: instruction, InstructionScope: scope, CreatedAt: time.Now().UTC()})
		}
	}
	return rule, rerun, err
}

func (service *ReviewService) disableRule(ctx context.Context, id int64, reviewer string) error {
	if err := service.store.DisableRule(ctx, id); err != nil {
		return err
	}
	return service.store.AddReview(ctx, Review{Reviewer: reviewer, Action: "disable_rule", Comment: fmt.Sprintf("rule %d", id), CreatedAt: time.Now().UTC()})
}

func (service *ReviewService) ApplicableRules(ctx context.Context, sourceID int64, url string) ([]SkillRule, error) {
	return service.store.Rules(ctx, sourceID, url)
}

type DeliveryPayload struct {
	Document    *ExportDocument `json:"-"`
	CandidateID int64           `json:"candidate_id"`
	SourceID    int64           `json:"source_id"`
	Origin      string          `json:"origin"`
	Sections    []Section       `json:"sections"`
	Markdown    string          `json:"markdown"`
	ApprovedBy  string          `json:"approved_by"`
	ApprovedAt  time.Time       `json:"approved_at"`
}

type DeliveryResult struct {
	Endpoint    string `json:"endpoint"`
	PayloadHash string `json:"payload_hash"`
	StatusCode  int    `json:"status_code"`
	Body        string `json:"body"`
}

type DeliveryClient interface {
	Send(ctx context.Context, endpoint string, payload DeliveryPayload) (statusCode int, body string, err error)
}

type DeliveryService struct {
	documentSource   *SourceEntry
	store            ReviewRepository
	client           DeliveryClient
	allowedEndpoints map[string]struct{}
}

func NewDeliveryService(store ReviewRepository, client DeliveryClient) *DeliveryService {
	return &DeliveryService{store: store, client: client}
}

func NewRestrictedDeliveryService(store ReviewRepository, client DeliveryClient, endpoints []string) *DeliveryService {
	allowed := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint) != "" {
			allowed[strings.TrimSpace(endpoint)] = struct{}{}
		}
	}
	return &DeliveryService{store: store, client: client, allowedEndpoints: allowed}
}

func (service *DeliveryService) Deliver(ctx context.Context, candidateID int64, endpoint string) (DeliveryResult, error) {
	return service.DeliverWithSchema(ctx, candidateID, endpoint, "legacy")
}

func (service *DeliveryService) DeliverWithSchema(ctx context.Context, candidateID int64, endpoint, schema string) (DeliveryResult, error) {
	if schema == "" {
		schema = "legacy"
	}
	if schema != "legacy" && schema != "document-v1" {
		return DeliveryResult{}, fmt.Errorf("unsupported delivery schema %q", schema)
	}
	if service.allowedEndpoints != nil {
		if _, ok := service.allowedEndpoints[endpoint]; !ok {
			return DeliveryResult{}, fmt.Errorf("delivery endpoint %q is not allowed", endpoint)
		}
	}
	candidate, err := service.store.Candidate(ctx, candidateID)
	if err != nil {
		return DeliveryResult{}, err
	}
	if candidate.Status != CandidateApproved || candidate.ApprovedAt == nil {
		return DeliveryResult{}, fmt.Errorf("candidate %d is not approved", candidateID)
	}
	payload := DeliveryPayload{CandidateID: candidate.ID, SourceID: candidate.SourceID, Origin: candidate.URL, Sections: cloneSections(candidate.Sections), Markdown: candidate.Markdown, ApprovedBy: candidate.ApprovedBy, ApprovedAt: *candidate.ApprovedAt}
	schemaVersion := "legacy"
	if schema == "document-v1" {
		if service.documentSource == nil {
			return DeliveryResult{}, fmt.Errorf("document delivery source is not configured")
		}
		reader, ok := service.store.(interface {
			ExportDocuments(context.Context, SourceEntry, ExportOptions) ([]ExportDocument, error)
		})
		if !ok {
			return DeliveryResult{}, fmt.Errorf("document delivery requires saved source storage")
		}
		documents, err := reader.ExportDocuments(ctx, *service.documentSource, ExportOptions{CandidateID: candidateID})
		if err != nil {
			return DeliveryResult{}, err
		}
		if len(documents) != 1 || documents[0].Status != CandidateApproved {
			return DeliveryResult{}, fmt.Errorf("candidate is not approved")
		}
		payload.Document = &documents[0]
		schemaVersion = DocumentSchemaVersion
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return DeliveryResult{}, fmt.Errorf("encode delivery payload: %w", err)
	}
	hash := sha256.Sum256(encoded)
	status, body, sendErr := service.client.Send(ctx, endpoint, payload)
	deliveryStatus := "succeeded"
	if sendErr != nil || status < 200 || status >= 300 {
		deliveryStatus = "failed"
		if sendErr == nil {
			sendErr = fmt.Errorf("delivery returned HTTP %d", status)
		}
	}
	auditBody := body
	if auditBody == "" && sendErr != nil {
		auditBody = sendErr.Error()
	}
	// Once a request has been attempted, retain its outcome even if the browser
	// disconnects. A bounded cleanup context cannot promise delivery deduplication.
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	auditErr := service.store.AddDelivery(auditCtx, DeliveryRecord{SchemaVersion: schemaVersion, CandidateID: candidateID, Endpoint: endpoint, PayloadHash: hex.EncodeToString(hash[:]), Status: deliveryStatus, StatusCode: status, Body: auditBody, AttemptedAt: time.Now().UTC()})
	if err := errors.Join(sendErr, auditErr); err != nil {
		return DeliveryResult{}, err
	}
	return DeliveryResult{Endpoint: endpoint, PayloadHash: hex.EncodeToString(hash[:]), StatusCode: status, Body: body}, nil
}

type MemoryStore struct {
	mu         sync.Mutex
	nextID     int64
	candidates map[int64]Candidate
	pages      []PageVersion
	rules      []SkillRule
	reviews    []Review
	deliveries []DeliveryRecord
}

func (store *MemoryStore) WithReviewTransaction(ctx context.Context, action func(ReviewRepository) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	transaction := &MemoryStore{
		nextID:     store.nextID,
		candidates: make(map[int64]Candidate, len(store.candidates)),
		pages:      append([]PageVersion(nil), store.pages...),
		rules:      append([]SkillRule(nil), store.rules...),
		reviews:    append([]Review(nil), store.reviews...),
		deliveries: append([]DeliveryRecord(nil), store.deliveries...),
	}
	for id, candidate := range store.candidates {
		candidate.Sections = cloneSections(candidate.Sections)
		transaction.candidates[id] = candidate
	}
	if err := action(transaction); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	store.nextID = transaction.nextID
	store.candidates = transaction.candidates
	store.pages = transaction.pages
	store.rules = transaction.rules
	store.reviews = transaction.reviews
	store.deliveries = transaction.deliveries
	return nil
}

func (store *MemoryStore) Reviews() []Review {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]Review(nil), store.reviews...)
}
func (store *MemoryStore) Deliveries() []DeliveryRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]DeliveryRecord(nil), store.deliveries...)
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1, candidates: make(map[int64]Candidate)}
}

func (store *MemoryStore) PutCandidate(candidate Candidate) Candidate {
	stored, _ := store.SaveCandidate(context.Background(), candidate)
	return stored
}

func (store *MemoryStore) Candidates() []Candidate {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]Candidate, 0, len(store.candidates))
	for _, candidate := range store.candidates {
		candidate.Sections = cloneSections(candidate.Sections)
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (store *MemoryStore) Candidate(_ context.Context, id int64) (Candidate, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	candidate, exists := store.candidates[id]
	if !exists {
		return Candidate{}, fmt.Errorf("candidate %d not found", id)
	}
	candidate.Sections = cloneSections(candidate.Sections)
	return candidate, nil
}

func (store *MemoryStore) SaveCandidate(_ context.Context, candidate Candidate) (Candidate, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if candidate.ID == 0 {
		candidate.ID = store.nextID
		store.nextID++
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	candidate.Sections = cloneSections(candidate.Sections)
	store.candidates[candidate.ID] = candidate
	return candidate, nil
}

func (store *MemoryStore) SetCandidateStatus(_ context.Context, id int64, status CandidateStatus) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	candidate, exists := store.candidates[id]
	if !exists {
		return fmt.Errorf("candidate %d not found", id)
	}
	candidate.Status = status
	store.candidates[id] = candidate
	return nil
}

func (store *MemoryStore) AddRule(_ context.Context, rule SkillRule) (SkillRule, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	rule.ID = int64(len(store.rules) + 1)
	store.rules = append(store.rules, rule)
	return rule, nil
}

func (store *MemoryStore) AddReview(_ context.Context, review Review) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reviews = append(store.reviews, review)
	return nil
}

func (store *MemoryStore) AddDelivery(_ context.Context, delivery DeliveryRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.deliveries = append(store.deliveries, delivery)
	return nil
}

func (store *MemoryStore) DisableRule(_ context.Context, id int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := range store.rules {
		if store.rules[i].ID == id {
			store.rules[i].Status = "disabled"
			return nil
		}
	}
	return fmt.Errorf("rule %d not found", id)
}

func (store *MemoryStore) Rules(_ context.Context, sourceID int64, url string) ([]SkillRule, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var rules []SkillRule
	for _, rule := range store.rules {
		if rule.SourceID != sourceID || rule.Status != "active" {
			continue
		}
		if rule.Scope == RuleScopeSource || (rule.Scope == RuleScopeURL && rule.OriginURL == url) {
			rules = append(rules, rule)
		}
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Version < rules[j].Version })
	return rules, nil
}

func (store *MemoryStore) AllRules(_ context.Context) ([]SkillRule, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	rules := append([]SkillRule(nil), store.rules...)
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].SourceID != rules[j].SourceID {
			return rules[i].SourceID < rules[j].SourceID
		}
		if rules[i].Version != rules[j].Version {
			return rules[i].Version < rules[j].Version
		}
		return rules[i].ID < rules[j].ID
	})
	return rules, nil
}

func (store *MemoryStore) CurrentSkillVersion(_ context.Context, sourceID int64) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	version := 1
	for _, rule := range store.rules {
		if rule.SourceID == sourceID && rule.Version > version {
			version = rule.Version
		}
	}
	return version, nil
}

func cloneSections(sections []Section) []Section {
	return append([]Section(nil), sections...)
}
