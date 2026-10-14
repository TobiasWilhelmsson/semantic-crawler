package semantic

import (
	"context"
	"fmt"
	"testing"
)

type recordingDeliveryClient struct{}

func (*recordingDeliveryClient) Send(_ context.Context, _ string, _ DeliveryPayload) (int, string, error) {
	return 200, `{"ok":true}`, nil
}

func TestReviewServiceCreatesImmutableEditedApproval(t *testing.T) {
	store := NewMemoryStore()
	original := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview, Sections: []Section{{BlockID: "b0001", Content: "Original"}}, Markdown: "Original"})
	service := NewReviewService(store)

	approved, err := service.EditAndApprove(context.Background(), original.ID, "reviewer@example.test", []Section{{BlockID: "b0001", Content: "Edited"}}, "corrected role")
	if err != nil {
		t.Fatalf("edit and approve: %v", err)
	}
	if approved.ID == original.ID || approved.SupersedesID != original.ID || approved.Status != CandidateApproved {
		t.Fatalf("expected immutable approved revision, got %#v", approved)
	}
	storedOriginal, _ := store.Candidate(context.Background(), original.ID)
	if storedOriginal.Status != CandidateSuperseded || storedOriginal.Sections[0].Content != "Original" {
		t.Fatalf("original history was overwritten: %#v", storedOriginal)
	}
	if approved.Markdown == "Original" || approved.Markdown == "" {
		t.Fatalf("edited approval did not regenerate markdown: %#v", approved)
	}
}

func TestReviewServiceCreatesVersionedSourceRuleWithoutRewritingApprovedHistory(t *testing.T) {
	store := NewMemoryStore()
	approved := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/approved", Status: CandidateApproved, SkillVersion: 1})
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/pending", Status: CandidatePendingReview, SkillVersion: 1})
	service := NewReviewService(store)

	rule, rerun, err := service.RetryWithInstruction(context.Background(), pending.ID, "reviewer", "Always retain dosage tables", RuleScopeSource)
	if err != nil {
		t.Fatalf("retry with instruction: %v", err)
	}
	if rule.Version != 2 || rule.Scope != RuleScopeSource || rerun.Status != CandidateQueued || rerun.SkillVersion != 2 {
		t.Fatalf("unexpected rule or rerun: %#v %#v", rule, rerun)
	}
	storedApproved, _ := store.Candidate(context.Background(), approved.ID)
	if storedApproved.Status != CandidateApproved || storedApproved.SkillVersion != 1 {
		t.Fatalf("approved history changed after source rule: %#v", storedApproved)
	}
	rules, err := service.ApplicableRules(context.Background(), 1, "https://example.test/another")
	if err != nil || len(rules) != 1 || rules[0].Instruction != "Always retain dosage tables" {
		t.Fatalf("source rule did not apply to another page: %#v %v", rules, err)
	}
}

func TestReviewServiceKeepsURLScopedRuleOnExactPageOnly(t *testing.T) {
	store := NewMemoryStore()
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/feet-warning", Status: CandidatePendingReview, SkillVersion: 1})
	service := NewReviewService(store)
	instruction := "only keep this warning box when text includes a note about feet"

	rule, _, err := service.RetryWithInstruction(context.Background(), pending.ID, "reviewer", instruction, RuleScopeURL)
	if err != nil {
		t.Fatalf("retry with url instruction: %v", err)
	}
	if rule.Scope != RuleScopeURL || rule.OriginURL != pending.URL {
		t.Fatalf("expected URL-scoped rule with origin URL: %#v", rule)
	}
	exactRules, err := service.ApplicableRules(context.Background(), 1, pending.URL)
	if err != nil || len(exactRules) != 1 || exactRules[0].Instruction != instruction {
		t.Fatalf("URL rule did not apply to exact page: %#v %v", exactRules, err)
	}
	otherRules, err := service.ApplicableRules(context.Background(), 1, "https://example.test/another-page")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherRules) != 0 {
		t.Fatalf("URL-scoped rule leaked to another page: %#v", otherRules)
	}
}

func TestReviewerActionLearningClassifiesSafeRepeatedRemovalAsSourceCandidate(t *testing.T) {
	event := AnalyzeReviewerLearningEvent(ReviewerLearningEventInput{
		Action: "remove_selected_approve",
		RemovedSections: []Section{{
			BlockID: "share",
			Role:    RoleNavigation,
			Content: "Tipsa & dela artikeln Kopiera länk Skriv ut sidan",
		}},
	})
	if event.Kind != ReviewerLearningEventDropRule || event.RecommendedScope != RuleScopeSource || event.Risky {
		t.Fatalf("share/print/copy removal should be a safe source-candidate drop rule: %#v", event)
	}
	if event.Instruction == "" || event.Reason == "" {
		t.Fatalf("learning event should be explainable: %#v", event)
	}
}

func TestReviewerActionLearningKeepsClinicalWarningRemovalURLScopedAndRisky(t *testing.T) {
	event := AnalyzeReviewerLearningEvent(ReviewerLearningEventInput{
		Action: "remove_selected_approve",
		RemovedSections: []Section{{
			BlockID: "warning",
			Role:    RoleInfoBox,
			Type:    TypeCritical,
			Content: "Avbryt vid allvarlig desaturation.",
		}},
	})
	if event.Kind != ReviewerLearningEventNeedsHumanDecision || event.RecommendedScope != RuleScopeURL || !event.Risky {
		t.Fatalf("clinical warning removal should not become source-wide automatically: %#v", event)
	}
}

func TestReviewerActionLearningClassifiesRetryPromptAsExplicitInstruction(t *testing.T) {
	event := AnalyzeReviewerLearningEvent(ReviewerLearningEventInput{
		Action:      "retry",
		Instruction: "Keep all dosage tables exactly.",
		Scope:       RuleScopeSource,
	})
	if event.Kind != ReviewerLearningEventExplicitInstruction || event.RecommendedScope != RuleScopeSource || event.Instruction != "Keep all dosage tables exactly." {
		t.Fatalf("retry prompt should be preserved as explicit reviewer instruction: %#v", event)
	}
}

func TestReviewServiceSourceScopedRuleAppliesToLaterPages(t *testing.T) {
	store := NewMemoryStore()
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/feet-warning", Status: CandidatePendingReview, SkillVersion: 1})
	service := NewReviewService(store)
	instruction := "only keep this warning box when text includes a note about feet"

	rule, _, err := service.RetryWithInstruction(context.Background(), pending.ID, "reviewer", instruction, RuleScopeSource)
	if err != nil {
		t.Fatalf("retry with source instruction: %v", err)
	}
	if rule.Scope != RuleScopeSource || rule.OriginURL != "" {
		t.Fatalf("expected source-scoped rule without origin URL: %#v", rule)
	}
	laterRules, err := service.ApplicableRules(context.Background(), 1, "https://example.test/later-page")
	if err != nil || len(laterRules) != 1 || laterRules[0].Instruction != instruction {
		t.Fatalf("source-scoped rule did not apply to later page: %#v %v", laterRules, err)
	}
}

func TestDeliveryServiceRejectsUnapprovedCandidates(t *testing.T) {
	store := NewMemoryStore()
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview})
	delivery := NewDeliveryService(store, &recordingDeliveryClient{})

	if _, err := delivery.Deliver(context.Background(), pending.ID, "https://receiver.test"); err == nil {
		t.Fatal("expected unapproved candidate delivery to be rejected")
	}
}

func TestReviewServiceAuditsActionsAndCanDisableRules(t *testing.T) {
	store := NewMemoryStore()
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview})
	service := NewReviewService(store)

	if _, err := service.Approve(context.Background(), pending.ID, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if len(store.Reviews()) != 1 || store.Reviews()[0].Action != "approve" {
		t.Fatalf("approval was not audited: %#v", store.Reviews())
	}
	rule, err := store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "retain tables", Version: 2, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DisableRule(context.Background(), rule.ID, "reviewer"); err != nil {
		t.Fatal(err)
	}
	rules, _ := store.Rules(context.Background(), 1, "https://example.test/other")
	if len(rules) != 0 {
		t.Fatalf("disabled rule remained active: %#v", rules)
	}
}

func TestDeliveryServicePersistsAttempt(t *testing.T) {
	store := NewMemoryStore()
	approved := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidateApproved, ApprovedBy: "reviewer"})
	now := approved.CreatedAt
	approved.ApprovedAt = &now
	approved, _ = store.SaveCandidate(context.Background(), approved)
	delivery := NewDeliveryService(store, &recordingDeliveryClient{})

	if _, err := delivery.Deliver(context.Background(), approved.ID, "https://receiver.test"); err != nil {
		t.Fatal(err)
	}
	if len(store.Deliveries()) != 1 || store.Deliveries()[0].Status != "succeeded" {
		t.Fatalf("delivery attempt was not persisted: %#v", store.Deliveries())
	}
}

func TestDeliveryServiceRejectsEndpointOutsideAllowlist(t *testing.T) {
	store := NewMemoryStore()
	approved := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidateApproved, ApprovedBy: "reviewer"})
	now := approved.CreatedAt
	approved.ApprovedAt = &now
	approved, _ = store.SaveCandidate(context.Background(), approved)
	delivery := NewRestrictedDeliveryService(store, &recordingDeliveryClient{}, []string{"https://allowed.test/approved"})

	if _, err := delivery.Deliver(context.Background(), approved.ID, "https://evil.test/approved"); err == nil {
		t.Fatal("expected disallowed delivery endpoint to fail")
	}
	if len(store.Deliveries()) != 0 {
		t.Fatalf("disallowed endpoint should not create delivery attempt: %#v", store.Deliveries())
	}
}

func TestReviewServiceRejectsInvalidStatusTransitions(t *testing.T) {
	store := NewMemoryStore()
	approved := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidateApproved})
	superseded := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/b", Status: CandidateSuperseded})
	service := NewReviewService(store)

	if err := service.Reject(context.Background(), approved.ID); err == nil {
		t.Fatal("expected rejecting approved candidate to fail")
	}
	if err := service.Escalate(context.Background(), superseded.ID); err == nil {
		t.Fatal("expected escalating superseded candidate to fail")
	}
	if _, _, err := service.RetryWithInstruction(context.Background(), approved.ID, "reviewer", "retry", RuleScopeURL); err == nil {
		t.Fatal("expected retrying approved candidate to fail")
	}
	storedApproved, _ := store.Candidate(context.Background(), approved.ID)
	if storedApproved.Status != CandidateApproved {
		t.Fatalf("approved status was mutated: %#v", storedApproved)
	}
}

func TestEditAndApproveKeepsOriginalReviewableWhenRevisionSaveFails(t *testing.T) {
	store := &failingSaveStore{MemoryStore: NewMemoryStore(), failNewCandidates: true}
	original := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview, Sections: []Section{{BlockID: "b0001", Content: "Original"}}})
	service := NewReviewService(store)

	if _, err := service.EditAndApprove(context.Background(), original.ID, "reviewer", []Section{{BlockID: "b0001", Content: "Edited"}}, "edit"); err == nil {
		t.Fatal("expected save failure")
	}
	stored, _ := store.Candidate(context.Background(), original.ID)
	if stored.Status != CandidatePendingReview {
		t.Fatalf("original should remain reviewable after failed edit save: %#v", stored)
	}
}

func TestRetryWithInstructionKeepsOriginalReviewableWhenRerunSaveFails(t *testing.T) {
	store := &failingSaveStore{MemoryStore: NewMemoryStore(), failNewCandidates: true}
	original := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview, SkillVersion: 1})
	service := NewReviewService(store)

	if _, _, err := service.RetryWithInstruction(context.Background(), original.ID, "reviewer", "retry carefully", RuleScopeURL); err == nil {
		t.Fatal("expected save failure")
	}
	stored, _ := store.Candidate(context.Background(), original.ID)
	if stored.Status != CandidatePendingReview {
		t.Fatalf("original should remain reviewable after failed retry save: %#v", stored)
	}
}

type failingSaveStore struct {
	*MemoryStore
	failNewCandidates bool
}

func (store *failingSaveStore) SaveCandidate(ctx context.Context, candidate Candidate) (Candidate, error) {
	if store.failNewCandidates && candidate.ID == 0 {
		return Candidate{}, fmt.Errorf("forced save failure")
	}
	return store.MemoryStore.SaveCandidate(ctx, candidate)
}

func (store *failingSaveStore) WithReviewTransaction(ctx context.Context, action func(ReviewRepository) error) error {
	return store.MemoryStore.WithReviewTransaction(ctx, func(repository ReviewRepository) error {
		return action(&failingSaveStore{MemoryStore: repository.(*MemoryStore), failNewCandidates: store.failNewCandidates})
	})
}
