package semantic

import (
	"github.com/PuerkitoBio/goquery"
	"strings"
)

type ReviewerLearningEventKind string

const (
	ReviewerLearningEventNoRule              ReviewerLearningEventKind = "no_rule"
	ReviewerLearningEventDropRule            ReviewerLearningEventKind = "drop_rule"
	ReviewerLearningEventExplicitInstruction ReviewerLearningEventKind = "explicit_instruction"
	ReviewerLearningEventNeedsHumanDecision  ReviewerLearningEventKind = "needs_human_decision"
)

type ReviewerLearningEventInput struct {
	Action          string
	Instruction     string
	Scope           RuleScope
	RemovedSections []Section
	EditedSections  []Section
}

type ReviewerLearningEvent struct {
	Kind             ReviewerLearningEventKind `json:"kind"`
	RecommendedScope RuleScope                 `json:"recommended_scope"`
	Instruction      string                    `json:"instruction"`
	Risky            bool                      `json:"risky"`
	Reason           string                    `json:"reason"`
}

func AnalyzeReviewerLearningEvent(input ReviewerLearningEventInput) ReviewerLearningEvent {
	switch strings.TrimSpace(input.Action) {
	case "retry":
		scope := input.Scope
		if scope != RuleScopeSource && scope != RuleScopeURL {
			scope = RuleScopeURL
		}
		return ReviewerLearningEvent{
			Kind:             ReviewerLearningEventExplicitInstruction,
			RecommendedScope: scope,
			Instruction:      strings.TrimSpace(input.Instruction),
			Reason:           "reviewer supplied an explicit retry instruction",
		}
	case "remove_selected_approve":
		return analyzeRemovedSections(input.RemovedSections)
	case "edit_approve":
		return ReviewerLearningEvent{
			Kind:             ReviewerLearningEventNeedsHumanDecision,
			RecommendedScope: RuleScopeURL,
			Risky:            true,
			Reason:           "manual structured edits need reviewer confirmation before becoming reusable rules",
		}
	case "approve":
		return ReviewerLearningEvent{Kind: ReviewerLearningEventNoRule, Reason: "approval confirms current extraction and active rules"}
	default:
		return ReviewerLearningEvent{Kind: ReviewerLearningEventNoRule, Reason: "review action does not imply a reusable rule"}
	}
}

func analyzeRemovedSections(sections []Section) ReviewerLearningEvent {
	if len(sections) == 0 {
		return ReviewerLearningEvent{Kind: ReviewerLearningEventNoRule, Reason: "no removed sections"}
	}
	for _, section := range sections {
		if section.Type == TypeCritical || section.Type == TypeWarning || section.Role == RoleInfoBox || section.Role == RoleTable {
			return ReviewerLearningEvent{
				Kind:             ReviewerLearningEventNeedsHumanDecision,
				RecommendedScope: RuleScopeURL,
				Risky:            true,
				Reason:           "removed warning, information, or table content must stay page-scoped unless a human promotes it",
			}
		}
	}
	if allRemovedSectionsAreBoilerplate(sections) {
		return ReviewerLearningEvent{
			Kind:             ReviewerLearningEventDropRule,
			RecommendedScope: RuleScopeSource,
			Instruction:      "For this source: drop share, print, email, copy-link, and page utility controls.",
			Reason:           "removed sections match repeated UI boilerplate",
		}
	}
	return ReviewerLearningEvent{
		Kind:             ReviewerLearningEventNeedsHumanDecision,
		RecommendedScope: RuleScopeURL,
		Risky:            true,
		Reason:           "removed content does not match a proven safe source-wide pattern",
	}
}

func allRemovedSectionsAreBoilerplate(sections []Section) bool {
	if len(sections) == 0 {
		return false
	}
	labels := []string{"tipsa & dela artikeln", "share this article", "print this page", "tipsa & dela", "kopiera länk", "skriv ut sidan", "dela via mejl", "copy link", "share", "print"}
	for _, section := range sections {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(section.Content))
		if err != nil {
			return false
		}
		if section.Role != RoleNavigation && doc.Find("a,button,input").Length() == 0 {
			return false
		}
		text := strings.ToLower(normalizeText(doc.Text()))
		if text == "" {
			return false
		}
		// Match complete utility labels, including a group of such controls. A
		// substantive suffix must never qualify merely because it contains a verb.
		for text != "" {
			matched := false
			for _, label := range labels {
				if text == label || strings.HasPrefix(text, label+" ") {
					text = strings.TrimSpace(strings.TrimPrefix(text, label))
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}
