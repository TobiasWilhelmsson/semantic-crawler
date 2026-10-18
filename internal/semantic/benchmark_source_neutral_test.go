package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeSemanticGoldRetainsSubstantiveTextBesideControls(t *testing.T) {
	for _, text := range []string{
		"Ansök senast 31 oktober. Skriv ut sidan och skicka blanketten.",
		"Avgiften är 250 kronor. Kopiera länk till kvittot i din ansökan.",
		"Skriv ut sidan och skriv under ansökan.",
		"Tipsa & dela föreningens beslut före årsmötet.",
	} {
		raw := BenchmarkSection{BlockID: "municipal-instruction", Role: RoleContent, Text: text}
		normalized := NormalizeSemanticGold([]BenchmarkSection{raw})
		if len(normalized) != 1 || normalized[0].Text != text {
			t.Errorf("discarded human-selected instruction %q: %#v", text, normalized)
		}
	}
	for _, text := range []string{"Skriv ut sidan", "Tipsa & dela | Kopiera länk | Skriv ut sidan | Dela via mejl"} {
		if normalized := NormalizeSemanticGold([]BenchmarkSection{{BlockID: "controls", Text: text}}); len(normalized) != 0 {
			t.Errorf("pure controls retained: %#v", normalized)
		}
	}
}

func TestReviewerFeedbackRetainsMunicipalEvidenceWithoutDomainRestrictions(t *testing.T) {
	for _, tc := range []struct {
		name, category string
		section        BenchmarkSection
	}{
		{"deadline", "missing_warning_info_box", BenchmarkSection{Role: RoleInfoBox, Type: TypeCritical, Text: "Ansök senast 31 oktober. Sena ansökningar avslås."}},
		{"fee table", "missing_table", BenchmarkSection{Role: RoleTable, Text: "Tillstånd Avgift Förnyelse 250 kronor"}},
		{"map", "missing_media", BenchmarkSection{Role: RoleMedia, Text: "Karta över kommunens avgiftszoner"}},
		{"instruction", "missing_content", BenchmarkSection{Role: RoleContent, Text: "Betala avgiften innan du skickar ansökan."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence := reviewerLearningEvidenceWithBlocks([]BenchmarkSection{tc.section}, nil, []CandidateBlock{{ID: "b0042", Text: tc.section.Text}}, BenchmarkMetrics{CriticalRecall: 0, TableRecall: 0, MediaRecall: 0, RelevantTextPrecision: 1})
			for _, item := range evidence {
				if item.Category != tc.category {
					continue
				}
				prompt := promptForEvidence(item)
				if !strings.Contains(prompt, tc.section.Text) || !strings.Contains(prompt, "b0042") {
					t.Errorf("lost concrete baseline evidence: %s", prompt)
				}
				if strings.Contains(strings.ToLower(prompt), "clinical") || strings.Contains(prompt, "dosage") {
					t.Errorf("municipal feedback is restricted to medicine: %s", prompt)
				}
				if tc.category == "missing_warning_info_box" && !strings.Contains(prompt, "type=critical") {
					t.Errorf("lost baseline critical classification: %s", prompt)
				}
				sourcePrompt := promptForEvidence(reviewerLearningPromptEvidence{Category: tc.category, Scope: RuleScopeSource})
				if strings.Contains(strings.ToLower(sourcePrompt), "clinical") || strings.Contains(sourcePrompt, "dosage") {
					t.Errorf("source feedback is restricted to medicine: %s", sourcePrompt)
				}
				return
			}
			t.Fatalf("missing expected feedback category %s: %#v", tc.category, evidence)
		})
	}
	prompt := simulatedInstruction(PageBenchmark{URL: "https://example.test/fees", Metrics: BenchmarkMetrics{CriticalRecall: 0, TableRecall: 0, MediaRecall: 0}})
	if strings.Contains(prompt, "clinical") {
		t.Fatalf("simulated municipal review is restricted to medicine: %s", prompt)
	}
}

func TestReviewerLearningResumeRejectsOldVersionWithoutChangingEvidence(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeAdaptive, ReviewerLearningModeDeep} {
		for _, version := range []string{"semantic-reviewer-learning-deep-v22-domain-neutral-extraction", ""} {
			t.Run(string(mode)+"/"+version, func(t *testing.T) {
				options := reviewerLearningResumeFixture(t, mode)
				if _, err := RunReviewerLearningBenchmark(context.Background(), options); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(options.ReportDirectory, "reviewer-learning.json")
				encoded, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var report map[string]any
				if err := json.Unmarshal(encoded, &report); err != nil {
					t.Fatal(err)
				}
				report["implementation_version"] = version
				encoded, err = json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, encoded, 0600); err != nil {
					t.Fatal(err)
				}
				options.Resume = true
				if _, err := RunReviewerLearningBenchmark(context.Background(), options); err == nil || !strings.Contains(err.Error(), "implementation version") {
					t.Fatalf("old semantics resumed: %v", err)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(encoded, after) {
					t.Fatal("rejected resume overwrote saved evidence")
				}
			})
		}
	}
}

func TestReviewerLearningMixedReferenceAndAdaptiveFeedbackAreSourceNeutral(t *testing.T) {
	mixed := BenchmarkSection{Role: RoleContent, Text: "Ansök senast 31 oktober. Läs mer om kommunens avgifter."}
	evidence := reviewerLearningEvidence([]BenchmarkSection{mixed}, nil, BenchmarkMetrics{CriticalRecall: 1, TableRecall: 1, MediaRecall: 1, RelevantTextPrecision: 1})
	if len(evidence) != 1 || evidence[0].Category != "missing_mixed_clinical_reference" {
		t.Errorf("mixed baseline reference not recognized: %#v", evidence)
	}
	for _, section := range []BenchmarkSection{
		{Role: RoleInfoBox, Type: TypeCritical, Text: "Ansök senast 31 oktober."},
		{Role: RoleTable, Text: "Tillstånd Avgift 250 kronor"},
		{Role: RoleMedia, Text: "Karta över kommunens avgiftszoner"},
		mixed,
	} {
		prompt, _ := generateReviewerLearningPromptAvoiding("https://example.test/fees", []BenchmarkSection{section}, nil, BenchmarkMetrics{CriticalRecall: 0, TableRecall: 0, MediaRecall: 0, RelevantTextPrecision: 1}, nil)
		if strings.Contains(prompt, "clinical") || strings.Contains(prompt, "dosage") {
			t.Errorf("adaptive feedback is restricted to medicine: %s", prompt)
		}
	}
	if extra := extraActualEvidence(nil, []BenchmarkSection{mixed, {Role: RoleContent, Text: "Skriv ut sidan och skicka blanketten senast 31 oktober."}}); len(extra) != 0 {
		t.Errorf("substantive paragraphs mislabeled as pure boilerplate: %#v", extra)
	}
}

func TestNormalizeSemanticGoldPreservesPunctuationWithoutControlLabels(t *testing.T) {
	for _, text := range []string{"...", "§", "—", "|"} {
		normalized := NormalizeSemanticGold([]BenchmarkSection{{BlockID: "literal", Role: RoleContent, Text: text}})
		if len(normalized) != 1 || normalized[0].Text != text {
			t.Errorf("literal reference text was treated as a UI control: %q => %#v", text, normalized)
		}
	}
}
