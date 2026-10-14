package semantic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const benchmarkFeedbackInstruction = "Benchmark feedback: include breadcrumbs, article metadata, warnings and information boxes, body headings and prose, tables, and media when present in the source blocks. Exclude share widgets, print/email/copy controls, global navigation, and related-link cards unless the card itself contains substantive source instructions. Do not summarize or rewrite source text."

type BenchmarkOptions struct {
	FixtureSet       string
	FixtureDirectory string
	MappingPath      string
	URLContains      string
	ReportDirectory  string
	GoldOverridePath string
	Scope            string
	SourceSkill      SourceSkill
	Model            SemanticModel
	ModelMode        string
	Fetcher          Fetcher
	LiveFetch        bool
	StartPage        int
	MaxPages         int
}

func RunBenchmark(ctx context.Context, options BenchmarkOptions) (BenchmarkReport, error) {
	if err := ctx.Err(); err != nil {
		return BenchmarkReport{}, err
	}
	if options.Model == nil {
		return BenchmarkReport{}, fmt.Errorf("benchmark model is required")
	}
	mapping, err := LoadGoldMappings(options.MappingPath, options.URLContains)
	if err != nil {
		return BenchmarkReport{}, err
	}
	overrides, err := LoadGoldOverrides(options.GoldOverridePath)
	if err != nil {
		return BenchmarkReport{}, err
	}
	if options.StartPage > 1 {
		if options.StartPage > len(mapping.Pages) {
			mapping.Pages = nil
		} else {
			mapping.Pages = mapping.Pages[options.StartPage-1:]
		}
	}
	if options.MaxPages > 0 && len(mapping.Pages) > options.MaxPages {
		mapping.Pages = mapping.Pages[:options.MaxPages]
	}
	modelMode := options.ModelMode
	if modelMode == "" {
		modelMode = "fixture"
	}
	report := BenchmarkReport{
		FixtureSet:          options.FixtureSet,
		ModelMode:           modelMode,
		ExpectedURLs:        len(mapping.Pages),
		FeedbackInstruction: benchmarkFeedbackInstruction,
		PromptIterations: []PromptIteration{
			{Name: "first_pass", Instruction: "Base semantic extraction prompt plus source skill."},
			{Name: "after_source_rules", Instruction: benchmarkFeedbackInstruction},
			{Name: "simulated_baseline_correction", Instruction: "Simulated correction sets output to the provided baseline for comparison. No human edit is performed here; this is not model accuracy."},
		},
	}
	validCalls := 0
	totalCalls := 0
	var afterFeedbackPages []PageBenchmark
	for _, goldPage := range mapping.Pages {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		html, readErr := readBenchmarkHTML(ctx, options, goldPage)
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if readErr != nil {
			report.MissingURLs = append(report.MissingURLs, goldPage.URL)
			report.Pages = append(report.Pages, PageBenchmark{URL: goldPage.URL, BaselineStatus: BaselineFetchFailed, MismatchLabels: []string{"fetch_failed"}})
			report.updateSummary(totalCalls, validCalls, afterFeedbackPages)
			continue
		}
		report.DiscoveredURLs++
		gold, extractionErr := ExtractGoldSections(goldPage, html)
		if extractionErr != nil {
			return report, extractionErr
		}
		baselineStatus := baselineStatusForGold(goldPage, gold, overrides)
		if overrideSections := overrides.SectionsForURL(goldPage.URL); len(overrideSections) > 0 {
			gold = overrideSections
			baselineStatus = BaselineAuditedRepair
		}
		report.countBaselineStatus(baselineStatus)
		blocks, _, extractionErr := BuildCandidateBlocks(strings.NewReader(string(html)), goldPage.URL)
		if extractionErr != nil {
			return report, extractionErr
		}
		config := CrawlConfig{Scope: options.Scope, SourceSkill: options.SourceSkill}
		page := PageVersion{URL: goldPage.URL, Title: pageTitle(string(html))}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		totalCalls++
		route, routeUsage, routeErr := options.Model.Route(ctx, buildRoutePrompt(config, page, blocks, nil))
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.ModelInputTokens += routeUsage.InputTokens
		report.ModelOutputTokens += routeUsage.OutputTokens
		if routeErr != nil || route.Action != RouteExtract {
			metrics := EvaluateBenchmark(gold, nil)
			if baselineStatus == BaselineZeroGold {
				metrics.ExactParity = false
			}
			failedPage := PageBenchmark{URL: goldPage.URL, BaselineStatus: baselineStatus, Expected: gold, Metrics: metrics, MismatchLabels: []string{"routing_error"}}
			report.Pages = append(report.Pages, failedPage)
			afterFeedbackPages = append(afterFeedbackPages, failedPage)
			report.updateSummary(totalCalls, validCalls, afterFeedbackPages)
			continue
		}
		validCalls++
		totalCalls++
		response, usage, modelErr := extractWithValidationRetry(ctx, options.Model, buildExtractionPrompt(config, page, blocks, nil), blocks)
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.ModelInputTokens += usage.InputTokens
		report.ModelOutputTokens += usage.OutputTokens
		if modelErr != nil {
			metrics := EvaluateBenchmark(gold, nil)
			if baselineStatus == BaselineZeroGold {
				metrics.ExactParity = false
			}
			failedPage := PageBenchmark{URL: goldPage.URL, BaselineStatus: baselineStatus, Expected: gold, Metrics: metrics, MismatchLabels: []string{"schema_or_extraction_error"}}
			report.Pages = append(report.Pages, failedPage)
			afterFeedbackPages = append(afterFeedbackPages, failedPage)
			report.updateSummary(totalCalls, validCalls, afterFeedbackPages)
			continue
		}
		validCalls++
		actual := benchmarkSections(sectionsFromDecisions(blocks, response.Blocks))
		metrics := EvaluateBenchmark(gold, actual)
		if baselineStatus == BaselineZeroGold {
			metrics.ExactParity = false
		}
		labels := classifyMismatches(metrics)
		if baselineStatus == BaselineZeroGold {
			labels = append([]string{"stale_selector_baseline"}, labels...)
		}
		report.Pages = append(report.Pages, PageBenchmark{URL: goldPage.URL, BaselineStatus: baselineStatus, Expected: gold, Actual: actual, Metrics: metrics, MismatchLabels: labels})

		afterActual, afterUsage, afterErr := extractBenchmarkSections(ctx, options.Model, config, page, blocks, []SkillRule{{Scope: RuleScopeSource, Instruction: benchmarkFeedbackInstruction, Version: 2, Status: "active"}})
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.ModelInputTokens += afterUsage.InputTokens
		report.ModelOutputTokens += afterUsage.OutputTokens
		var afterLabels []string
		if afterErr != nil {
			afterActual = nil
			afterLabels = []string{"schema_or_extraction_error"}
		}
		afterMetrics := EvaluateBenchmark(gold, afterActual)
		if baselineStatus == BaselineZeroGold {
			afterMetrics.ExactParity = false
		}
		afterFeedbackPages = append(afterFeedbackPages, PageBenchmark{URL: goldPage.URL, BaselineStatus: baselineStatus, Expected: gold, Actual: afterActual, Metrics: afterMetrics, MismatchLabels: afterLabels})
		report.updateSummary(totalCalls, validCalls, afterFeedbackPages)
	}
	report.updateSummary(totalCalls, validCalls, afterFeedbackPages)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := WriteBenchmarkReports(options.ReportDirectory, report); err != nil {
		return report, err
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}

func readBenchmarkHTML(ctx context.Context, options BenchmarkOptions, goldPage GoldPage) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.FixtureDirectory != "" && !options.LiveFetch {
		path := filepath.Join(options.FixtureDirectory, goldPage.Slug+".html")
		html, err := os.ReadFile(path)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err == nil {
			return html, nil
		}
	}
	if options.Fetcher == nil {
		return nil, fmt.Errorf("benchmark fixture missing for %s", goldPage.URL)
	}
	result, err := options.Fetcher.Fetch(ctx, goldPage.URL)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	if result.StatusCode != 0 && (result.StatusCode < 200 || result.StatusCode >= 300) {
		return nil, fmt.Errorf("benchmark fetch %s returned HTTP %d", goldPage.URL, result.StatusCode)
	}
	return []byte(result.HTML), nil
}

func (report *BenchmarkReport) updateSummary(totalCalls, validCalls int, afterFeedbackPages []PageBenchmark) {
	if totalCalls > 0 {
		report.SchemaValidRate = float64(validCalls) / float64(totalCalls)
	}
	report.Aggregate = aggregateMetrics(report.Pages)
	report.AfterFeedbackAggregate = aggregateMetrics(afterFeedbackPages)
	report.SimulatedBaselineAggregate = simulatedBaselineAggregate(report.Pages)
	report.ReviewerPages, report.Corrections, report.Escalations, report.ReviewerSeconds = estimateReviewEffort(report.Pages)
	report.updateUsageSummary()
}

func simulatedBaselineAggregate(pages []PageBenchmark) BenchmarkMetrics {
	audited := make([]PageBenchmark, 0, len(pages))
	for _, page := range pages {
		if page.BaselineStatus == BaselineFetchFailed || page.BaselineStatus == BaselineZeroGold {
			continue
		}
		audited = append(audited, PageBenchmark{
			URL:            page.URL,
			BaselineStatus: page.BaselineStatus,
			Expected:       page.Expected,
			Actual:         page.Expected,
			Metrics:        EvaluateBenchmark(page.Expected, page.Expected),
		})
	}
	return aggregateMetrics(audited)
}

func (report *BenchmarkReport) updateUsageSummary() {
	if report.ModelMode == "" || report.ModelMode == "fixture" {
		report.TokenUsageAvailable = false
		report.RawUsageMissing = false
		report.UsageNote = "Fixture model: no LLM call, no token usage."
		return
	}
	if report.ModelInputTokens > 0 || report.ModelOutputTokens > 0 {
		report.TokenUsageAvailable = true
		report.RawUsageMissing = false
		report.UsageNote = "Real model usage returned by provider."
		return
	}
	report.TokenUsageAvailable = false
	report.RawUsageMissing = true
	report.UsageNote = "Real model run, but provider response did not include token usage."
}

func (report *BenchmarkReport) countBaselineStatus(status BaselineStatus) {
	switch status {
	case BaselineValidGold:
		report.UsableGoldPages++
	case BaselinePartialGold:
		report.PartialGoldPages++
	case BaselineZeroGold:
		report.StaleSelectorPages++
	case BaselineAuditedRepair:
		report.RepairedGoldPages++
	case BaselineFetchFailed:
		report.StaleSelectorPages++
	}
}

func baselineStatusForGold(page GoldPage, gold []BenchmarkSection, overrides GoldOverrides) BaselineStatus {
	if len(overrides.SectionsForURL(page.URL)) > 0 {
		return BaselineAuditedRepair
	}
	if len(gold) == 0 {
		return BaselineZeroGold
	}
	if len(gold) < len(page.Identifiers) {
		return BaselinePartialGold
	}
	return BaselineValidGold
}

func extractBenchmarkSections(ctx context.Context, model SemanticModel, config CrawlConfig, page PageVersion, blocks []CandidateBlock, rules []SkillRule) ([]BenchmarkSection, ModelUsage, error) {
	response, usage, err := extractWithValidationRetry(ctx, model, buildExtractionPrompt(config, page, blocks, rules), blocks)
	if err != nil {
		return nil, usage, err
	}
	return benchmarkSections(sectionsFromDecisions(blocks, response.Blocks)), usage, nil
}

func extractWithValidationRetry(ctx context.Context, model SemanticModel, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	if err := ctx.Err(); err != nil {
		return ExtractionResponse{}, ModelUsage{}, err
	}
	response, usage, err := model.Extract(ctx, prompt, blocks)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ExtractionResponse{}, usage, ctxErr
	}
	if err == nil {
		return response, usage, nil
	}
	retryLimit := extractionValidationRetryLimit(err)
	if retryLimit == 0 {
		return ExtractionResponse{}, usage, err
	}
	lastErr := err
	retryPrompt := prompt
	for attempt := 1; attempt <= retryLimit; attempt++ {
		if err := ctx.Err(); err != nil {
			return ExtractionResponse{}, usage, err
		}
		retryPrompt += "\n\nYour previous response failed local schema or block validation. Retry with compact JSON only. Use only known block_id values from the source blocks, use only enum values from the JSON schema, and keep every reason under 3 words."
		retryResponse, retryUsage, retryErr := model.Extract(ctx, retryPrompt, blocks)
		usage.InputTokens += retryUsage.InputTokens
		usage.OutputTokens += retryUsage.OutputTokens
		usage.TotalTokens += retryUsage.TotalTokens
		usage.Latency += retryUsage.Latency
		usage.RawResponse = usage.RawResponse + "\n" + retryUsage.RawResponse
		if err := ctx.Err(); err != nil {
			return ExtractionResponse{}, usage, err
		}
		if retryErr == nil {
			return retryResponse, usage, nil
		}
		lastErr = retryErr
		if !shouldRetryExtractionValidationError(retryErr) {
			break
		}
	}
	return ExtractionResponse{}, usage, lastErr
}

func shouldRetryExtractionValidationError(err error) bool {
	return extractionValidationRetryLimit(err) > 0
}

func extractionValidationRetryLimit(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0
	}
	message := err.Error()
	if strings.Contains(message, "decode structured model content") ||
		strings.Contains(message, "model response contained no structured content") {
		return 2
	}
	if strings.Contains(message, "validate extraction response") {
		return 1
	}
	return 0
}

func benchmarkSections(sections []Section) []BenchmarkSection {
	result := make([]BenchmarkSection, 0, len(sections))
	for _, section := range sections {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(section.Content))
		text := section.Content
		if err == nil {
			text = doc.Text()
			if normalizeText(text) == "" && isMediaRole(section.Role) {
				text = mediaSelectionText(doc.Selection)
			}
		}
		if isMediaRole(section.Role) {
			text = normalizeMediaGoldText(text)
		}
		result = append(result, BenchmarkSection{BlockID: section.BlockID, Role: section.Role, Type: section.Type, ParseMode: section.ParseMode, Text: normalizeText(text)})
	}
	return result
}

func classifyMismatches(metrics BenchmarkMetrics) []string {
	if metrics.ExactParity {
		return nil
	}
	var labels []string
	if metrics.RelevantTextRecall < 1 || metrics.CriticalRecall < 1 || metrics.TableRecall < 1 || metrics.MediaRecall < 1 {
		labels = append(labels, "ai_error_or_missing_content")
	}
	if metrics.RoleParity < 1 || metrics.TypeParity < 1 || metrics.ParseModeParity < 1 {
		labels = append(labels, "policy_or_segmentation_difference")
	}
	if metrics.RelevantTextPrecision < 1 {
		labels = append(labels, "additional_or_boilerplate_content")
	}
	return labels
}

func aggregateMetrics(pages []PageBenchmark) BenchmarkMetrics {
	var comparable []PageBenchmark
	for _, page := range pages {
		if page.BaselineStatus == BaselineFetchFailed || page.BaselineStatus == BaselineZeroGold {
			continue
		}
		comparable = append(comparable, page)
	}
	if len(comparable) == 0 {
		return BenchmarkMetrics{}
	}
	var aggregate BenchmarkMetrics
	exact := true
	for _, page := range comparable {
		metrics := page.Metrics
		aggregate.RelevantTextRecall += metrics.RelevantTextRecall
		aggregate.RelevantTextPrecision += metrics.RelevantTextPrecision
		aggregate.RelevantTextF1 += metrics.RelevantTextF1
		aggregate.CriticalRecall += metrics.CriticalRecall
		aggregate.TableRecall += metrics.TableRecall
		aggregate.MediaRecall += metrics.MediaRecall
		aggregate.BoilerplateRate += metrics.BoilerplateRate
		aggregate.RoleParity += metrics.RoleParity
		aggregate.TypeParity += metrics.TypeParity
		aggregate.ParseModeParity += metrics.ParseModeParity
		aggregate.GoldSections += metrics.GoldSections
		aggregate.ActualSections += metrics.ActualSections
		exact = exact && metrics.ExactParity
	}
	count := float64(len(comparable))
	aggregate.RelevantTextRecall /= count
	aggregate.RelevantTextPrecision /= count
	aggregate.RelevantTextF1 /= count
	aggregate.CriticalRecall /= count
	aggregate.TableRecall /= count
	aggregate.MediaRecall /= count
	aggregate.BoilerplateRate /= count
	aggregate.RoleParity /= count
	aggregate.TypeParity /= count
	aggregate.ParseModeParity /= count
	aggregate.ExactParity = exact
	return aggregate
}

func estimateReviewEffort(pages []PageBenchmark) (reviewerPages int, corrections int, escalations int, seconds int64) {
	for _, page := range pages {
		if page.BaselineStatus == BaselineFetchFailed || page.BaselineStatus == BaselineZeroGold {
			continue
		}
		if page.Metrics.ExactParity {
			continue
		}
		reviewerPages++
		pageSeconds := int64(8)
		if page.Metrics.RelevantTextRecall < 1 || page.Metrics.RelevantTextPrecision < 1 || page.Metrics.RoleParity < 1 || page.Metrics.TypeParity < 1 || page.Metrics.ParseModeParity < 1 {
			corrections++
			pageSeconds += 12
		}
		if page.Metrics.CriticalRecall < 1 || page.Metrics.TableRecall < 1 || page.Metrics.MediaRecall < 1 {
			escalations++
			pageSeconds += 30
		}
		seconds += pageSeconds
	}
	return reviewerPages, corrections, escalations, seconds
}

type FixtureModel struct{}

func (*FixtureModel) Route(_ context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	if strings.Contains(prompt, "URL: https://guidelines.example.test/airway/\n") {
		return RouteResponse{Action: RouteNavigateOnly, Confidence: 1, Reason: "generic topic navigation root"}, ModelUsage{}, nil
	}
	if strings.Contains(prompt, "URL: https://guidelines.example.test/about\n") || strings.Contains(prompt, "URL: https://guidelines.example.test/about/") {
		return RouteResponse{Action: RouteIgnore, Confidence: 1, Reason: "generic about page"}, ModelUsage{}, nil
	}
	for _, marker := range []string{"/film-om-", "/referenser-och-regelverk/", "/relaterad-information/", "/testa-dina-kunskaper/"} {
		if strings.Contains(prompt, marker) {
			return RouteResponse{Action: RouteNavigateOnly, Confidence: 1, Reason: "deterministic non-ingestion topic page"}, ModelUsage{}, nil
		}
	}
	if strings.Contains(prompt, "URL: https://source.example.test/topics/airway/\n") {
		return RouteResponse{Action: RouteNavigateOnly, Confidence: 1, Reason: "topic navigation root"}, ModelUsage{}, nil
	}
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "deterministic fixture article"}, ModelUsage{}, nil
}

func (*FixtureModel) Extract(_ context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	decisions := make([]BlockDecision, 0, len(blocks))
	for _, block := range blocks {
		role, blockType, parseMode := fixtureClassification(block)
		include := fixtureInclude(block, prompt)
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: include, Role: role, Type: blockType, ParseMode: parseMode, Reason: "deterministic semantic fixture rule"})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions, Uncertainties: []string{}}
	return response, ModelUsage{}, ValidateExtraction(response, blocks)
}

func fixtureInclude(block CandidateBlock, prompt string) bool {
	text := strings.ToLower(block.Text)
	for _, ignored := range []string{"acceptera kakor", "inställningar för kakor", "till sidans topp", "relaterad information", "du är här:"} {
		if strings.Contains(text, ignored) {
			return false
		}
	}
	for _, ignored := range []string{"share this article", "contact and newsletter", "editorial information, contact details, cookies"} {
		if strings.Contains(text, ignored) {
			return false
		}
	}
	if strings.Contains(strings.ToLower(prompt), "benchmark feedback:") {
		for _, ignored := range []string{"tipsa & dela", "dela via mejl", "kopiera länk", "skriv ut sidan"} {
			if strings.Contains(text, ignored) {
				return false
			}
		}
	}
	return block.Text != "" || block.Tag == "figure" || block.Tag == "video" || block.Tag == "img" || block.Tag == "audio"
}

func fixtureClassification(block CandidateBlock) (BlockRole, BlockType, bool) {
	text := strings.ToLower(block.Text)
	switch block.Tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return RoleHeader, TypeNone, true
	case "aside":
		blockType := TypeInfo
		if strings.Contains(text, "observera") || strings.Contains(text, "avbryt") || strings.Contains(text, "risk") || strings.Contains(text, "critical warning") || strings.Contains(text, "stop") || strings.Contains(text, "do not continue") || strings.Contains(text, "urgent care") {
			blockType = TypeCritical
		}
		return RoleInfoBox, blockType, false
	case "table":
		return RoleTable, TypeNone, true
	case "figure", "img", "audio":
		return RoleMedia, TypeNone, false
	case "video":
		return RoleVideo, TypeNone, false
	default:
		return RoleContent, TypeNone, true
	}
}
