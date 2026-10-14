package semantic

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"semantic-crawler/internal/models"
)

func newReviewTestStore(t *testing.T) (*gorm.DB, *GormStore) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "review.db")+"?_busy_timeout=50"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.SemanticCandidate{}, &models.SemanticReview{}, &models.SemanticSkillRule{}); err != nil {
		t.Fatal(err)
	}
	return db, NewGormStore(db)
}

func TestReviewActionsRollBackWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	actions := map[string]func(*ReviewService, int64, int64) error{
		"approve": func(service *ReviewService, id, _ int64) error {
			_, err := service.Approve(ctx, id, "reviewer")
			return err
		},
		"edit_approve": func(service *ReviewService, id, _ int64) error {
			_, err := service.EditAndApprove(ctx, id, "reviewer", []Section{{BlockID: "b0001", Content: "<p>Edited</p>"}}, "edit")
			return err
		},
		"remove_selected_approve": func(service *ReviewService, id, _ int64) error {
			_, err := service.RemoveSelectedAndApprove(ctx, id, "reviewer", []string{"b0002"}, "remove")
			return err
		},
		"reject": func(service *ReviewService, id, _ int64) error {
			return service.Reject(ctx, id)
		},
		"escalate": func(service *ReviewService, id, _ int64) error {
			return service.Escalate(ctx, id)
		},
		"retry": func(service *ReviewService, id, _ int64) error {
			_, _, err := service.RetryWithInstruction(ctx, id, "reviewer", "retain tables", RuleScopeSource)
			return err
		},
		"disable_rule": func(service *ReviewService, _, ruleID int64) error {
			return service.DisableRule(ctx, ruleID, "reviewer")
		},
	}
	for name, action := range actions {
		t.Run(name, func(t *testing.T) {
			db, store := newReviewTestStore(t)
			candidate, err := store.SaveCandidate(ctx, Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview, Sections: []Section{{BlockID: "b0001", Content: "<p>Keep</p>"}, {BlockID: "b0002", Content: "<p>Drop</p>"}}})
			if err != nil {
				t.Fatal(err)
			}
			rule, err := store.AddRule(ctx, SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "existing rule", Version: 2, Status: "active"})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("CREATE TRIGGER fail_review BEFORE INSERT ON semantic_reviews BEGIN SELECT RAISE(ABORT, 'forced audit write failure'); END").Error; err != nil {
				t.Fatal(err)
			}
			if err := action(NewReviewService(store), candidate.ID, rule.ID); err == nil {
				t.Fatal("expected audit failure")
			}
			stored, err := store.Candidate(ctx, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != CandidatePendingReview || stored.ApprovedAt != nil || len(stored.Sections) != 2 {
				t.Errorf("failed action changed original candidate: %#v", stored)
			}
			candidates, err := store.CandidatesByStatus(ctx)
			if err != nil || len(candidates) != 1 {
				t.Errorf("failed action left a revision: candidates=%#v err=%v", candidates, err)
			}
			rules, err := store.Rules(ctx, 1, "https://example.test/another")
			if err != nil || len(rules) != 1 || rules[0].ID != rule.ID {
				t.Errorf("failed action changed active rules: rules=%#v err=%v", rules, err)
			}
		})
	}
}

func TestRetryWithInstructionRollsBackRuleWhenCandidateSaveFails(t *testing.T) {
	db, store := newReviewTestStore(t)
	ctx := context.Background()
	original, err := store.SaveCandidate(ctx, Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER fail_candidate BEFORE INSERT ON semantic_candidates BEGIN SELECT RAISE(ABORT, 'forced candidate write failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewReviewService(store).RetryWithInstruction(ctx, original.ID, "reviewer", "drop all tables", RuleScopeSource); err == nil {
		t.Fatal("expected retry save failure")
	}
	rules, err := store.Rules(ctx, 1, "https://example.test/another")
	if err != nil || len(rules) != 0 {
		t.Fatalf("failed retry changed extraction for other pages: rules=%#v err=%v", rules, err)
	}
}

func TestEditedApprovalRollsBackWhenSupersessionFails(t *testing.T) {
	db, store := newReviewTestStore(t)
	ctx := context.Background()
	original, err := store.SaveCandidate(ctx, Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER fail_supersession BEFORE UPDATE OF status ON semantic_candidates WHEN NEW.status = 'superseded' BEGIN SELECT RAISE(ABORT, 'forced supersession failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := NewReviewService(store).EditAndApprove(ctx, original.ID, "reviewer", []Section{{BlockID: "b0001", Content: "<p>Edited</p>"}}, "edit"); err == nil {
		t.Fatal("expected supersession failure")
	}
	candidates, err := store.CandidatesByStatus(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].Status != CandidatePendingReview {
		t.Fatalf("failed supersession left an approved revision: candidates=%#v err=%v", candidates, err)
	}
}

func TestConcurrentReviewActionsCannotApproveARejectedCandidate(t *testing.T) {
	db, store := newReviewTestStore(t)
	ctx := context.Background()
	original, err := store.SaveCandidate(ctx, Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview})
	if err != nil {
		t.Fatal(err)
	}
	firstRead := make(chan struct{})
	releaseRead := make(chan struct{})
	var reads atomic.Int32
	if err := db.Callback().Query().After("gorm:query").Register("hold_first_candidate_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "semantic_candidates" && reads.Add(1) == 1 {
			close(firstRead)
			<-releaseRead
		}
	}); err != nil {
		t.Fatal(err)
	}
	service := NewReviewService(store)
	approval := make(chan error, 1)
	go func() {
		_, err := service.Approve(ctx, original.ID, "reviewer")
		approval <- err
	}()
	select {
	case <-firstRead:
	case <-time.After(2 * time.Second):
		close(releaseRead)
		t.Fatal("approval did not read candidate")
	}
	rejectErr := service.Reject(ctx, original.ID)
	close(releaseRead)
	approveErr := <-approval
	if rejectErr == nil && approveErr == nil {
		t.Fatal("both stale review actions succeeded; rejection was overwritten by approval")
	}
	stored, err := store.Candidate(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rejectErr == nil && stored.Status != CandidateRejected {
		t.Fatalf("successful rejection was overwritten: %#v", stored)
	}
}

type auditFailMemoryStore struct{ *MemoryStore }

func (store *auditFailMemoryStore) AddReview(context.Context, Review) error {
	return fmt.Errorf("forced audit write failure")
}

func (store *auditFailMemoryStore) WithReviewTransaction(ctx context.Context, action func(ReviewRepository) error) error {
	return store.MemoryStore.WithReviewTransaction(ctx, func(repository ReviewRepository) error {
		return action(&auditFailMemoryStore{MemoryStore: repository.(*MemoryStore)})
	})
}

func TestMemoryReviewActionRollsBackWhenAuditFails(t *testing.T) {
	store := &auditFailMemoryStore{MemoryStore: NewMemoryStore()}
	original := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview})
	if _, err := NewReviewService(store).Approve(context.Background(), original.ID, "reviewer"); err == nil {
		t.Fatal("expected audit failure")
	}
	stored, err := store.Candidate(context.Background(), original.ID)
	if err != nil || stored.Status != CandidatePendingReview || stored.ApprovedAt != nil {
		t.Fatalf("failed memory approval changed candidate: %#v %v", stored, err)
	}
}
