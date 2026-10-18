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
	prompt := fmt.Sprintf("For this page only: compare %s against the human selector baseline and include the remaining source blocks selected by the human baseline while dropping only standalone UI boilerplate absent from that baseline.", url)
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
		case paragraphWithInlineReference(expected.Text):
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
		evidence = append(evidence, reviewerLearningPromptEvidence{Category: "missing_warning_info_box", Snippet: "critical notice or information box selected by the human baseline", Scope: RuleScopeSource, Risky: true})
	}
	if metrics.TableRecall < 1 && !hasEvidenceCategory(evidence, "missing_table") {
		evidence = append(evidence, reviewerLearningPromptEvidence{Category: "missing_table", Snippet: "table selected by the human baseline", Scope: RuleScopeSource, Risky: true})
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
			return "For this source: keep substantive paragraphs selected by the human baseline even when the same paragraph also contains inline Läs mer/reference links. Drop only standalone related-link cards or pure navigation link blocks absent from the baseline."
		case "critical_type_mismatch":
			return "For this source: preserve type=critical on notices and warning/info boxes identified as critical by the human baseline, including deadlines, prohibitions, exceptions, and required actions."
		case "missing_warning_info_box":
			return "For this source: keep highlighted warning/info boxes and other critical notices selected by the human baseline. Preserve their text, deadlines, exceptions, and required actions, and use type=critical where the baseline does."
		case "missing_table":
			return "For this source: keep every table selected by the human baseline exactly as shown, including headings, values, units, and row/column relationships."
		case "missing_media":
			return "For this source: keep images, figures, videos, captions, and media references selected by the human baseline."
		case "missing_metadata":
			return "For this source: keep article metadata such as approved date, author, reviewer, fact reviewer, illustrator, photographer, and source owner when present."
		case "missing_breadcrumb":
			return "For this source: keep breadcrumb/topic path blocks when they are part of the human baseline, but do not include global navigation menus."
		case "extra_boilerplate":
			return "For this source: drop only standalone share, print, copy-link, email, navigation, or related-card UI absent from the human baseline. Preserve mixed blocks containing substantive source content."
		default:
			return "For this source: preserve source content patterns selected by the human baseline, while dropping only standalone UI boilerplate absent from the baseline."
		}
	}
	switch evidence.Category {
	case "critical_type_mismatch":
		return fmt.Sprintf("%s: keep the already included source block but label it as type=critical to match the human baseline.%s The text that must be critical is: %q.", target, blockHint, evidence.Snippet)
	case "missing_warning_info_box":
		return fmt.Sprintf("%s: keep the critical notice or warning/info block selected by the human baseline, preserving deadlines, exceptions, and required actions, and mark that block as type=critical.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_table":
		return fmt.Sprintf("%s: keep every table selected by the human baseline exactly as shown, including headings, values, units, and row/column relationships.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_media":
		return fmt.Sprintf("%s: keep images, figures, videos, and media references selected by the human baseline.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_metadata":
		return fmt.Sprintf("%s: keep article metadata such as approved date, author, reviewer, fact reviewer, illustrator, photographer, and source owner.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "missing_breadcrumb":
		return fmt.Sprintf("%s: keep breadcrumb/topic path blocks when they are part of the human baseline, but do not include global navigation menus.%s Include source blocks containing: %q.", target, blockHint, evidence.Snippet)
	case "extra_boilerplate":
		return fmt.Sprintf("For this source: drop only standalone share, print, copy-link, email, navigation, or related-card UI absent from the human baseline. Preserve mixed blocks containing substantive source content. Extra text seen: %q.", evidence.Snippet)
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
		if isPureSelectorBoilerplate(candidate) {
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
	case strings.Contains(prompt, "label it as type=critical") || strings.Contains(prompt, "preserve type=critical"):
		return "critical_type_mismatch"
	case strings.Contains(prompt, "inline Läs mer/reference links"):
		return "missing_mixed_clinical_reference"
	case strings.Contains(prompt, "warning/info boxes") || strings.Contains(prompt, "critical notice or warning/info block"):
		return "missing_warning_info_box"
	case strings.Contains(prompt, "dosage, parameter") || strings.Contains(prompt, "keep every table"):
		return "missing_table"
	case strings.Contains(prompt, "clinical images") || strings.Contains(prompt, "keep images"):
		return "missing_media"
	case strings.Contains(prompt, "article metadata"):
		return "missing_metadata"
	case strings.Contains(prompt, "breadcrumb/topic path"):
		return "missing_breadcrumb"
	case strings.Contains(prompt, "drop blocks containing") || strings.Contains(prompt, "drop only standalone"):
		return "extra_boilerplate"
	default:
		return "page_specific_content"
	}
}

// Keep the historical report category for compatibility, but identify mixed
// reference paragraphs from their structure rather than medical vocabulary.
func paragraphWithInlineReference(text string) bool {
	lower := strings.ToLower(normalizeText(text))
	for _, marker := range []string{"läs mer", "read more"} {
		if index := strings.Index(lower, marker); index > 0 && strings.TrimSpace(lower[:index]) != "" {
			return true
		}
	}
	return false
}
