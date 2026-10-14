package semantic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewerLearningRuleIdentityAfterMerge(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "a.html"), []byte(`<main><h1>Title</h1><p>Body clinical paragraph</p></main>`), 0600); err != nil {
		t.Fatal(err)
	}
	r := ReviewerLearningReport{Rules: []ReviewerLearningRule{
		{ID: 1, Version: 1, Scope: RuleScopeSource, Instruction: "same source rule", Status: "active"},
		{ID: 9, Version: 9, Scope: RuleScopeSource, Instruction: "same source rule", Status: "active"},
		{ID: 3, Version: 3, Scope: RuleScopeURL, OriginURL: "https://example.test/old/", Instruction: "old URL instruction", Status: "active"},
	}}
	r.updateReviewerLearningSummary()
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	r = ReviewerLearningReport{}
	if err := json.Unmarshal(encoded, &r); err != nil {
		t.Fatal(err)
	}
	p := GoldPage{URL: "https://example.test/a/", Slug: "a", Identifiers: []GoldIdentifier{{Name: "title", Role: RoleHeader, Container: "h1", ParseMode: true}, {Name: "body", Role: RoleContent, Container: "p", ParseMode: true}}}
	out := filepath.Join(d, "reports")
	lock, err := acquireReviewerLearningLock(out, reviewerLearningRunID(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseReviewerLearningLock(out, lock)
	_, err = runDeepReviewerLearningPage(context.Background(), ReviewerLearningOptions{FixtureDirectory: d, ReportDirectory: out, Model: &blockIDReviewerModel{}, QualityThreshold: 0.99, ReviewReadyThreshold: 0.97, StallWindow: 2, MinImprovement: 0.002}, &r, p, 1, GoldOverrides{}, lock)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, rule := range r.Rules {
		if prev, ok := seen[rule.ID]; ok {
			t.Fatalf("duplicate rule ID %d: %q and %q", rule.ID, prev, rule.Instruction)
		}
		seen[rule.ID] = rule.Instruction
		if rule.ID != 1 && rule.ID != 3 && (rule.ID <= 9 || rule.Version <= 9) {
			t.Fatalf("reused a retired identity: %#v", rule)
		}
	}
}

func TestReviewerLearningMergedRuleReferences(t *testing.T) {
	report := ReviewerLearningReport{
		Rules: []ReviewerLearningRule{
			{ID: 1, Version: 1, Scope: RuleScopeSource, Instruction: "same rule", Status: "active"},
			{ID: 2, Version: 2, Scope: RuleScopeSource, Instruction: "same rule", Status: "active"},
		},
		Pages: []ReviewerLearningPage{{Index: 1, ActiveRuleIDs: []int{1, 2}, ReviewerRuleID: 2, ReviewerTurnDetails: []ReviewerLearningTurn{{Turn: 1, RuleID: 2}}}},
	}
	report.updateReviewerLearningSummary()
	if len(report.Rules) != 1 {
		t.Fatalf("rules not merged: %#v", report.Rules)
	}
	page := report.Pages[0]
	if len(page.ActiveRuleIDs) != 1 || page.ActiveRuleIDs[0] != 1 || page.ReviewerRuleID != 1 || page.ReviewerTurnDetails[0].RuleID != 1 {
		t.Fatalf("references do not identify the retained rule: %#v", page)
	}
}
