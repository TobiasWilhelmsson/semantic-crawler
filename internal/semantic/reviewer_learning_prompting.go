package semantic

import (
	"fmt"
	"strings"
)

type reviewerLearningPromptEvidence struct {
	Category string    `json:"category"`
	Snippet  string    `json:"snippet"`
	BlockIDs []string  `json:"block_ids,omitempty"`
	Scope    RuleScope `json:"scope"`
	Risky    bool      `json:"risky"`
}

func reviewerLearningPromptFromEvidenceWithBlocks(url string, gold, actual []BenchmarkSection, blocks []CandidateBlock, metrics BenchmarkMetrics, used map[string]bool) (string, RuleScope, string) {
	for _, evidence := range reviewerLearningEvidenceWithBlocks(gold, actual, blocks, metrics) {
		prompt := promptForEvidence(evidence)
		if used[prompt] {
			continue
		}
		return prompt, evidence.Scope, evidence.Category
	}
	prompt := fmt.Sprintf("For this page only: compare %s against the human selector baseline and include the remaining missing clinical source blocks while dropping only non-clinical UI boilerplate.", url)
	if !used[prompt] {
		return prompt, RuleScopeURL, "page_specific_remaining_mismatch"
	}
	return fmt.Sprintf("For this page only: retry the remaining mismatch on %s using the exact missing text snippets and extra boilerplate evidence from the previous turn.", url), RuleScopeURL, "duplicate_remaining_mismatch"
}

func reviewerLearningEvidence(gold, actual []BenchmarkSection, metrics BenchmarkMetrics) []reviewerLearningPromptEvidence {
	return reviewerLearningEvidenceWithBlocks(gold, actual, nil, metrics)
}

func reviewerLearningEvidenceWithBlocks(gold, actual []BenchmarkSection, blocks []CandidateBlock, metrics BenchmarkMetrics) []reviewerLearningPromptEvidence {
	var evidence []reviewerLearningPromptEvidence
	if metrics.CriticalRecall < 1 {
		for _, expected := range gold {
			if expected.Type != TypeCritical {
				continue
			}
			if mismatch, ok := criticalTypeMismatchEvidence(expected, actual); ok {
				evidence = append(evidence, mismatch)
				continue
			}
			if sectionTextCoveredByCollection(expected, actual, nil, 0.8) {
				continue
			}
			blockIDs := matchingCandidateBlockIDs(expected, blocks)
			scope := RuleScopeSource
			if len(blockIDs) > 0 {
				scope = RuleScopeURL
			}
			evidence = append(evidence, reviewerLearningPromptEvidence{
				Category: "missing_warning_info_box",
				Snippet:  reviewerLearningSnippet(expected.Text),
				BlockIDs: blockIDs,
				Scope:    scope,
				Risky:    true,
			})
		}
	}
	for _, expected := range gold {
		if expected.Type == TypeCritical {
			continue
		}
		if sectionTextCoveredByCollection(expected, actual, nil, 0.8) {
			continue
		}
		category := "missing_content"
		scope := RuleScopeURL
		risky := false
		snippet := reviewerLearningSnippet(expected.Text)
		switch {
		case clinicalParagraphWithInlineReference(expected.Text):
			category = "missing_mixed_clinical_reference"
			scope = RuleScopeSource
		case expected.Type == TypeCritical:
			category = "missing_warning_info_box"
			scope = RuleScopeSource
			risky = true
		case expected.Role == RoleTable:
			category = "missing_table"
			scope = RuleScopeSource
			risky = true
		case expected.Role == RoleMedia || expected.Role == RoleMediaGroup || expected.Role == RoleVideo:
			category = "missing_media"
			scope = RuleScopeSource
		case expected.Role == RoleMetadata:
			category = "missing_metadata"
			scope = RuleScopeSource
		case expected.Role == RoleNavigation:
			category = "missing_breadcrumb"
			scope = RuleScopeSource
		}
		blockIDs := matchingCandidateBlockIDs(expected, blocks)
		if len(blockIDs) > 0 && category != "missing_mixed_clinical_reference" {
			scope = RuleScopeURL
		}
		evidence = append(evidence, reviewerLearningPromptEvidence{
			Category: category,
			Snippet:  snippet,
			BlockIDs: blockIDs,
			Scope:    scope,
			Risky:    risky,
		})
	}
	if metrics.RelevantTextPrecision < 0.98 {
		evidence = append(evidence, extraActualEvidence(gold, actual)...)
	}
	if metrics.CriticalRecall < 1 && !hasEvidenceCategory(evidence, "missing_warning_info_box") && !hasEvidenceCategory(evidence, "critical_type_mismatch") {
		evidence = append(evidence, reviewerLearningPromptEvidence{Category: "missing_warning_info_box", Snippet: "clinical warning or information box", Scope: RuleScopeSource, Risky: true})
	}
	if metrics.TableRecall < 1 && !hasEvidenceCategory(evidence, "missing_table") {
		evidence = append(evidence, reviewerLearningPromptEvidence{Category: "missing_table", Snippet: "clinical table", Scope: RuleScopeSource, Risky: true})
	}
	return evidence
}

func criticalTypeMismatchEvidence(expected BenchmarkSection, actual []BenchmarkSection) (reviewerLearningPromptEvidence, bool) {
	bestIndex := -1
	bestOverlap := 0.0
	for index, candidate := range actual {
		if candidate.Type == TypeCritical {
			continue
		}
		overlap := textOverlap(expected.Text, candidate.Text)
		if overlap > bestOverlap {
			bestIndex = index
			bestOverlap = overlap
		}
	}
	if bestIndex < 0 || bestOverlap < 0.8 {
		return reviewerLearningPromptEvidence{}, false
	}
	var ids []string
	if actual[bestIndex].BlockID != "" {
		ids = []string{actual[bestIndex].BlockID}
	}
	return reviewerLearningPromptEvidence{
		Category: "critical_type_mismatch",
		Snippet:  reviewerLearningSnippet(expected.Text),
		BlockIDs: ids,
		Scope:    RuleScopeURL,
		Risky:    true,
	}, true
}

func promptForEvidence(evidence reviewerLearningPromptEvidence) string {
	blockHint := exactBlockHint(evidence.BlockIDs)
	target := "For this source"
	if evidence.Scope == RuleScopeURL {
		target = "For this page only"
	}
	if evidence.Scope == RuleScopeSource {
		switch evidence.Category {
		case "missing_mixed_clinical_reference":
			return "For this source: keep paragraphs that contain primary clinical assessment, treatment, action, or care instructions even when the same paragraph also contains inline Läs mer/reference links. Drop only standalone related-link cards or pure navigation link blocks."
		case "critical_type_mismatch":
			return "For this source: highlighted clinical warning/info boxes that describe clinical risk, deterioration, late danger signs, thresholds, urgent action, contraindications, dosage limits, or patient-risk information must be labeled as type=critical, even when the wording does not use explicit emergency verbs."
		case "missing_warning_info_box":
			return "For this source: keep highlighted warning/info boxes when they contain clinical safety instructions, contraindications, dosage limits, urgent-care advice, patient-risk information, or other clinical instructions. Label urgent safety, contraindication, dosage-limit, and patient-risk boxes as type=critical."
		case "missing_table":
			return "For this source: keep every dosage, parameter, equipment, observation, or clinical instruction table exactly as shown."
		case "missing_media":
			return "For this source: keep clinical images, figures, videos, captions, and media references when the human baseline includes instructional media."
		case "missing_metadata":
			return "For this source: keep article metadata such as approved date, author, reviewer, fact reviewer, illustrator, photographer, and source owner when present."
		case "missing_breadcrumb":
			return "For this source: keep breadcrumb/topic path blocks when they are part of the human baseline, but do not include global navigation menus."
		case "extra_boilerplate":
			return "For this source: drop blocks containing share, print, copy-link, email, navigation, pure related-link text, or related-card UI unless the block itself contains clinical instructions."
		default:
			return "For this source: preserve repeated Sample Source article content patterns that are part of the human baseline, while dropping non-clinical UI boilerplate."
		}
	}
	switch evidence.Category {
	case "critical_type_mismatch":
		return fmt.Sprintf("%s: keep the already included highlighted clinical warning/info block but label it as type=critical.%s The text that must be critical is: %q.", target, blockHint, evidence.Snippet)
	case "missing_warning_info_box":
		return fmt.Sprintf("%s: keep highlighted warning/info boxes with clinical safety, contraindication, dosage-limit, urgent-care, or patient-risk content and label those safety boxes as type=critical.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_table":
		return fmt.Sprintf("%s: keep every dosage, parameter, equipment, observation, or clinical instruction table exactly as shown.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_media":
		return fmt.Sprintf("%s: keep clinical images, figures, videos, and media references when the human baseline includes them.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_metadata":
		return fmt.Sprintf("%s: keep article metadata such as approved date, author, reviewer, fact reviewer, illustrator, photographer, and source owner.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_breadcrumb":
		return fmt.Sprintf("%s: keep breadcrumb/topic path blocks when they are part of the human baseline, but do not include global navigation menus.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "extra_boilerplate":
		return fmt.Sprintf("For this source: drop blocks containing share, print, copy-link, email, navigation, or related-card UI unless they contain clinical instructions. Extra text seen: %q.", evidence.Snippet)
	default:
		return fmt.Sprintf("For this page only:%s include the source block containing %q because it is part of the human selector baseline.", blockHint, evidence.Snippet)
	}
}

func exactBlockHint(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	if len(ids) == 1 {
		return fmt.Sprintf(" Include exact block_id %s.", ids[0])
	}
	return fmt.Sprintf(" Include exact block_ids %s.", strings.Join(ids, ", "))
}

func matchingCandidateBlockIDs(expected BenchmarkSection, blocks []CandidateBlock) []string {
	var ids []string
	for _, block := range blocks {
		if textOverlap(expected.Text, block.Text) >= 0.8 || substantivePartialBlockMatch(block.Text, expected.Text) {
			ids = append(ids, block.ID)
		}
	}
	return ids
}

func substantivePartialBlockMatch(blockText, expectedText string) bool {
	normalized := normalizeText(blockText)
	if len([]rune(normalized)) < 25 {
		return false
	}
	if textOverlap(blockText, expectedText) < 0.8 {
		return false
	}
	return textOverlap(expectedText, blockText) >= 0.25 || sentenceLikeBlockText(normalized)
}

func sentenceLikeBlockText(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasSuffix(text, ".") || strings.HasSuffix(text, "!") || strings.HasSuffix(text, "?") || strings.HasSuffix(text, ":")
}

func bestBenchmarkOverlap(expected BenchmarkSection, actual []BenchmarkSection) float64 {
	best := 0.0
	for _, candidate := range actual {
		overlap := textOverlap(expected.Text, candidate.Text)
		if overlap > best {
			best = overlap
		}
	}
	return best
}

func extraActualEvidence(gold, actual []BenchmarkSection) []reviewerLearningPromptEvidence {
	var evidence []reviewerLearningPromptEvidence
	for _, candidate := range actual {
		if bestBenchmarkOverlap(candidate, gold) >= 0.8 {
			continue
		}
		lower := strings.ToLower(candidate.Text)
		if strings.Contains(lower, "läs mer") ||
			strings.Contains(lower, "skriv ut") ||
			strings.Contains(lower, "kopiera") ||
			strings.Contains(lower, "tipsa") ||
			strings.Contains(lower, "relaterad information") ||
			strings.Contains(lower, "länk till") {
			evidence = append(evidence, reviewerLearningPromptEvidence{Category: "extra_boilerplate", Snippet: reviewerLearningSnippet(candidate.Text), Scope: RuleScopeSource})
		}
	}
	return evidence
}

func hasEvidenceCategory(evidence []reviewerLearningPromptEvidence, category string) bool {
	for _, item := range evidence {
		if item.Category == category {
			return true
		}
	}
	return false
}

func reviewerLearningSnippet(text string) string {
	text = normalizeText(text)
	if len(text) <= 120 {
		return text
	}
	return strings.TrimSpace(text[:120])
}

func reviewerLearningPromptFamily(prompt string) string {
	switch {
	case strings.Contains(prompt, "label it as type=critical"):
		return "critical_type_mismatch"
	case strings.Contains(prompt, "inline Läs mer/reference links"):
		return "missing_mixed_clinical_reference"
	case strings.Contains(prompt, "warning/info boxes"):
		return "missing_warning_info_box"
	case strings.Contains(prompt, "dosage, parameter"):
		return "missing_table"
	case strings.Contains(prompt, "clinical images"):
		return "missing_media"
	case strings.Contains(prompt, "article metadata"):
		return "missing_metadata"
	case strings.Contains(prompt, "breadcrumb/topic path"):
		return "missing_breadcrumb"
	case strings.Contains(prompt, "drop blocks containing"):
		return "extra_boilerplate"
	default:
		return "page_specific_content"
	}
}

func clinicalParagraphWithInlineReference(text string) bool {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "läs mer") && !strings.Contains(lower, "sample") && !strings.Contains(lower, "vårdhandboken") {
		return false
	}
	return containsClinicalSignal(lower)
}
