package semantic

import (
	"strings"
	"testing"
)

func TestGeneralPromptsPreserveDetailsAcrossDomains(t *testing.T) {
	prompt := buildExtractionPrompt(CrawlConfig{Scope: "permit fees and eligibility"}, PageVersion{URL: "https://example.test"}, nil, nil)
	for _, bad := range []string{"medical facts", "parse_mode="} {
		if strings.Contains(prompt, bad) {
			t.Errorf("domain/output assumption %s", bad)
		}
	}
	for _, want := range []string{"exceptions", "dates", "units", "table"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("missing preservation guidance %s", want)
		}
	}
	guide := defaultSourceSkillTemplate()
	for _, bad := range []string{"RAG", "Clinical", "patient", "contraindications"} {
		if strings.Contains(guide, bad) {
			t.Errorf("default guide assumes %s", bad)
		}
	}
}
func TestReviewerLearningKeepsSubstantiveNonmedicalContent(t *testing.T) {
	for _, text := range []string{"Share capital must be at least 25,000 SEK.", "Print the permit and retain it for seven years.", "Revenue sharing is available except for temporary applicants.", "<p>Share this article</p><p>Pay before 31 October.</p>"} {
		event := AnalyzeReviewerLearningEvent(ReviewerLearningEventInput{Action: "remove_selected_approve", RemovedSections: []Section{{Role: RoleContent, Content: text}}})
		if event.Kind == ReviewerLearningEventDropRule || event.RecommendedScope != RuleScopeURL {
			t.Errorf("substantive content proposed for global removal: %s", text)
		}
	}
}

func TestReviewerLearningDoesNotTreatUtilityWordsAsControlStructure(t *testing.T) {
	e := AnalyzeReviewerLearningEvent(ReviewerLearningEventInput{Action: "remove_selected_approve", RemovedSections: []Section{{Role: RoleHeader, Content: "<h1>Print</h1>"}}})
	if e.Kind == ReviewerLearningEventDropRule {
		t.Fatal("substantive heading treated as control")
	}
}
