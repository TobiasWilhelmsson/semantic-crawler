package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func RunDeepReviewerLearningBenchmark(ctx context.Context, options ReviewerLearningOptions) (ReviewerLearningReport, error) {
	options.LearningMode = ReviewerLearningModeDeep
	if options.Model == nil {
		return ReviewerLearningReport{}, fmt.Errorf("reviewer learning model is required")
	}
	if err := ctx.Err(); err != nil {
		return ReviewerLearningReport{}, err
	}
	if options.QualityThreshold == 0 {
		options.QualityThreshold = 0.98
	}
	if options.ReviewReadyThreshold == 0 {
		options.ReviewReadyThreshold = 0.97
	}
	if options.MinPages == 0 {
		options.MinPages = 10
	}
	if options.StableStreak == 0 {
		options.StableStreak = 5
	}
	if options.StallWindow == 0 {
		options.StallWindow = 3
	}
	if options.MinImprovement == 0 {
		options.MinImprovement = 0.002
	}
	if options.LockTimeout == 0 {
		options.LockTimeout = 30 * time.Minute
	}
	runID := reviewerLearningRunID()
	lock, lockErr := acquireReviewerLearningLock(options.ReportDirectory, runID, options.LockTimeout, options.Force)
	if lockErr != nil {
		return ReviewerLearningReport{}, lockErr
	}
	defer releaseReviewerLearningLock(options.ReportDirectory, lock)

	mapping, err := LoadGoldMappings(options.MappingPath, options.URLContains)
	if err != nil {
		return ReviewerLearningReport{}, err
	}
	overrides, err := LoadGoldOverrides(options.GoldOverridePath)
	if err != nil {
		return ReviewerLearningReport{}, err
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
	if len(mapping.Pages) == 0 {
		return ReviewerLearningReport{}, fmt.Errorf("reviewer learning selected baseline contains no pages")
	}
	inputIdentity, err := reviewerLearningInputIdentity(options, mapping, overrides)
	if err != nil {
		return ReviewerLearningReport{}, err
	}

	report := ReviewerLearningReport{}
	if options.Force {
		report = ReviewerLearningReport{}
	} else if options.Resume {
		if loaded, loadErr := LoadReviewerLearningReport(options.ReportDirectory); loadErr == nil {
			report = loaded
			if report.FixtureSet != "" && report.FixtureSet != options.FixtureSet {
				return report, fmt.Errorf("reviewer learning report in %s belongs to fixture_set %q, not %q", options.ReportDirectory, report.FixtureSet, options.FixtureSet)
			}
			if err := validateReviewerLearningResume(report, inputIdentity, mapping, options.ReportDirectory); err != nil {
				return report, err
			}
		} else if !os.IsNotExist(loadErr) {
			return report, loadErr
		}
	} else if !options.Force {
		if _, loadErr := LoadReviewerLearningReport(options.ReportDirectory); loadErr == nil {
			return report, fmt.Errorf("reviewer learning report already exists in %s; use resume or force", options.ReportDirectory)
		} else if !os.IsNotExist(loadErr) {
			return report, loadErr
		}
	}
	if report.StartedAt.IsZero() {
		report.StartedAt = time.Now().UTC()
	}
	report.FixtureSet = options.FixtureSet
	report.ImplementationVersion = ReviewerLearningImplementationVersion
	report.InputIdentity = inputIdentity
	report.ModelMode = reviewerLearningModelMode(options.ModelMode)
	report.LearningMode = ReviewerLearningModeDeep
	report.RunID = lock.RunID
	report.RunStatus = ReviewerLearningRunStatusRunning
	report.ExpectedURLs = len(mapping.Pages)
	report.QualityThreshold = options.QualityThreshold
	report.ReviewReadyThreshold = options.ReviewReadyThreshold
	report.StallWindow = options.StallWindow
	report.MinImprovement = options.MinImprovement
	report.AdaptiveStopEnabled = options.AdaptiveStop
	report.AdaptiveMinPages = options.MinPages
	report.AdaptiveStableStreakTarget = options.StableStreak
	report.updateReviewerLearningSummary()

	processedPages := 0
	for index, goldPage := range mapping.Pages {
		if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
			return report, err
		}
		if index < len(report.Pages) && reviewerLearningPageComplete(report.Pages[index]) && report.Pages[index].BaselineStatus != BaselineFetchFailed {
			continue
		}
		pageNumber := index + 1
		if err := refreshReviewerLearningLock(options.ReportDirectory, lock); err != nil {
			return report, err
		}
		page, err := runDeepReviewerLearningPage(ctx, options, &report, goldPage, pageNumber, overrides, lock)
		if cancelErr := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock, page); cancelErr != nil {
			if err != nil && err != ctx.Err() {
				return report, errors.Join(cancelErr, err)
			}
			return report, cancelErr
		}
		if err != nil {
			return report, err
		}
		storeReviewerLearningPage(&report, page)
		processedPages++
		report.updateReviewerLearningSummary()
		if err := WriteReviewerLearningReports(options.ReportDirectory, report, lock); err != nil {
			return report, err
		}
		writeReviewerLearningProgress(options.ProgressWriter, report, page)
		if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
			return report, err
		}
		if options.AdaptiveStop && len(report.Pages) >= options.MinPages && report.CurrentStableStreak >= options.StableStreak {
			report.RunStatus = ReviewerLearningRunStatusStoppedStable
			report.CompletedAt = time.Now().UTC()
			report.updateReviewerLearningSummary()
			if err := WriteReviewerLearningReports(options.ReportDirectory, report, lock); err != nil {
				return report, err
			}
			if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
				return report, err
			}
			return report, nil
		}
		if options.ChunkPages > 0 && processedPages >= options.ChunkPages && reviewerLearningHasPendingPages(report, index+1, len(mapping.Pages)) {
			report.RunStatus = ReviewerLearningRunStatusPausedChunk
			report.CompletedAt = time.Time{}
			report.updateReviewerLearningSummary()
			if err := WriteReviewerLearningReports(options.ReportDirectory, report, lock); err != nil {
				return report, err
			}
			if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
				return report, err
			}
			return report, nil
		}
	}
	if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
		return report, err
	}
	report.RunStatus = reviewerLearningCompletedStatus(report)
	report.CompletedAt = time.Now().UTC()
	report.updateReviewerLearningSummary()
	if err := WriteReviewerLearningReports(options.ReportDirectory, report, lock); err != nil {
		return report, err
	}
	if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
		return report, err
	}
	return report, nil
}

func reviewerLearningInputIdentity(options ReviewerLearningOptions, mapping GoldMapping, overrides GoldOverrides) (string, error) {
	fixtureDirectory := options.FixtureDirectory
	if fixtureDirectory != "" {
		var err error
		fixtureDirectory, err = filepath.Abs(fixtureDirectory)
		if err != nil {
			return "", fmt.Errorf("resolve reviewer learning fixture directory: %w", err)
		}
	}
	learningMode := options.LearningMode
	if learningMode == "" {
		learningMode = ReviewerLearningModeAdaptive
	}
	var modelConfig *OpenAIConfig
	if client, ok := options.Model.(*OpenAIClient); ok {
		config := client.config
		config.APIKey = ""
		modelConfig = &config
	}
	inputs := struct {
		Version              int
		ExtractionVersion    string
		LearningMode         ReviewerLearningMode
		ModelMode            string
		ModelConfig          *OpenAIConfig
		Mapping              GoldMapping
		Overrides            GoldOverrides
		FixtureDirectory     string
		LiveFetch            bool
		LiveFallback         bool
		Scope                string
		SourceSkill          SourceSkill
		QualityThreshold     float64
		ReviewReadyThreshold float64
		AdaptiveStop         bool
		MinPages             int
		StableStreak         int
		MaxReviewerTurns     int
		StallWindow          int
		MinImprovement       float64
	}{
		Version:              2,
		ExtractionVersion:    ExtractionVersion,
		LearningMode:         learningMode,
		ModelMode:            reviewerLearningModelMode(options.ModelMode),
		ModelConfig:          modelConfig,
		Mapping:              mapping,
		Overrides:            overrides,
		FixtureDirectory:     fixtureDirectory,
		LiveFetch:            options.LiveFetch,
		LiveFallback:         options.Fetcher != nil,
		Scope:                options.Scope,
		SourceSkill:          options.SourceSkill,
		QualityThreshold:     options.QualityThreshold,
		ReviewReadyThreshold: options.ReviewReadyThreshold,
		AdaptiveStop:         options.AdaptiveStop,
		MinPages:             options.MinPages,
		StableStreak:         options.StableStreak,
		MaxReviewerTurns:     options.MaxReviewerTurns,
		StallWindow:          options.StallWindow,
		MinImprovement:       options.MinImprovement,
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return "", fmt.Errorf("encode reviewer learning input identity: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func validateReviewerLearningResume(report ReviewerLearningReport, identity string, mapping GoldMapping, directory string) error {
	if reviewerLearningReportHasContent(report) && report.ImplementationVersion != ReviewerLearningImplementationVersion {
		return fmt.Errorf("reviewer learning report in %s has implementation version %q, expected %q; use a different report directory for a fresh run", directory, report.ImplementationVersion, ReviewerLearningImplementationVersion)
	}
	if reviewerLearningReportHasContent(report) && (report.InputIdentity == "" || report.InputIdentity != identity) {
		return fmt.Errorf("reviewer learning report in %s has missing or incompatible input identity; use a different report directory for a fresh run", directory)
	}
	if len(report.Pages) > len(mapping.Pages) {
		return fmt.Errorf("reviewer learning report in %s contains more pages than the selected baseline; use a different report directory for a fresh run", directory)
	}
	for index, page := range report.Pages {
		if page.URL != mapping.Pages[index].URL {
			return fmt.Errorf("reviewer learning report in %s does not match the selected URL sequence at page %d; use a different report directory for a fresh run", directory, index+1)
		}
	}
	return nil
}

func storeReviewerLearningPage(report *ReviewerLearningReport, page ReviewerLearningPage) {
	index := page.Index - 1
	if index < len(report.Pages) {
		report.Pages[index] = page
	} else {
		report.Pages = append(report.Pages, page)
	}
}

func reviewerLearningHasPendingPages(report ReviewerLearningReport, startIndex, expectedPages int) bool {
	if len(report.Pages) < expectedPages {
		return true
	}
	for _, page := range report.Pages[startIndex:] {
		if page.BaselineStatus == BaselineFetchFailed || !reviewerLearningPageComplete(page) {
			return true
		}
	}
	return false
}

func reviewerLearningReportHasContent(report ReviewerLearningReport) bool {
	return report.FixtureSet != "" ||
		report.RunStatus != "" ||
		len(report.Pages) > 0 ||
		len(report.Rules) > 0 ||
		report.ExpectedURLs > 0 ||
		report.DiscoveredURLs > 0
}

func runDeepReviewerLearningPage(ctx context.Context, options ReviewerLearningOptions, report *ReviewerLearningReport, goldPage GoldPage, pageNumber int, overrides GoldOverrides, lock ReviewerLearningLock) (ReviewerLearningPage, error) {
	html, readErr := readBenchmarkHTML(ctx, BenchmarkOptions{
		FixtureDirectory: options.FixtureDirectory,
		Fetcher:          options.Fetcher,
		LiveFetch:        options.LiveFetch,
	}, goldPage)
	if err := ctx.Err(); err != nil {
		return ReviewerLearningPage{Index: pageNumber, URL: goldPage.URL, BaselineStatus: BaselineFetchFailed, FetchError: err.Error()}, err
	}
	if readErr != nil {
		report.MissingURLs = append(report.MissingURLs, goldPage.URL)
		return ReviewerLearningPage{Index: pageNumber, URL: goldPage.URL, BaselineStatus: BaselineFetchFailed, FetchError: readErr.Error()}, nil
	}
	report.DiscoveredURLs++
	rawGold, extractionErr := ExtractGoldSections(goldPage, html)
	if extractionErr != nil {
		return ReviewerLearningPage{}, extractionErr
	}
	baselineStatus := baselineStatusForGold(goldPage, rawGold, overrides)
	gold := NormalizeSemanticGold(rawGold)
	if overrideSections := overrides.SectionsForURL(goldPage.URL); len(overrideSections) > 0 {
		rawGold = overrideSections
		gold = overrideSections
		baselineStatus = BaselineAuditedRepair
	}
	report.countReviewerLearningBaselineStatus(baselineStatus)
	page := ReviewerLearningPage{
		Index:          pageNumber,
		URL:            goldPage.URL,
		BaselineStatus: baselineStatus,
		RawExpected:    rawGold,
		Expected:       gold,
	}
	if baselineStatus == BaselineFetchFailed || baselineStatus == BaselineZeroGold {
		return page, nil
	}
	blocks, _, extractionErr := BuildCandidateBlocks(strings.NewReader(string(html)), goldPage.URL)
	if extractionErr != nil {
		return ReviewerLearningPage{}, extractionErr
	}
	config := CrawlConfig{Scope: options.Scope, SourceSkill: options.SourceSkill}
	pageVersion := PageVersion{URL: goldPage.URL, Title: pageTitle(string(html))}

	page.FirstPass = runReviewerLearningLayer(ctx, options.Model, config, pageVersion, blocks, gold, nil)
	report.addLayerUsage(page.FirstPass)
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if err := checkpointDeepModelError(options.ReportDirectory, report, page, "first_pass", page.FirstPass, lock); err != nil {
		return page, err
	}
	existingRules := applicableLearningRules(report.Rules, goldPage.URL, true)
	page.ActiveRuleIDs = skillRuleIDs(existingRules)
	if len(existingRules) == 0 {
		page.WithExistingRules = page.FirstPass
	} else {
		page.WithExistingRules = runReviewerLearningLayer(ctx, options.Model, config, pageVersion, blocks, gold, existingRules)
		report.addLayerUsage(page.WithExistingRules)
		if err := ctx.Err(); err != nil {
			return page, err
		}
		if err := checkpointDeepModelError(options.ReportDirectory, report, page, "with_existing_rules", page.WithExistingRules, lock); err != nil {
			return page, err
		}
		report.recordDeepRuleEffects(existingRules, goldPage.URL, page.FirstPass.Metrics, page.WithExistingRules.Metrics, options.MinImprovement)
	}
	page.ReviewReady = reviewerLearningPasses(page.WithExistingRules.Metrics, options.ReviewReadyThreshold)
	page.StrictPass = reviewerLearningPasses(page.WithExistingRules.Metrics, options.QualityThreshold)
	if reviewerLearningPasses(page.FirstPass.Metrics, options.QualityThreshold) {
		page.FinalApproved = page.FirstPass
		page.FinalApprovalMethod = ReviewerLearningApprovalFirstPass
		page.StrictPass = true
		page.ReviewReady = true
		finalizeReviewerLearningPage(&page)
		return page, nil
	}
	if page.StrictPass {
		page.FinalApproved = page.WithExistingRules
		page.FinalApprovalMethod = ReviewerLearningApprovalExistingRules
		page.FixedByExistingRules = len(existingRules) > 0
		page.AutonomousAfterLearning = len(existingRules) > 0
		page.TurnsUntilStrictPass = 0
		report.recordRuleReuse(existingRules, goldPage.URL)
		finalizeReviewerLearningPage(&page)
		return page, nil
	}

	currentLayer := page.WithExistingRules
	usedPrompts := usedReviewerLearningPrompts(report.Rules, goldPage.URL)
	nonProgress := 0
	for {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		prompt, scope, family := reviewerLearningPromptFromEvidenceWithBlocks(goldPage.URL, gold, currentLayer.Actual, blocks, currentLayer.Metrics, usedPrompts)
		duplicate := usedPrompts[prompt]
		usedPrompts[prompt] = true
		identity := report.nextRuleIdentity()
		rule := ReviewerLearningRule{
			ID:             identity,
			Version:        identity,
			Scope:          scope,
			OriginURL:      originURLForScope(scope, goldPage.URL),
			Instruction:    prompt,
			Status:         "active",
			MismatchLabels: currentLayer.MismatchLabels,
			CreatedOnPage:  pageNumber,
			CreatedOnTurn:  page.ReviewerTurns + 1,
		}
		report.Rules = append(report.Rules, rule)
		newRules := applicableLearningRules(report.Rules, goldPage.URL, true)
		afterLayer := runReviewerLearningLayer(ctx, options.Model, config, pageVersion, blocks, gold, newRules)
		report.addLayerUsage(afterLayer)
		ruleHelped := reviewerLearningRuleHelped(currentLayer.Metrics, afterLayer.Metrics, options.MinImprovement)
		retainBestReviewerPromptLayer(&page, afterLayer)
		page.ReviewerTurnDetails = append(page.ReviewerTurnDetails, ReviewerLearningTurn{
			Turn:           len(page.ReviewerTurnDetails) + 1,
			RuleID:         rule.ID,
			Scope:          scope,
			Prompt:         prompt,
			BeforeMetrics:  currentLayer.Metrics,
			AfterMetrics:   afterLayer.Metrics,
			MismatchLabels: append(currentLayer.MismatchLabels, family),
			ModelError:     afterLayer.ModelError,
		})
		page.ReviewerTurns = len(page.ReviewerTurnDetails)
		page.ReviewerPrompt = prompt
		page.ReviewerRuleScope = scope
		page.ReviewerRuleID = rule.ID
		if err := ctx.Err(); err != nil {
			return page, err
		}
		if afterLayer.ModelError != "" {
			nonProgress++
			page.StallReason = fmt.Sprintf("model call failed during after_new_reviewer_prompt: %s", afterLayer.ModelError)
			if err := checkpointDeepPage(options.ReportDirectory, report, page, lock); err != nil {
				return page, err
			}
			if nonProgress < options.StallWindow {
				continue
			}
		} else {
			report.recordDeepRuleEffects(newRules, goldPage.URL, currentLayer.Metrics, afterLayer.Metrics, options.MinImprovement)
			if reviewerLearningPasses(afterLayer.Metrics, options.ReviewReadyThreshold) && page.TurnsUntilReviewReady == 0 {
				page.TurnsUntilReviewReady = page.ReviewerTurns
				page.ReviewReady = true
			}
			if err := checkpointDeepPage(options.ReportDirectory, report, page, lock); err != nil {
				return page, err
			}
			if reviewerLearningPasses(afterLayer.Metrics, options.QualityThreshold) {
				page.FinalApproved = afterLayer
				page.FinalApprovalMethod = ReviewerLearningApprovalNewReviewerPrompt
				page.StrictPass = true
				page.ReviewReady = true
				page.TurnsUntilStrictPass = page.ReviewerTurns
				maybePromoteHelpfulRule(report, rule, family, goldPage.URL, ruleHelped)
				finalizeReviewerLearningPage(&page)
				return page, nil
			}
			if !ruleHelped || duplicate {
				nonProgress++
			} else {
				nonProgress = 0
				maybePromoteHelpfulRule(report, rule, family, goldPage.URL, ruleHelped)
			}
			if afterLayer.Metrics.RelevantTextF1 >= currentLayer.Metrics.RelevantTextF1 {
				currentLayer = afterLayer
			}
		}
		if nonProgress >= options.StallWindow {
			if oraclePrompt, ok := reviewerLearningOraclePrompt(goldPage.URL, gold, currentLayer.Actual, blocks, usedPrompts); ok {
				usedPrompts[oraclePrompt] = true
				identity := report.nextRuleIdentity()
				rule := ReviewerLearningRule{
					ID:             identity,
					Version:        identity,
					Scope:          RuleScopeURL,
					OriginURL:      goldPage.URL,
					Instruction:    oraclePrompt,
					Status:         "active",
					MismatchLabels: currentLayer.MismatchLabels,
					CreatedOnPage:  pageNumber,
					CreatedOnTurn:  page.ReviewerTurns + 1,
				}
				report.Rules = append(report.Rules, rule)
				afterOracle := runReviewerLearningLayer(ctx, options.Model, config, pageVersion, blocks, gold, applicableLearningRules(report.Rules, goldPage.URL, true))
				report.addLayerUsage(afterOracle)
				retainBestReviewerPromptLayer(&page, afterOracle)
				page.ReviewerTurnDetails = append(page.ReviewerTurnDetails, ReviewerLearningTurn{
					Turn:           len(page.ReviewerTurnDetails) + 1,
					RuleID:         rule.ID,
					Scope:          RuleScopeURL,
					Prompt:         oraclePrompt,
					BeforeMetrics:  currentLayer.Metrics,
					AfterMetrics:   afterOracle.Metrics,
					MismatchLabels: append(currentLayer.MismatchLabels, "oracle_exact_block_id_review"),
					ModelError:     afterOracle.ModelError,
				})
				page.ReviewerTurns = len(page.ReviewerTurnDetails)
				page.ReviewerPrompt = oraclePrompt
				page.ReviewerRuleScope = RuleScopeURL
				page.ReviewerRuleID = rule.ID
				if err := ctx.Err(); err != nil {
					return page, err
				}
				if err := checkpointDeepPage(options.ReportDirectory, report, page, lock); err != nil {
					return page, err
				}
				if afterOracle.ModelError == "" && reviewerLearningPasses(afterOracle.Metrics, options.QualityThreshold) {
					page.FinalApproved = afterOracle
					page.FinalApprovalMethod = ReviewerLearningApprovalOraclePrompt
					page.ReviewReady = true
					page.StrictPass = true
					page.TurnsUntilStrictPass = page.ReviewerTurns
					if page.TurnsUntilReviewReady == 0 {
						page.TurnsUntilReviewReady = page.ReviewerTurns
					}
					finalizeReviewerLearningPage(&page)
					return page, nil
				}
				if afterOracle.ModelError == "" {
					if reviewerLearningPasses(afterOracle.Metrics, options.ReviewReadyThreshold) {
						page.ReviewReady = true
						if page.TurnsUntilReviewReady == 0 {
							page.TurnsUntilReviewReady = page.ReviewerTurns
						}
					}
					currentLayer = afterOracle
				}
			}
			auditedEdit, operations, sourceEvidenced := auditedReviewerEditLayer(blocks, gold, currentLayer.Actual)
			page.AuditedEditOperations = operations
			if sourceEvidenced && auditedEdit.Metrics.ExactParity {
				page.FinalApproved = auditedEdit
				page.FinalApprovalMethod = ReviewerLearningApprovalAuditedBlockSelect
				page.StallReason = fmt.Sprintf("model did not reach strict %.2f F1 after exact reviewer block-id prompts; benchmark applied exact audited reviewer edits from source evidence", options.QualityThreshold)
			} else {
				page.FinalApproved = auditedHumanEditLayer(gold)
				page.FinalApprovalMethod = ReviewerLearningApprovalStalledHumanEdit
				page.StallReason = fmt.Sprintf("model did not reach strict %.2f F1 after exact reviewer block-id prompts; audited reviewer edits only reached %.4f F1 with complete source evidence=%t, so final parity required audited human edit", options.QualityThreshold, auditedEdit.Metrics.RelevantTextF1, sourceEvidenced)
			}
			page.ReviewReady = reviewerLearningPasses(currentLayer.Metrics, options.ReviewReadyThreshold)
			page.Stalled = true
			finalizeReviewerLearningPage(&page)
			return page, nil
		}
	}
}

func retainBestReviewerPromptLayer(page *ReviewerLearningPage, layer ReviewerLearningLayer) {
	if len(page.AfterNewReviewerPrompt.Actual) == 0 ||
		layer.Metrics.RelevantTextF1 > page.AfterNewReviewerPrompt.Metrics.RelevantTextF1 {
		page.AfterNewReviewerPrompt = layer
	}
}

func reviewerLearningCompletedStatus(report ReviewerLearningReport) ReviewerLearningRunStatus {
	if report.FetchFailedPages > 0 {
		return ReviewerLearningRunStatusIncomplete
	}
	return ReviewerLearningRunStatusCompleted
}

func checkpointDeepModelError(directory string, report *ReviewerLearningReport, page ReviewerLearningPage, layerName string, layer ReviewerLearningLayer, lock ReviewerLearningLock) error {
	if layer.ModelError == "" {
		return nil
	}
	page.StallReason = fmt.Sprintf("model call failed during %s: %s", layerName, layer.ModelError)
	if err := checkpointDeepPage(directory, report, page, lock); err != nil {
		return err
	}
	return fmt.Errorf("reviewer learning model call failed on page %d (%s) during %s: %s", page.Index, page.URL, layerName, layer.ModelError)
}

func finalizeReviewerLearningPage(page *ReviewerLearningPage) {
	page.SemanticGoldMetrics = page.FinalApproved.Metrics
	if len(page.RawExpected) > 0 && len(page.FinalApproved.Actual) > 0 {
		page.RawSelectorMetrics = EvaluateBenchmark(page.RawExpected, page.FinalApproved.Actual)
	}
	page.SoftReviewCandidate = reviewerLearningSoftReviewCandidate(reviewerLearningBestModelFacingMetrics(*page))
	page.ManualEditRequired = page.FinalApprovalMethod == ReviewerLearningApprovalAuditedHumanEdit ||
		page.FinalApprovalMethod == ReviewerLearningApprovalAuditedBlockSelect ||
		page.FinalApprovalMethod == ReviewerLearningApprovalStalledHumanEdit ||
		page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict
}

func reviewerLearningOraclePrompt(url string, gold, actual []BenchmarkSection, blocks []CandidateBlock, used map[string]bool) (string, bool) {
	ids := missingReviewerBlockIDs(gold, actual, blocks)
	if len(ids) == 0 {
		return "", false
	}
	prompt := fmt.Sprintf("For this page only: final reviewer correction for %s. Include these exact missing source block_id values with include=true: %s. Keep existing source content selected by the human baseline, and drop only standalone UI boilerplate absent from that baseline.", url, strings.Join(ids, ", "))
	if used[prompt] {
		return "", false
	}
	return prompt, true
}

func missingReviewerBlockIDs(gold, actual []BenchmarkSection, blocks []CandidateBlock) []string {
	seen := map[string]bool{}
	var ids []string
	for _, expected := range gold {
		if sectionTextCoveredByCollection(expected, actual, nil, 0.8) {
			continue
		}
		for _, id := range matchingCandidateBlockIDs(expected, blocks) {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func auditedReviewerEditLayer(blocks []CandidateBlock, gold, actual []BenchmarkSection) (ReviewerLearningLayer, []ReviewerLearningAuditedEditOperation, bool) {
	sections := make([]BenchmarkSection, 0, len(gold))
	operations := make([]ReviewerLearningAuditedEditOperation, 0, len(gold)+len(actual))
	sourceEvidenced := true
	usedActual := map[int]bool{}

	for _, expected := range gold {
		actualIndex, actualSection, actualOK := bestAuditedActualEvidence(expected, actual)
		if actualOK {
			usedActual[actualIndex] = true
			if benchmarkSectionsExact(expected, actualSection) {
				operations = append(operations, reviewerLearningAuditedEditOperation("keep", expected, "reviewer kept the section already emitted by the model"))
			} else {
				operations = append(operations, reviewerLearningAuditedEditOperation("correct_metadata", expected, "reviewer corrected role, type, parse mode, block id, or text to match the approved source baseline"))
			}
			sections = append(sections, expected)
			continue
		}

		if len(matchingCandidateBlockIDs(expected, blocks)) > 0 {
			operations = append(operations, reviewerLearningAuditedEditOperation("include_from_source_block", expected, "reviewer added a missing section that exists in the extracted source blocks"))
			sections = append(sections, expected)
			continue
		}

		sourceEvidenced = false
		operations = append(operations, reviewerLearningAuditedEditOperation("copy_from_human_gold", expected, "reviewer could not map this section to current source blocks, so final parity requires an audited human-gold edit"))
		sections = append(sections, expected)
	}

	for index, section := range actual {
		if usedActual[index] {
			continue
		}
		if sectionTextCoveredByCollection(section, gold, nil, 0.8) {
			continue
		}
		operations = append(operations, reviewerLearningAuditedEditOperation("drop_extra", section, "reviewer removed model output that is not part of the approved semantic baseline"))
	}

	metrics := EvaluateBenchmark(gold, sections)
	return ReviewerLearningLayer{Actual: sections, Metrics: metrics, MismatchLabels: classifyMismatches(metrics)}, operations, sourceEvidenced
}

func bestAuditedActualEvidence(expected BenchmarkSection, actual []BenchmarkSection) (int, BenchmarkSection, bool) {
	bestIndex := -1
	var best BenchmarkSection
	bestScore := 0.0
	for index, candidate := range actual {
		score := maxFloat(textOverlap(candidate.Text, expected.Text), textOverlap(expected.Text, candidate.Text))
		if candidate.BlockID == expected.BlockID && score >= 0.5 {
			score += 1
		}
		if score > bestScore && score >= 0.8 {
			bestIndex = index
			best = candidate
			bestScore = score
		}
	}
	if bestIndex == -1 {
		return 0, BenchmarkSection{}, false
	}
	return bestIndex, best, true
}

func benchmarkSectionsExact(left, right BenchmarkSection) bool {
	return left.BlockID == right.BlockID &&
		left.Role == right.Role &&
		left.Type == right.Type &&
		left.ParseMode == right.ParseMode &&
		normalizeText(left.Text) == normalizeText(right.Text)
}

func reviewerLearningAuditedEditOperation(operation string, section BenchmarkSection, reason string) ReviewerLearningAuditedEditOperation {
	return ReviewerLearningAuditedEditOperation{
		Operation:   operation,
		BlockID:     section.BlockID,
		Role:        section.Role,
		Type:        section.Type,
		ParseMode:   section.ParseMode,
		TextSnippet: reviewerLearningSnippet(section.Text),
		Reason:      reason,
	}
}

func auditedHumanEditLayer(gold []BenchmarkSection) ReviewerLearningLayer {
	sections := append([]BenchmarkSection(nil), gold...)
	metrics := EvaluateBenchmark(gold, sections)
	return ReviewerLearningLayer{Actual: sections, Metrics: metrics, MismatchLabels: classifyMismatches(metrics)}
}

func checkpointDeepPage(directory string, report *ReviewerLearningReport, page ReviewerLearningPage, lock ReviewerLearningLock) error {
	checkpoint := *report
	checkpoint.Pages = append([]ReviewerLearningPage(nil), report.Pages...)
	storeReviewerLearningPage(&checkpoint, page)
	checkpoint.updateReviewerLearningSummary()
	return WriteReviewerLearningReports(directory, checkpoint, lock)
}

func maybePromoteHelpfulRule(report *ReviewerLearningReport, rule ReviewerLearningRule, family, url string, helped bool) {
	if !helped || rule.Scope == RuleScopeSource || family == "page_specific_content" {
		return
	}
	var matching []int
	for _, existing := range report.Rules {
		if existing.Scope != RuleScopeURL || existing.ID == rule.ID || reviewerLearningPromptFamily(existing.Instruction) != family {
			continue
		}
		matching = append(matching, existing.ID)
	}
	if len(matching) == 0 {
		return
	}
	instruction := promptForEvidence(reviewerLearningPromptEvidence{Category: family, Snippet: "repeated Sample Source pattern", Scope: RuleScopeSource})
	for index := range report.Rules {
		if report.Rules[index].Scope != RuleScopeSource || report.Rules[index].Instruction != instruction {
			continue
		}
		report.Rules[index].PromotedFromURLRules = appendUniqueInt(report.Rules[index].PromotedFromURLRules, matching...)
		report.Rules[index].PromotedFromURLRules = appendUniqueInt(report.Rules[index].PromotedFromURLRules, rule.ID)
		return
	}
	identity := report.nextRuleIdentity()
	promoted := ReviewerLearningRule{
		ID:                   identity,
		Version:              identity,
		Scope:                RuleScopeSource,
		Instruction:          instruction,
		Status:               "active",
		CreatedOnPage:        rule.CreatedOnPage,
		CreatedOnTurn:        rule.CreatedOnTurn,
		PromotedFromURLRules: appendUniqueInt(matching, rule.ID),
	}
	report.Rules = append(report.Rules, promoted)
}

func (report *ReviewerLearningReport) recordDeepRuleEffects(rules []SkillRule, url string, before, after BenchmarkMetrics, minImprovement float64) {
	delta := after.RelevantTextF1 - before.RelevantTextF1
	for _, active := range rules {
		if active.Scope != RuleScopeSource {
			continue
		}
		for index := range report.Rules {
			if report.Rules[index].ID != int(active.ID) {
				continue
			}
			report.Rules[index].AttemptedOnURLs = appendUniqueString(report.Rules[index].AttemptedOnURLs, url)
			switch {
			case reviewerLearningRuleHelped(before, after, minImprovement) || reviewerLearningPasses(after, report.QualityThreshold) && !reviewerLearningPasses(before, report.QualityThreshold):
				markReviewerLearningRuleHelped(&report.Rules[index], url)
			case delta < -minImprovement:
				markReviewerLearningRuleHurt(&report.Rules[index], url)
			default:
				markReviewerLearningRuleNeutral(&report.Rules[index], url)
			}
		}
	}
}

func reviewerLearningRuleHelped(before, after BenchmarkMetrics, minImprovement float64) bool {
	if after.RelevantTextF1-before.RelevantTextF1 > minImprovement {
		return true
	}
	return reviewerLearningClinicalCoverageImproved(before, after)
}

func reviewerLearningClinicalCoverageImproved(before, after BenchmarkMetrics) bool {
	return after.CriticalRecall > before.CriticalRecall ||
		after.TableRecall > before.TableRecall ||
		after.MediaRecall > before.MediaRecall
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueInt(values []int, additions ...int) []int {
	for _, value := range additions {
		exists := false
		for _, existing := range values {
			if existing == value {
				exists = true
				break
			}
		}
		if !exists {
			values = append(values, value)
		}
	}
	return values
}

func markReviewerLearningRuleHelped(rule *ReviewerLearningRule, url string) {
	rule.HelpedURLs = appendUniqueString(rule.HelpedURLs, url)
	rule.HurtURLs = removeString(rule.HurtURLs, url)
	rule.NeutralURLs = removeString(rule.NeutralURLs, url)
	rule.ReuseCount = len(rule.HelpedURLs)
}

func markReviewerLearningRuleHurt(rule *ReviewerLearningRule, url string) {
	if containsStringValue(rule.HelpedURLs, url) {
		return
	}
	rule.HurtURLs = appendUniqueString(rule.HurtURLs, url)
	rule.NeutralURLs = removeString(rule.NeutralURLs, url)
}

func markReviewerLearningRuleNeutral(rule *ReviewerLearningRule, url string) {
	if containsStringValue(rule.HelpedURLs, url) || containsStringValue(rule.HurtURLs, url) {
		return
	}
	rule.NeutralURLs = appendUniqueString(rule.NeutralURLs, url)
}

func containsStringValue(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func removeString(values []string, value string) []string {
	result := values[:0]
	for _, existing := range values {
		if existing != value {
			result = append(result, existing)
		}
	}
	return result
}
