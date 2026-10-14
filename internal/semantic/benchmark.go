package semantic

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type BenchmarkSection struct {
	BlockID   string    `json:"block_id"`
	Role      BlockRole `json:"role"`
	Type      BlockType `json:"type"`
	ParseMode bool      `json:"parse_mode"`
	Text      string    `json:"text"`
}

type BenchmarkMetrics struct {
	ExactParity           bool    `json:"exact_parity"`
	RelevantTextRecall    float64 `json:"relevant_text_recall"`
	RelevantTextPrecision float64 `json:"relevant_text_precision"`
	RelevantTextF1        float64 `json:"relevant_text_f1"`
	CriticalRecall        float64 `json:"critical_recall"`
	TableRecall           float64 `json:"table_recall"`
	MediaRecall           float64 `json:"media_recall"`
	BoilerplateRate       float64 `json:"boilerplate_rate"`
	RoleParity            float64 `json:"role_parity"`
	TypeParity            float64 `json:"type_parity"`
	ParseModeParity       float64 `json:"parse_mode_parity"`
	GoldSections          int     `json:"gold_sections"`
	ActualSections        int     `json:"actual_sections"`
}

var (
	mediaImageAltPattern        = regexp.MustCompile(`(?i)<img[^>]*\salt="([^"]*)"[^>]*>`)
	mediaHTMLTagPattern         = regexp.MustCompile(`(?s)<[^>]+>`)
	mediaAssetPathPattern       = regexp.MustCompile(`/globalassets/[^\s"]+`)
	mediaSlideCounterPattern    = regexp.MustCompile(`(?i)\bBild\s+\d+(?:/\d+)?`)
	mediaSlideControlsPattern   = regexp.MustCompile(`(?i)Visa nästa bild|Visa föregående bild`)
	benchmarkCitationPattern    = regexp.MustCompile(`\[\s*\d+(?:\s*[,–-]\s*\d+)*\s*\]`)
	benchmarkImageUIPattern     = regexp.MustCompile(`(?i)förstora bilden|\bbild som visar\b|\bbild\s*\d+\.?`)
	benchmarkRelatedInfoPattern = regexp.MustCompile(`(?i)länk till [^.]*relaterad information\.?`)
	mediaSelectorTokenPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bsrc\b`),
		regexp.MustCompile(`(?i)\balt\b`),
		regexp.MustCompile(`(?i)\bimg\b`),
	}
)

func NormalizeSemanticGold(raw []BenchmarkSection) []BenchmarkSection {
	normalized := make([]BenchmarkSection, 0, len(raw))
	for index, section := range raw {
		if isDuplicateSelectorContainer(section, raw, index) {
			continue
		}
		if isPureSelectorBoilerplate(section) {
			continue
		}
		section = normalizeSemanticGoldSection(section)
		normalized = append(normalized, section)
	}
	return normalized
}

func normalizeSemanticGoldSection(section BenchmarkSection) BenchmarkSection {
	if isMediaRole(section.Role) {
		section.Text = normalizeMediaGoldText(section.Text)
	}
	return section
}

func normalizeMediaGoldText(text string) string {
	text = mediaImageAltPattern.ReplaceAllString(text, " $1 ")
	text = mediaHTMLTagPattern.ReplaceAllString(text, " ")
	text = mediaAssetPathPattern.ReplaceAllString(text, " ")
	text = mediaSlideControlsPattern.ReplaceAllString(text, " ")
	text = mediaSlideCounterPattern.ReplaceAllString(text, " ")
	for _, pattern := range mediaSelectorTokenPatterns {
		text = pattern.ReplaceAllString(text, " ")
	}
	return normalizeText(text)
}

func isDuplicateSelectorContainer(section BenchmarkSection, sections []BenchmarkSection, self int) bool {
	lowerID := strings.ToLower(section.BlockID)
	if !strings.Contains(lowerID, "container") && !strings.Contains(lowerID, "main") && !strings.Contains(lowerID, "wrapper") {
		return false
	}
	coveredChildren := 0
	for index, candidate := range sections {
		if index == self || normalizeText(candidate.Text) == "" {
			continue
		}
		if textOverlap(candidate.Text, section.Text) >= 0.8 {
			coveredChildren++
		}
	}
	return coveredChildren >= 2
}

func isPureSelectorBoilerplate(section BenchmarkSection) bool {
	text := strings.ToLower(normalizeText(section.Text))
	if text == "" {
		return true
	}
	boilerplateSignals := []string{"tipsa & dela", "kopiera länk", "skriv ut sidan", "dela via mejl"}
	for _, signal := range boilerplateSignals {
		if strings.Contains(text, signal) {
			return !containsClinicalSignal(text)
		}
	}
	return false
}

func containsClinicalSignal(text string) bool {
	for _, signal := range []string{"patient", "dos", "risk", "behandling", "vård", "symtom", "läkemedel", "andning", "cirkulation", "smärta", "infektion"} {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

type PageBenchmark struct {
	URL            string             `json:"url"`
	BaselineStatus BaselineStatus     `json:"baseline_status"`
	Expected       []BenchmarkSection `json:"expected,omitempty"`
	Actual         []BenchmarkSection `json:"actual,omitempty"`
	Metrics        BenchmarkMetrics   `json:"metrics"`
	MismatchLabels []string           `json:"mismatch_labels,omitempty"`
}

type BenchmarkReport struct {
	FixtureSet                 string            `json:"fixture_set"`
	ModelMode                  string            `json:"model_mode"`
	ExpectedURLs               int               `json:"expected_urls"`
	DiscoveredURLs             int               `json:"discovered_urls"`
	MissingURLs                []string          `json:"missing_urls,omitempty"`
	UnexpectedURLs             []string          `json:"unexpected_urls,omitempty"`
	Pages                      []PageBenchmark   `json:"pages"`
	Aggregate                  BenchmarkMetrics  `json:"aggregate"`
	FeedbackInstruction        string            `json:"feedback_instruction,omitempty"`
	PromptIterations           []PromptIteration `json:"prompt_iterations,omitempty"`
	AfterFeedbackAggregate     BenchmarkMetrics  `json:"after_feedback_aggregate"`
	SimulatedBaselineAggregate BenchmarkMetrics  `json:"simulated_baseline_correction_aggregate"`
	SchemaValidRate            float64           `json:"schema_valid_rate"`
	ModelInputTokens           int               `json:"model_input_tokens"`
	ModelOutputTokens          int               `json:"model_output_tokens"`
	TokenUsageAvailable        bool              `json:"token_usage_available"`
	RawUsageMissing            bool              `json:"raw_usage_missing"`
	UsageNote                  string            `json:"usage_note"`
	UsableGoldPages            int               `json:"usable_gold_pages"`
	PartialGoldPages           int               `json:"partial_gold_pages"`
	StaleSelectorPages         int               `json:"stale_selector_pages"`
	RepairedGoldPages          int               `json:"repaired_gold_pages"`
	ReviewerPages              int               `json:"reviewer_pages"`
	ReviewerSeconds            int64             `json:"reviewer_seconds"`
	Corrections                int               `json:"corrections"`
	Escalations                int               `json:"escalations"`
}

type BaselineStatus string

const (
	BaselineValidGold     BaselineStatus = "valid_gold"
	BaselinePartialGold   BaselineStatus = "partial_gold"
	BaselineZeroGold      BaselineStatus = "zero_gold"
	BaselineAuditedRepair BaselineStatus = "audited_repair"
	BaselineFetchFailed   BaselineStatus = "fetch_failed"
)

type PromptIteration struct {
	Name        string `json:"name"`
	Instruction string `json:"instruction"`
}

func EvaluateBenchmark(gold, actual []BenchmarkSection) BenchmarkMetrics {
	metrics := BenchmarkMetrics{GoldSections: len(gold), ActualSections: len(actual)}
	goldTokens := benchmarkTokens(gold)
	actualTokens := benchmarkTokens(actual)
	intersection := tokenIntersection(goldTokens, actualTokens)
	metrics.RelevantTextRecall = ratio(intersection, tokenCount(goldTokens))
	metrics.RelevantTextPrecision = ratio(intersection, tokenCount(actualTokens))
	metrics.RelevantTextF1 = f1(metrics.RelevantTextPrecision, metrics.RelevantTextRecall)
	metrics.BoilerplateRate = 1 - metrics.RelevantTextPrecision
	metrics.CriticalRecall = categoryRecall(gold, actual, func(section BenchmarkSection) bool { return section.Type == TypeCritical })
	metrics.TableRecall = categoryRecall(gold, actual, func(section BenchmarkSection) bool { return section.Role == RoleTable })
	metrics.MediaRecall = mediaRecall(gold, actual)
	metrics.RoleParity, metrics.TypeParity, metrics.ParseModeParity = structuralParity(gold, actual)
	metrics.ExactParity = exactBenchmarkParity(gold, actual)
	return metrics
}

func mediaRecall(gold, actual []BenchmarkSection) float64 {
	var expected, found int
	for _, section := range gold {
		if !isMediaRole(section.Role) {
			continue
		}
		expected++
		if hasActualMediaEvidence(section, actual) {
			found++
		}
	}
	return ratio(found, expected)
}

func hasActualMediaEvidence(expected BenchmarkSection, actual []BenchmarkSection) bool {
	for _, candidate := range actual {
		if !isMediaRole(candidate.Role) {
			continue
		}
		if maxFloat(textOverlap(candidate.Text, expected.Text), textOverlap(expected.Text, candidate.Text)) >= 0.3 {
			return true
		}
	}
	return false
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func benchmarkTokens(sections []BenchmarkSection) map[string]int {
	tokens := make(map[string]int)
	for _, section := range sections {
		text := normalizeBenchmarkTokenText(section.Text)
		for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
			if token != "" {
				tokens[token] = 1
			}
		}
	}
	return tokens
}

func normalizeBenchmarkTokenText(text string) string {
	text = benchmarkCitationPattern.ReplaceAllString(text, " ")
	text = benchmarkImageUIPattern.ReplaceAllString(text, " ")
	text = benchmarkRelatedInfoPattern.ReplaceAllString(text, " ")
	return text
}

func tokenIntersection(left, right map[string]int) int {
	total := 0
	for token, count := range left {
		if right[token] < count {
			count = right[token]
		}
		total += count
	}
	return total
}

func tokenCount(tokens map[string]int) int {
	total := 0
	for _, count := range tokens {
		total += count
	}
	return total
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}

func f1(precision, recall float64) float64 {
	if precision+recall == 0 {
		return 0
	}
	return 2 * precision * recall / (precision + recall)
}

func categoryRecall(gold, actual []BenchmarkSection, matches func(BenchmarkSection) bool) float64 {
	var expected, found int
	for _, section := range gold {
		if matches(section) {
			expected++
			if sectionTextCoveredByCollection(section, actual, matches, 0.8) {
				found++
			}
		}
	}
	return ratio(found, expected)
}

func structuralParity(gold, actual []BenchmarkSection) (float64, float64, float64) {
	var matched, role, blockType, parseMode int
	used := make(map[int]bool)
	for _, expected := range gold {
		best := -1
		bestOverlap := 0.0
		for index, candidate := range actual {
			if used[index] {
				continue
			}
			overlap := textOverlap(expected.Text, candidate.Text)
			if overlap > bestOverlap {
				best, bestOverlap = index, overlap
			}
		}
		if best < 0 || bestOverlap < 0.8 {
			continue
		}
		used[best] = true
		matched++
		if expected.Role == actual[best].Role {
			role++
		}
		if expected.Type == actual[best].Type {
			blockType++
		}
		if expected.ParseMode == actual[best].ParseMode {
			parseMode++
		}
	}
	return ratio(role, matched), ratio(blockType, matched), ratio(parseMode, matched)
}

func textOverlap(left, right string) float64 {
	leftTokens := benchmarkTokens([]BenchmarkSection{{Text: left}})
	rightTokens := benchmarkTokens([]BenchmarkSection{{Text: right}})
	return ratio(tokenIntersection(leftTokens, rightTokens), tokenCount(leftTokens))
}

func sectionTextCoveredByCollection(expected BenchmarkSection, actual []BenchmarkSection, matches func(BenchmarkSection) bool, threshold float64) bool {
	expectedTokens := benchmarkTokens([]BenchmarkSection{expected})
	actualTokens := make(map[string]int)
	for _, candidate := range actual {
		if matches != nil && !matches(candidate) {
			continue
		}
		for token := range benchmarkTokens([]BenchmarkSection{candidate}) {
			actualTokens[token] = 1
		}
	}
	return ratio(tokenIntersection(expectedTokens, actualTokens), tokenCount(expectedTokens)) >= threshold
}

func exactBenchmarkParity(gold, actual []BenchmarkSection) bool {
	if len(gold) != len(actual) {
		return false
	}
	normalize := func(sections []BenchmarkSection) []string {
		result := make([]string, 0, len(sections))
		for _, section := range sections {
			result = append(result, fmt.Sprintf("%s|%s|%s|%t|%s", section.BlockID, section.Role, section.Type, section.ParseMode, normalizeText(section.Text)))
		}
		sort.Strings(result)
		return result
	}
	left, right := normalize(gold), normalize(actual)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func WriteBenchmarkReports(directory string, report BenchmarkReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create benchmark directory: %w", err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode benchmark report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "benchmark.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write benchmark JSON: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "prompt-iteration.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write prompt iteration JSON: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "reviewer-prompts.md"), []byte(benchmarkReviewerPromptsMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write reviewer prompts: %w", err)
	}
	file, err := os.Create(filepath.Join(directory, "benchmark.html"))
	if err != nil {
		return fmt.Errorf("create benchmark HTML: %w", err)
	}
	defer file.Close()
	functions := template.FuncMap{"mul": func(left, right float64) float64 { return left * right }}
	if err := template.Must(template.New("report").Funcs(functions).Parse(benchmarkHTML)).Execute(file, report); err != nil {
		return err
	}
	iterationFile, err := os.Create(filepath.Join(directory, "prompt-iteration.html"))
	if err != nil {
		return fmt.Errorf("create prompt iteration HTML: %w", err)
	}
	defer iterationFile.Close()
	return template.Must(template.New("prompt-iteration").Funcs(functions).Parse(promptIterationHTML)).Execute(iterationFile, report)
}

func benchmarkReviewerPromptsMarkdown(report BenchmarkReport) string {
	var builder strings.Builder
	builder.WriteString("# Benchmark Reviewer Prompts\n\n")
	builder.WriteString("These are the exact benchmark instructions/rules used for prompt iteration.\n\n")
	for _, iteration := range report.PromptIterations {
		fmt.Fprintf(&builder, "## %s\n\n%s\n\n", iteration.Name, iteration.Instruction)
	}
	if len(report.PromptIterations) == 0 && report.FeedbackInstruction != "" {
		fmt.Fprintf(&builder, "## after_source_rules\n\n%s\n", report.FeedbackInstruction)
	}
	return builder.String()
}

const benchmarkHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Semantic crawler benchmark</title><style>body{font:14px system-ui;max-width:1200px;margin:30px auto;padding:0 20px;color:#172033}.cards{display:grid;grid-template-columns:repeat(5,1fr);gap:12px}.card{border:1px solid #ccd3dc;border-radius:8px;padding:12px;background:#f8fafc}.metric{font-size:24px;font-weight:700}table{border-collapse:collapse;width:100%;margin-top:16px}th,td{border:1px solid #ccc;padding:8px;text-align:left;vertical-align:top}.yes{color:#087830}.no{color:#b42318}.muted{color:#536274}</style></head><body><h1>Semantic crawler benchmark: {{.FixtureSet}}</h1><p>Model mode: <strong>{{.ModelMode}}</strong> · Expected URLs: {{.ExpectedURLs}} · Discovered URLs: {{.DiscoveredURLs}} · Schema valid rate: {{printf "%.1f%%" (mul .SchemaValidRate 100)}}</p><p>Usable gold: {{.UsableGoldPages}} · Partial gold: {{.PartialGoldPages}} · Stale/zero gold: {{.StaleSelectorPages}} · Audited repairs: {{.RepairedGoldPages}}</p><div class="cards"><section class="card"><div class="muted">Before feedback F1</div><div class="metric">{{printf "%.1f%%" (mul .Aggregate.RelevantTextF1 100)}}</div><div>Recall {{printf "%.1f%%" (mul .Aggregate.RelevantTextRecall 100)}} · Precision {{printf "%.1f%%" (mul .Aggregate.RelevantTextPrecision 100)}}</div></section><section class="card"><div class="muted">After source instruction F1</div><div class="metric">{{printf "%.1f%%" (mul .AfterFeedbackAggregate.RelevantTextF1 100)}}</div><div>Recall {{printf "%.1f%%" (mul .AfterFeedbackAggregate.RelevantTextRecall 100)}} · Precision {{printf "%.1f%%" (mul .AfterFeedbackAggregate.RelevantTextPrecision 100)}}</div></section><section class="card"><div class="muted">Simulated baseline correction</div><div class="metric">{{printf "%.1f%%" (mul .SimulatedBaselineAggregate.RelevantTextF1 100)}}</div><div>Exact parity {{.SimulatedBaselineAggregate.ExactParity}}</div></section><section class="card"><div class="muted">Estimated review effort</div><div class="metric">{{.ReviewerSeconds}}s</div><div>{{.ReviewerPages}} pages · {{.Corrections}} corrections · {{.Escalations}} escalations</div></section><section class="card"><div class="muted">Model usage</div><div class="metric">{{if .TokenUsageAvailable}}{{.ModelInputTokens}} / {{.ModelOutputTokens}}{{else}}n/a{{end}}</div><div>{{.UsageNote}}</div></section></div>{{if .FeedbackInstruction}}<p><strong>Feedback instruction tested:</strong> {{.FeedbackInstruction}}</p>{{end}}<table><thead><tr><th>URL</th><th>Baseline</th><th>Exact parity</th><th>Text recall</th><th>Precision</th><th>Critical recall</th><th>Table recall</th><th>Media recall</th><th>Mismatch labels</th></tr></thead><tbody>{{range .Pages}}<tr><td>{{.URL}}</td><td><code>{{.BaselineStatus}}</code></td><td>{{if .Metrics.ExactParity}}<span class="yes">Yes</span>{{else}}<span class="no">No</span>{{end}}</td><td>{{printf "%.1f%%" (mul .Metrics.RelevantTextRecall 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.RelevantTextPrecision 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.CriticalRecall 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.TableRecall 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.MediaRecall 100)}}</td><td>{{range .MismatchLabels}}<div>{{.}}</div>{{end}}</td></tr>{{end}}</tbody></table></body></html>`

const promptIterationHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Prompt iteration</title><style>body{font:14px system-ui;max-width:1000px;margin:30px auto;padding:0 20px;color:#172033}section{border:1px solid #ccd3dc;border-radius:8px;padding:14px;margin:12px 0;background:#f8fafc}pre{white-space:pre-wrap}</style></head><body><h1>Prompt iteration: {{.FixtureSet}}</h1>{{range .PromptIterations}}<section><h2>{{.Name}}</h2><pre>{{.Instruction}}</pre></section>{{else}}<p>No prompt iterations recorded.</p>{{end}}</body></html>`
