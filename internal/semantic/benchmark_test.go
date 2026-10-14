package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateBenchmarkReportsParityRecallPrecisionAndCriticalCoverage(t *testing.T) {
	gold := []BenchmarkSection{
		{BlockID: "g1", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Kontrollera patientens identitet."},
		{BlockID: "g2", Role: RoleInfoBox, Type: TypeCritical, ParseMode: false, Text: "Avbryt vid allvarlig desaturation."},
		{BlockID: "g3", Role: RoleTable, Type: TypeNone, ParseMode: true, Text: "Sugning Vid behov"},
	}
	actual := []BenchmarkSection{
		{BlockID: "a1", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Kontrollera patientens identitet."},
		{BlockID: "a2", Role: RoleInfoBox, Type: TypeCritical, ParseMode: false, Text: "Avbryt vid allvarlig desaturation."},
		{BlockID: "a3", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Relaterade länkar"},
	}

	metrics := EvaluateBenchmark(gold, actual)
	if metrics.RelevantTextRecall <= 0.6 || metrics.RelevantTextRecall >= 1 {
		t.Fatalf("unexpected recall: %#v", metrics)
	}
	if metrics.RelevantTextPrecision >= 1 || metrics.CriticalRecall != 1 || metrics.TableRecall != 0 {
		t.Fatalf("unexpected precision or category recall: %#v", metrics)
	}
	if metrics.ExactParity {
		t.Fatal("structurally different results must not have exact parity")
	}
}

func TestEvaluateBenchmarkDoesNotDoubleCountOverlappingHumanSelectors(t *testing.T) {
	gold := []BenchmarkSection{
		{BlockID: "g1", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Alpha beta gamma delta."},
		{BlockID: "g2", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Alpha beta."},
		{BlockID: "g3", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Gamma delta."},
	}
	actual := []BenchmarkSection{
		{BlockID: "a1", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Alpha beta."},
		{BlockID: "a2", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Gamma delta."},
	}

	metrics := EvaluateBenchmark(gold, actual)
	if metrics.RelevantTextF1 < 0.98 || metrics.RelevantTextRecall < 0.98 || metrics.RelevantTextPrecision < 0.98 {
		t.Fatalf("overlapping human selector text should compare by covered content, not duplicated selector shape: %#v", metrics)
	}
	if metrics.ExactParity {
		t.Fatal("split semantic sections should not be reported as exact structural parity")
	}
}

func TestReviewerLearningEvidenceIgnoresMergedGoldWhenActualBlocksCoverContent(t *testing.T) {
	gold := []BenchmarkSection{
		{BlockID: "g1", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Alpha beta gamma delta."},
		{BlockID: "g2", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Alpha beta."},
		{BlockID: "g3", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Gamma delta."},
	}
	actual := []BenchmarkSection{
		{BlockID: "a1", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Alpha beta."},
		{BlockID: "a2", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Gamma delta."},
	}

	evidence := reviewerLearningEvidence(gold, actual, EvaluateBenchmark(gold, actual))
	if len(evidence) != 0 {
		t.Fatalf("covered overlapping gold should not generate impossible missing-content prompts: %#v", evidence)
	}
}

func TestReviewerLearningEvidenceTargetsCoveredCriticalTypeMismatch(t *testing.T) {
	gold := []BenchmarkSection{{
		BlockID:   "warning",
		Role:      RoleInfoBox,
		Type:      TypeCritical,
		ParseMode: false,
		Text:      "Lågt blodtryck är ett relativt sent tecken på cirkulationssvikt.",
	}}
	actual := []BenchmarkSection{{
		BlockID:   "b0039",
		Role:      RoleInfoBox,
		Type:      TypeInfo,
		ParseMode: false,
		Text:      "Lågt blodtryck är ett relativt sent tecken på cirkulationssvikt.",
	}}

	evidence := reviewerLearningEvidence(gold, actual, EvaluateBenchmark(gold, actual))
	if len(evidence) == 0 || evidence[0].Category != "critical_type_mismatch" {
		t.Fatalf("covered critical text with non-critical type should produce targeted critical type evidence: %#v", evidence)
	}
	if evidence[0].Scope != RuleScopeURL || len(evidence[0].BlockIDs) != 1 || evidence[0].BlockIDs[0] != "b0039" {
		t.Fatalf("critical type mismatch should target the actual page block: %#v", evidence[0])
	}
	prompt := promptForEvidence(evidence[0])
	if !strings.Contains(prompt, "b0039") || !strings.Contains(prompt, "type=critical") || !strings.Contains(prompt, "Lågt blodtryck") {
		t.Fatalf("critical type mismatch prompt should be concrete and block-specific: %s", prompt)
	}
}

func TestNormalizeSemanticGoldDropsDuplicateSelectorContainerButKeepsClinicalSections(t *testing.T) {
	raw := []BenchmarkSection{
		{BlockID: "article-header", Role: RoleHeader, Type: TypeNone, ParseMode: true, Text: "ABCDE - Översikt"},
		{BlockID: "article-introduction", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Clinical introduction text."},
		{BlockID: "info-warning", Role: RoleInfoBox, Type: TypeCritical, ParseMode: false, Text: "Prioritera alltid din egen säkerhet."},
		{BlockID: "main-container", Role: RoleContent, Type: TypeNone, ParseMode: false, Text: "ABCDE - Översikt Clinical introduction text. Prioritera alltid din egen säkerhet. Tipsa & dela artikeln Kopiera länk Skriv ut sidan Hitta på sidan"},
	}

	normalized := NormalizeSemanticGold(raw)
	if len(normalized) != 3 {
		t.Fatalf("semantic gold should remove duplicate container only, got %#v", normalized)
	}
	if containsBenchmarkBlock(normalized, "main-container") {
		t.Fatalf("duplicate selector container should not remain in semantic gold: %#v", normalized)
	}
	if !containsBenchmarkBlock(normalized, "info-warning") {
		t.Fatalf("critical warning must be preserved: %#v", normalized)
	}
	metrics := EvaluateBenchmark(normalized, normalized)
	if !metrics.ExactParity || metrics.CriticalRecall != 1 {
		t.Fatalf("normalized semantic gold should retain perfect self-parity and clinical coverage: %#v", metrics)
	}
}

func TestNormalizeSemanticGoldCleansMediaCarouselSelectorArtifacts(t *testing.T) {
	raw := []BenchmarkSection{{
		BlockID:   "media-haklyft",
		Role:      RoleMediaGroup,
		Type:      TypeNone,
		ParseMode: false,
		Text:      `Bild 1 <img src="/globalassets/vhb/media/bilder/haklyft1.jpg?saved=2018-09-27+09:30" alt="Bild som visar haklyft" />Bild 1/2Haklyft. Lägg din ena hand på patientens panna.Visa nästa bildVisa föregående bild`,
	}}

	normalized := NormalizeSemanticGold(raw)
	if len(normalized) != 1 {
		t.Fatalf("expected one normalized media section, got %#v", normalized)
	}
	text := normalized[0].Text
	for _, forbidden := range []string{"<img", "src", "globalassets", "saved", "Visa nästa", "Visa föregående", "Bild 1/2"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("media selector artifact %q should be removed from semantic gold: %q", forbidden, text)
		}
	}
	if !strings.Contains(text, "Bild som visar haklyft") || !strings.Contains(text, "Haklyft. Lägg din ena hand") {
		t.Fatalf("semantic media gold should keep alt text and clinical caption: %q", text)
	}
}

func TestEvaluateBenchmarkMediaRecallAllowsSemanticCaptionSplitButRequiresMediaBlock(t *testing.T) {
	gold := []BenchmarkSection{{
		BlockID:   "media-haklyft",
		Role:      RoleMediaGroup,
		Type:      TypeNone,
		ParseMode: false,
		Text:      "Bild som visar haklyft Haklyft. Lägg din ena hand på patientens panna.",
	}}
	actualSplit := []BenchmarkSection{
		{BlockID: "image", Role: RoleMedia, Type: TypeNone, ParseMode: false, Text: "Bild som visar haklyft"},
		{BlockID: "caption", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Haklyft. Lägg din ena hand på patientens panna."},
	}
	if metrics := EvaluateBenchmark(gold, actualSplit); metrics.MediaRecall != 1 {
		t.Fatalf("media recall should allow semantic split across media and caption blocks when media exists: %#v", metrics)
	}
	actualNoMedia := []BenchmarkSection{
		{BlockID: "caption", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Bild som visar haklyft Haklyft. Lägg din ena hand på patientens panna."},
	}
	if metrics := EvaluateBenchmark(gold, actualNoMedia); metrics.MediaRecall != 0 {
		t.Fatalf("media recall should still require an actual media block: %#v", metrics)
	}
}

func TestEvaluateBenchmarkMediaRecallAcceptsShortVisibleCaptionForLongAltText(t *testing.T) {
	gold := []BenchmarkSection{
		{BlockID: "image-news", Role: RoleMedia, Type: TypeNone, Text: "Bidl som visar Åtgärdsskala enligt NEWS"},
		{BlockID: "image-nrs", Role: RoleMedia, Type: TypeNone, Text: "Numerisk skala för smärtskattning där 0 innebär smärtfrihet och 10 innebär värsta tänkbara smärta"},
	}
	actual := []BenchmarkSection{
		{BlockID: "actual-news", Role: RoleMedia, Type: TypeNone, Text: "Förstora bilden Bild 3. Åtgärdsskala enligt NEWS."},
		{BlockID: "actual-nrs", Role: RoleMedia, Type: TypeNone, Text: "Numerisk skala (NRS)."},
	}

	metrics := EvaluateBenchmark(gold, actual)

	if metrics.MediaRecall != 1 {
		t.Fatalf("short visible captions should satisfy media recall when they identify the same instructional media: %#v", metrics)
	}
}

func TestEvaluateBenchmarkMediaRecallIgnoresGluedImageControlText(t *testing.T) {
	gold := []BenchmarkSection{{
		BlockID: "image-thorax", Role: RoleMedia, Type: TypeNone, Text: "Bild som visar normal anatomi thorax",
	}}
	actual := []BenchmarkSection{{
		BlockID: "actual-thorax", Role: RoleMedia, Type: TypeNone, Text: "Förstora bildenAnatomi lungor.",
	}}

	metrics := EvaluateBenchmark(gold, actual)

	if metrics.MediaRecall != 1 {
		t.Fatalf("glued image UI controls should not hide matching media evidence: %#v", metrics)
	}
}

func TestEvaluateBenchmarkIgnoresCitationReferencesAndImageControls(t *testing.T) {
	gold := []BenchmarkSection{
		{Role: RoleContent, Text: "Om tecken till försämring upptäcks tidigt ökar möjligheterna att vända sjukdomsförloppet."},
		{Role: RoleMedia, Text: "Bild som visar National Early Warning Score"},
	}
	actual := []BenchmarkSection{
		{Role: RoleContent, Text: "Om tecken till försämring upptäcks tidigt ökar möjligheterna att vända sjukdomsförloppet [15,16]."},
		{Role: RoleMedia, Text: "Förstora bilden Bild 1. National Early Warning Score."},
	}

	metrics := EvaluateBenchmark(gold, actual)

	if metrics.RelevantTextPrecision != 1 || metrics.RelevantTextRecall != 1 {
		t.Fatalf("citation references and image controls should not lower semantic text scoring: %#v", metrics)
	}
}

func TestEvaluateBenchmarkIgnoresRelatedInformationPointersInsideClinicalBlocks(t *testing.T) {
	gold := []BenchmarkSection{{
		Role: RoleInfoBox, Type: TypeInfo, Text: "Denna text gäller vuxna. För barn finns specifika riktlinjer.",
	}}
	actual := []BenchmarkSection{{
		Role: RoleInfoBox, Type: TypeInfo, Text: "Denna text gäller vuxna. För barn finns specifika riktlinjer. Länk till vårdprogram för barn med trakealkanyl finns på sidan Relaterad information.",
	}}

	metrics := EvaluateBenchmark(gold, actual)

	if metrics.RelevantTextPrecision != 1 || metrics.RelevantTextRecall != 1 {
		t.Fatalf("related-information pointer sentence should not lower semantic text scoring: %#v", metrics)
	}
}

func TestSoftReviewCandidateRequiresClinicalCoverage(t *testing.T) {
	ok := BenchmarkMetrics{RelevantTextF1: 0.951, CriticalRecall: 1, TableRecall: 1, MediaRecall: 1, BoilerplateRate: 0.01}
	if !reviewerLearningSoftReviewCandidate(ok) {
		t.Fatalf("high F1 with full clinical coverage should be a soft review candidate")
	}
	missingWarning := BenchmarkMetrics{RelevantTextF1: 0.951, CriticalRecall: 0, TableRecall: 1, MediaRecall: 1}
	if reviewerLearningSoftReviewCandidate(missingWarning) {
		t.Fatalf("0.95 F1 must not pass soft review when critical warnings are missing")
	}
}

func TestReviewerLearningPassRequiresClinicalCoverage(t *testing.T) {
	ok := BenchmarkMetrics{RelevantTextF1: 0.99, CriticalRecall: 1, TableRecall: 1, MediaRecall: 1}
	if !reviewerLearningPasses(ok, 0.98) {
		t.Fatalf("high F1 with full clinical coverage should pass")
	}
	missingWarning := BenchmarkMetrics{RelevantTextF1: 0.999, CriticalRecall: 0, TableRecall: 1, MediaRecall: 1}
	if reviewerLearningPasses(missingWarning, 0.98) {
		t.Fatalf("high F1 must not pass strict/review-ready gates when critical recall is missing")
	}
	exact := BenchmarkMetrics{ExactParity: true, RelevantTextF1: 1}
	if !reviewerLearningPasses(exact, 0.98) {
		t.Fatalf("exact parity should pass")
	}
}

func TestReviewerLearningEvidenceIgnoresNormalizedRelatedInformationLink(t *testing.T) {
	gold := []BenchmarkSection{{Role: RoleContent, Text: "Klinisk huvudtext om NEWS."}}
	actual := []BenchmarkSection{
		{Role: RoleContent, Text: "Klinisk huvudtext om NEWS."},
		{Role: RoleContent, Text: "Länk till obstetrisk NEWS finns på sidan Relaterad information."},
	}

	metrics := EvaluateBenchmark(gold, actual)
	evidence := reviewerLearningEvidence(gold, actual, metrics)

	if metrics.RelevantTextPrecision != 1 || len(evidence) != 0 {
		t.Fatalf("normalized related-information links should not consume reviewer turns, metrics=%#v evidence=%#v", metrics, evidence)
	}
}

func TestReviewerLearningEvidencePrioritizesCriticalCoverageBeforeGenericMissingContent(t *testing.T) {
	gold := []BenchmarkSection{
		{Role: RoleContent, Type: TypeNone, Text: "Systoliskt blodtryck Mät patientens blodtryck."},
		{Role: RoleInfoBox, Type: TypeCritical, Text: "Vid all bedömning av medvetandegrad bör sederade patienter bedömas särskilt."},
	}
	actual := []BenchmarkSection{
		{BlockID: "b0031", Role: RoleInfoBox, Type: TypeNone, Text: "Vid all bedömning av medvetandegrad bör sederade patienter bedömas särskilt."},
	}

	evidence := reviewerLearningEvidence(gold, actual, BenchmarkMetrics{RelevantTextPrecision: 0.98, RelevantTextRecall: 0.9, CriticalRecall: 0, TableRecall: 1, MediaRecall: 1})

	if len(evidence) == 0 || evidence[0].Category != "critical_type_mismatch" {
		t.Fatalf("critical coverage failure should be prompted before generic missing content, got %#v", evidence)
	}
}

func TestReviewerLearningEvidenceKeepsMixedClinicalParagraphWithInlineReference(t *testing.T) {
	gold := []BenchmarkSection{{
		Role: RoleContent,
		Text: "Vid allvarliga kliniska situationer är det primära att få överblick över patientens tillstånd. Läs mer i Sample Sources texter om ABCDE.",
	}}
	actual := []BenchmarkSection{{Role: RoleContent, Text: "Åtgärdsskalan anger vilken kompetens som behövs."}}

	evidence := reviewerLearningEvidence(gold, actual, BenchmarkMetrics{RelevantTextRecall: 0.8, RelevantTextPrecision: 1, CriticalRecall: 1, TableRecall: 1, MediaRecall: 1})

	if len(evidence) == 0 || evidence[0].Category != "missing_mixed_clinical_reference" || evidence[0].Scope != RuleScopeSource {
		t.Fatalf("missing clinical paragraph with inline reference should create source-scoped evidence, got %#v", evidence)
	}
	prompt := promptForEvidence(evidence[0])
	if !strings.Contains(strings.ToLower(prompt), "inline") || !strings.Contains(strings.ToLower(prompt), "clinical") {
		t.Fatalf("prompt should teach mixed clinical/reference paragraph handling, got %q", prompt)
	}
}

func containsBenchmarkBlock(sections []BenchmarkSection, blockID string) bool {
	for _, section := range sections {
		if section.BlockID == blockID {
			return true
		}
	}
	return false
}

func TestWriteBenchmarkReportsCreatesMachineAndHumanReadableArtifacts(t *testing.T) {
	directory := t.TempDir()
	report := BenchmarkReport{
		FixtureSet:             "held-out",
		ModelMode:              "fixture",
		UsageNote:              "Fixture model: no LLM call, no token usage.",
		Aggregate:              BenchmarkMetrics{RelevantTextRecall: 1, RelevantTextPrecision: 1, RelevantTextF1: 1, CriticalRecall: 1, ExactParity: true},
		AfterFeedbackAggregate: BenchmarkMetrics{RelevantTextRecall: 1, RelevantTextPrecision: 1, RelevantTextF1: 1},
		ReviewerSeconds:        12,
		ModelInputTokens:       100,
		ModelOutputTokens:      20,
		UsableGoldPages:        1,
		Pages:                  []PageBenchmark{{URL: "https://example.test/a", BaselineStatus: BaselineValidGold, Metrics: BenchmarkMetrics{RelevantTextRecall: 1, RelevantTextPrecision: 1, CriticalRecall: 1, ExactParity: true}}},
	}

	if err := WriteBenchmarkReports(directory, report); err != nil {
		t.Fatalf("write reports: %v", err)
	}
	jsonReport, err := os.ReadFile(filepath.Join(directory, "benchmark.json"))
	if err != nil || !strings.Contains(string(jsonReport), `"fixture_set": "held-out"`) {
		t.Fatalf("missing JSON report: %s %v", jsonReport, err)
	}
	if !strings.Contains(string(jsonReport), `"token_usage_available": false`) || !strings.Contains(string(jsonReport), `"baseline_status": "valid_gold"`) {
		t.Fatalf("JSON report should include usage and baseline status: %s", jsonReport)
	}
	htmlReport, err := os.ReadFile(filepath.Join(directory, "benchmark.html"))
	if err != nil || !strings.Contains(string(htmlReport), "Exact parity") || !strings.Contains(string(htmlReport), "Fixture model: no LLM call") || !strings.Contains(string(htmlReport), "valid_gold") || !strings.Contains(string(htmlReport), "https://example.test/a") {
		t.Fatalf("missing HTML report: %s %v", htmlReport, err)
	}
}
