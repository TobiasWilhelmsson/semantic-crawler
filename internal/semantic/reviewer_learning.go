package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ReviewerLearningApprovalFirstPass           = "model_first_pass"
	ReviewerLearningApprovalExistingRules       = "model_with_existing_rules"
	ReviewerLearningApprovalNewReviewerPrompt   = "model_after_new_reviewer_prompt"
	ReviewerLearningApprovalOraclePrompt        = "model_after_oracle_reviewer_prompt"
	ReviewerLearningApprovalReviewReadyNoStrict = "review_ready_model_output_needs_human_confirmation"
	ReviewerLearningApprovalAuditedHumanEdit    = "audited_human_edit"
	ReviewerLearningApprovalStalledHumanEdit    = "requires_manual_audited_edit_after_stalled_prompt_loop"
	ReviewerLearningApprovalAuditedBlockSelect  = "audited_block_selection_after_prompt_loop"
)

type ReviewerLearningMode string

const (
	ReviewerLearningModeAdaptive ReviewerLearningMode = "adaptive"
	ReviewerLearningModeDeep     ReviewerLearningMode = "deep"
)

const ReviewerLearningImplementationVersion = "semantic-reviewer-learning-deep-v23-source-fidelity"

type ReviewerLearningRunStatus string

const (
	ReviewerLearningRunStatusRunning               ReviewerLearningRunStatus = "running"
	ReviewerLearningRunStatusCompleted             ReviewerLearningRunStatus = "completed"
	ReviewerLearningRunStatusIncomplete            ReviewerLearningRunStatus = "incomplete_fetch_failed"
	ReviewerLearningRunStatusIncompleteFetchFailed ReviewerLearningRunStatus = ReviewerLearningRunStatusIncomplete
	ReviewerLearningRunStatusStoppedStable         ReviewerLearningRunStatus = "stopped_after_stable_streak"
	ReviewerLearningRunStatusPausedChunk           ReviewerLearningRunStatus = "paused_after_chunk"
	ReviewerLearningRunStatusNotStarted            ReviewerLearningRunStatus = "not_started"
	ReviewerLearningRunStatusInterrupted           ReviewerLearningRunStatus = "interrupted"
)

type ReviewerLearningOptions struct {
	FixtureSet           string
	FixtureDirectory     string
	MappingPath          string
	URLContains          string
	ReportDirectory      string
	GoldOverridePath     string
	Scope                string
	SourceSkill          SourceSkill
	Model                SemanticModel
	ModelMode            string
	Fetcher              Fetcher
	LiveFetch            bool
	StartPage            int
	MaxPages             int
	QualityThreshold     float64
	ReviewReadyThreshold float64
	AdaptiveStop         bool
	MinPages             int
	StableStreak         int
	ChunkPages           int
	MaxReviewerTurns     int
	LearningMode         ReviewerLearningMode
	StallWindow          int
	MinImprovement       float64
	LockTimeout          time.Duration
	Resume               bool
	Force                bool
	ProgressWriter       io.Writer
}

type ReviewerLearningLayer struct {
	Actual         []BenchmarkSection `json:"actual,omitempty"`
	Metrics        BenchmarkMetrics   `json:"metrics"`
	MismatchLabels []string           `json:"mismatch_labels,omitempty"`
	InputTokens    int                `json:"input_tokens"`
	OutputTokens   int                `json:"output_tokens"`
	LatencyMS      int64              `json:"latency_ms"`
	ModelError     string             `json:"model_error,omitempty"`
}

type ReviewerLearningPage struct {
	Index                    int                                    `json:"index"`
	URL                      string                                 `json:"url"`
	BaselineStatus           BaselineStatus                         `json:"baseline_status"`
	FetchError               string                                 `json:"fetch_error,omitempty"`
	RawExpected              []BenchmarkSection                     `json:"raw_expected,omitempty"`
	Expected                 []BenchmarkSection                     `json:"expected,omitempty"`
	FirstPass                ReviewerLearningLayer                  `json:"first_pass"`
	WithExistingRules        ReviewerLearningLayer                  `json:"with_existing_rules"`
	AfterNewReviewerPrompt   ReviewerLearningLayer                  `json:"after_new_reviewer_prompt,omitempty"`
	FinalApproved            ReviewerLearningLayer                  `json:"final_approved"`
	RawSelectorMetrics       BenchmarkMetrics                       `json:"raw_selector_metrics"`
	SemanticGoldMetrics      BenchmarkMetrics                       `json:"semantic_gold_metrics"`
	FinalApprovalMethod      string                                 `json:"final_approval_method"`
	ReviewerPrompt           string                                 `json:"reviewer_prompt,omitempty"`
	ReviewerRuleScope        RuleScope                              `json:"reviewer_rule_scope,omitempty"`
	ReviewerRuleID           int                                    `json:"reviewer_rule_id,omitempty"`
	ReviewerTurnDetails      []ReviewerLearningTurn                 `json:"reviewer_turn_details,omitempty"`
	AuditedEditOperations    []ReviewerLearningAuditedEditOperation `json:"audited_edit_operations,omitempty"`
	ReviewerTurns            int                                    `json:"reviewer_turns"`
	SoftReviewCandidate      bool                                   `json:"soft_review_candidate"`
	ReviewReady              bool                                   `json:"review_ready"`
	StrictPass               bool                                   `json:"strict_pass"`
	Stalled                  bool                                   `json:"stalled"`
	StallReason              string                                 `json:"stall_reason,omitempty"`
	ManualEditRequired       bool                                   `json:"manual_edit_required"`
	TurnsUntilStrictPass     int                                    `json:"turns_until_strict_pass,omitempty"`
	TurnsUntilReviewReady    int                                    `json:"turns_until_review_ready,omitempty"`
	FixedByExistingRules     bool                                   `json:"fixed_by_existing_rules"`
	AutonomousAfterLearning  bool                                   `json:"autonomous_after_learning"`
	ActiveRuleIDs            []int                                  `json:"active_rule_ids,omitempty"`
	PagesSinceLastSourceRule int                                    `json:"pages_since_last_source_rule"`
}

type ReviewerLearningAuditedEditOperation struct {
	Operation   string    `json:"operation"`
	BlockID     string    `json:"block_id,omitempty"`
	Role        BlockRole `json:"role,omitempty"`
	Type        BlockType `json:"type,omitempty"`
	ParseMode   bool      `json:"parse_mode,omitempty"`
	TextSnippet string    `json:"text_snippet,omitempty"`
	Reason      string    `json:"reason,omitempty"`
}

type ReviewerLearningTurn struct {
	Turn           int              `json:"turn"`
	RuleID         int              `json:"rule_id"`
	Scope          RuleScope        `json:"scope"`
	Prompt         string           `json:"prompt"`
	BeforeMetrics  BenchmarkMetrics `json:"before_metrics"`
	AfterMetrics   BenchmarkMetrics `json:"after_metrics"`
	MismatchLabels []string         `json:"mismatch_labels,omitempty"`
	ModelError     string           `json:"model_error,omitempty"`
}

type ReviewerLearningRule struct {
	ID                   int       `json:"id"`
	Version              int       `json:"version"`
	Scope                RuleScope `json:"scope"`
	OriginURL            string    `json:"origin_url,omitempty"`
	Instruction          string    `json:"instruction"`
	Status               string    `json:"status"`
	MismatchLabels       []string  `json:"mismatch_labels,omitempty"`
	CreatedOnPage        int       `json:"created_on_page"`
	CreatedOnTurn        int       `json:"created_on_turn,omitempty"`
	ReuseCount           int       `json:"reuse_count"`
	AttemptedOnURLs      []string  `json:"attempted_on_urls,omitempty"`
	HelpedURLs           []string  `json:"helped_urls,omitempty"`
	HurtURLs             []string  `json:"hurt_urls,omitempty"`
	NeutralURLs          []string  `json:"neutral_urls,omitempty"`
	PromotedFromURLRules []int     `json:"promoted_from_url_rules,omitempty"`
}

type ReviewerLearningReport struct {
	RuleSequence                           int                       `json:"rule_sequence,omitempty"`
	FixtureSet                             string                    `json:"fixture_set"`
	ImplementationVersion                  string                    `json:"implementation_version,omitempty"`
	InputIdentity                          string                    `json:"input_identity,omitempty"`
	ModelMode                              string                    `json:"model_mode"`
	LearningMode                           ReviewerLearningMode      `json:"learning_mode,omitempty"`
	RunID                                  string                    `json:"run_id,omitempty"`
	RunStatus                              ReviewerLearningRunStatus `json:"run_status"`
	StartedAt                              time.Time                 `json:"started_at,omitempty"`
	UpdatedAt                              time.Time                 `json:"updated_at,omitempty"`
	CompletedAt                            time.Time                 `json:"completed_at,omitempty"`
	LastCheckpointAt                       time.Time                 `json:"last_checkpoint_at,omitempty"`
	ExpectedURLs                           int                       `json:"expected_urls"`
	DiscoveredURLs                         int                       `json:"discovered_urls"`
	MissingURLs                            []string                  `json:"missing_urls,omitempty"`
	FetchFailedPages                       int                       `json:"fetch_failed_pages"`
	ProofUsable                            bool                      `json:"proof_usable"`
	ProofBlockedReason                     string                    `json:"proof_blocked_reason,omitempty"`
	LastCompletedPage                      int                       `json:"last_completed_page"`
	Pages                                  []ReviewerLearningPage    `json:"pages"`
	Rules                                  []ReviewerLearningRule    `json:"rules"`
	UsableGoldPages                        int                       `json:"usable_gold_pages"`
	PartialGoldPages                       int                       `json:"partial_gold_pages"`
	StaleSelectorPages                     int                       `json:"stale_selector_pages"`
	RepairedGoldPages                      int                       `json:"repaired_gold_pages"`
	FirstPassAggregate                     BenchmarkMetrics          `json:"first_pass_aggregate"`
	WithExistingRulesAggregate             BenchmarkMetrics          `json:"with_existing_rules_aggregate"`
	AfterNewPromptAggregate                BenchmarkMetrics          `json:"after_new_prompt_aggregate"`
	FinalApprovedAggregate                 BenchmarkMetrics          `json:"final_approved_aggregate"`
	RawSelectorMetrics                     BenchmarkMetrics          `json:"raw_selector_metrics"`
	SemanticGoldMetrics                    BenchmarkMetrics          `json:"semantic_gold_metrics"`
	ModelFirstPassMetrics                  BenchmarkMetrics          `json:"model_first_pass_metrics"`
	ModelAfterRulesMetrics                 BenchmarkMetrics          `json:"model_after_rules_metrics"`
	ModelAfterReviewerPromptMetrics        BenchmarkMetrics          `json:"model_after_reviewer_prompt_metrics"`
	FinalAuditedMetrics                    BenchmarkMetrics          `json:"final_audited_metrics"`
	TotalReviewerPrompts                   int                       `json:"total_reviewer_prompts"`
	TotalReviewerTurns                     int                       `json:"total_reviewer_turns"`
	AverageReviewerTurnsPerPage            float64                   `json:"average_reviewer_turns_per_page"`
	MedianReviewerTurnsPerPage             float64                   `json:"median_reviewer_turns_per_page"`
	PagesRequiringZeroReviewerPrompts      int                       `json:"pages_requiring_zero_reviewer_prompts"`
	PagesFixedByExistingRules              int                       `json:"pages_fixed_by_existing_rules"`
	PagesEverFixedByExistingRules          int                       `json:"pages_ever_fixed_by_existing_rules"`
	PagesCurrentlyPassingWithExistingRules int                       `json:"pages_currently_passing_with_existing_rules"`
	PagesWhereExistingRulesHurtScore       int                       `json:"pages_where_existing_rules_hurt_score"`
	PagesRequiringNewPrompt                int                       `json:"pages_requiring_new_prompt"`
	PagesNeedingAuditedEdit                int                       `json:"pages_needing_audited_human_edit"`
	PagesRequiringManualEditAfterStall     int                       `json:"pages_requiring_manual_edit_after_stall"`
	PagesRequiringHumanConfirmation        int                       `json:"pages_requiring_human_confirmation"`
	AutonomousAfterLearningPages           int                       `json:"autonomous_after_learning_pages"`
	StrictModelPassPages                   int                       `json:"strict_model_pass_pages"`
	ReviewReady97Pages                     int                       `json:"review_ready_97_pages"`
	SoftReviewCandidatePages               int                       `json:"soft_review_candidate_pages"`
	SourceRulesHelpedCount                 int                       `json:"source_rules_helped_count"`
	SourceRulesHurtCount                   int                       `json:"source_rules_hurt_count"`
	SourceRulesNeutralCount                int                       `json:"source_rules_neutral_count"`
	CurrentStableStreak                    int                       `json:"current_stable_streak"`
	StableStreakStartPage                  int                       `json:"stable_streak_start_page"`
	MaxStableStreak                        int                       `json:"max_stable_streak"`
	MaxStableStreakStartPage               int                       `json:"max_stable_streak_start_page"`
	MaxStableStreakEndPage                 int                       `json:"max_stable_streak_end_page"`
	AdaptiveStopEnabled                    bool                      `json:"adaptive_stop_enabled"`
	AdaptiveMinPages                       int                       `json:"adaptive_min_pages"`
	AdaptiveStableStreakTarget             int                       `json:"adaptive_stable_streak_target"`
	SourceScopedRulesCreated               int                       `json:"source_scoped_rules_created"`
	URLScopedRulesCreated                  int                       `json:"url_scoped_rules_created"`
	ModelInputTokens                       int                       `json:"model_input_tokens"`
	ModelOutputTokens                      int                       `json:"model_output_tokens"`
	TokenUsageAvailable                    bool                      `json:"token_usage_available"`
	RawUsageMissing                        bool                      `json:"raw_usage_missing"`
	UsageNote                              string                    `json:"usage_note"`
	QualityThreshold                       float64                   `json:"quality_threshold"`
	ReviewReadyThreshold                   float64                   `json:"review_ready_threshold,omitempty"`
	StallWindow                            int                       `json:"stall_window,omitempty"`
	MinImprovement                         float64                   `json:"min_improvement,omitempty"`
}

func RunReviewerLearningBenchmark(ctx context.Context, options ReviewerLearningOptions) (ReviewerLearningReport, error) {
	if options.LearningMode == ReviewerLearningModeDeep {
		return RunDeepReviewerLearningBenchmark(ctx, options)
	}
	if options.Model == nil {
		return ReviewerLearningReport{}, fmt.Errorf("reviewer learning model is required")
	}
	if err := ctx.Err(); err != nil {
		return ReviewerLearningReport{}, err
	}
	lock, err := acquireReviewerLearningLock(options.ReportDirectory, reviewerLearningRunID(), options.LockTimeout, options.Force)
	if err != nil {
		return ReviewerLearningReport{}, err
	}
	defer releaseReviewerLearningLock(options.ReportDirectory, lock)
	if options.QualityThreshold == 0 {
		options.QualityThreshold = 0.98
	}
	if options.MinPages == 0 {
		options.MinPages = 10
	}
	if options.StableStreak == 0 {
		options.StableStreak = 5
	}
	if options.MaxReviewerTurns == 0 {
		options.MaxReviewerTurns = 3
	}
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
		loaded, loadErr := LoadReviewerLearningReport(options.ReportDirectory)
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return report, loadErr
		}
		if loadErr == nil {
			report = loaded
			if report.FixtureSet != "" && report.FixtureSet != options.FixtureSet {
				return report, fmt.Errorf("reviewer learning report in %s belongs to fixture_set %q, not %q; use a different report directory or force", options.ReportDirectory, report.FixtureSet, options.FixtureSet)
			}
			if err := validateReviewerLearningResume(report, inputIdentity, mapping, options.ReportDirectory); err != nil {
				return report, err
			}
			if (report.RunStatus == ReviewerLearningRunStatusCompleted || report.RunStatus == ReviewerLearningRunStatusStoppedStable) && !reviewerLearningHasPendingPages(report, 0, len(mapping.Pages)) {
				if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
					return report, err
				}
				return report, nil
			}
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
	report.RunID = lock.RunID
	report.RunStatus = ReviewerLearningRunStatusRunning
	report.ExpectedURLs = len(mapping.Pages)
	report.QualityThreshold = options.QualityThreshold
	report.AdaptiveStopEnabled = options.AdaptiveStop
	report.AdaptiveMinPages = options.MinPages
	report.AdaptiveStableStreakTarget = options.StableStreak
	report.updateReviewerLearningSummary()
	lastSourceRulePage := lastSourceRulePage(report.Pages)
	for index, goldPage := range mapping.Pages {
		if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
			return report, err
		}
		if index < len(report.Pages) && reviewerLearningPageComplete(report.Pages[index]) && report.Pages[index].BaselineStatus != BaselineFetchFailed {
			continue
		}
		pageNumber := index + 1
		html, readErr := readBenchmarkHTML(ctx, BenchmarkOptions{
			FixtureDirectory: options.FixtureDirectory,
			Fetcher:          options.Fetcher,
			LiveFetch:        options.LiveFetch,
		}, goldPage)
		if cancelErr := ctx.Err(); cancelErr != nil {
			page := ReviewerLearningPage{Index: pageNumber, URL: goldPage.URL, BaselineStatus: BaselineFetchFailed, FetchError: cancelErr.Error()}
			err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock, page)
			return report, err
		}
		if readErr != nil {
			report.MissingURLs = append(report.MissingURLs, goldPage.URL)
			page := ReviewerLearningPage{Index: pageNumber, URL: goldPage.URL, BaselineStatus: BaselineFetchFailed, FetchError: readErr.Error()}
			storeReviewerLearningPage(&report, page)
			report.updateReviewerLearningSummary()
			if err := WriteReviewerLearningReports(options.ReportDirectory, report, lock); err != nil {
				return report, err
			}
			writeReviewerLearningProgress(options.ProgressWriter, report, page)
			if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
				return report, err
			}
			continue
		}
		report.DiscoveredURLs++
		rawGold, extractionErr := ExtractGoldSections(goldPage, html)
		if extractionErr != nil {
			return report, extractionErr
		}
		baselineStatus := baselineStatusForGold(goldPage, rawGold, overrides)
		gold := NormalizeSemanticGold(rawGold)
		if overrideSections := overrides.SectionsForURL(goldPage.URL); len(overrideSections) > 0 {
			rawGold = overrideSections
			gold = overrideSections
			baselineStatus = BaselineAuditedRepair
		}
		report.countReviewerLearningBaselineStatus(baselineStatus)

		blocks, _, extractionErr := BuildCandidateBlocks(strings.NewReader(string(html)), goldPage.URL)
		if extractionErr != nil {
			return report, extractionErr
		}
		config := CrawlConfig{Scope: options.Scope, SourceSkill: options.SourceSkill}
		pageVersion := PageVersion{URL: goldPage.URL, Title: pageTitle(string(html))}

		page := ReviewerLearningPage{
			Index:                    pageNumber,
			URL:                      goldPage.URL,
			BaselineStatus:           baselineStatus,
			RawExpected:              rawGold,
			Expected:                 gold,
			PagesSinceLastSourceRule: pageNumber - lastSourceRulePage,
		}
		if baselineStatus == BaselineFetchFailed || baselineStatus == BaselineZeroGold {
			storeReviewerLearningPage(&report, page)
			report.updateReviewerLearningSummary()
			if err := WriteReviewerLearningReports(options.ReportDirectory, report, lock); err != nil {
				return report, err
			}
			writeReviewerLearningProgress(options.ProgressWriter, report, page)
			if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock); err != nil {
				return report, err
			}
			continue
		}

		page.FirstPass = runReviewerLearningLayer(ctx, options.Model, config, pageVersion, blocks, gold, nil)
		report.addLayerUsage(page.FirstPass)
		if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock, page); err != nil {
			return report, err
		}

		existingRules := applicableLearningRules(report.Rules, goldPage.URL, true)
		page.ActiveRuleIDs = skillRuleIDs(existingRules)
		if len(existingRules) == 0 {
			page.WithExistingRules = page.FirstPass
		} else {
			page.WithExistingRules = runReviewerLearningLayer(ctx, options.Model, config, pageVersion, blocks, gold, existingRules)
			report.addLayerUsage(page.WithExistingRules)
			if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock, page); err != nil {
				return report, err
			}
		}

		if reviewerLearningPasses(page.FirstPass.Metrics, options.QualityThreshold) {
			page.FinalApproved = page.FirstPass
			page.FinalApprovalMethod = ReviewerLearningApprovalFirstPass
		} else if reviewerLearningPasses(page.WithExistingRules.Metrics, options.QualityThreshold) {
			page.FinalApproved = page.WithExistingRules
			page.FinalApprovalMethod = ReviewerLearningApprovalExistingRules
			page.FixedByExistingRules = true
			page.AutonomousAfterLearning = true
			report.recordRuleReuse(existingRules, goldPage.URL)
		} else {
			currentLayer := page.WithExistingRules
			usedPrompts := map[string]bool{}
			for _, rule := range applicableLearningRules(report.Rules, goldPage.URL, true) {
				usedPrompts[rule.Instruction] = true
			}
			for turn := 1; turn <= options.MaxReviewerTurns; turn++ {
				if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock, page); err != nil {
					return report, err
				}
				prompt, scope := generateReviewerLearningPromptAvoiding(goldPage.URL, gold, currentLayer.Actual, currentLayer.Metrics, usedPrompts)
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
				}
				report.Rules = append(report.Rules, rule)
				if scope == RuleScopeSource {
					lastSourceRulePage = pageNumber
				}
				newRules := applicableLearningRules(report.Rules, goldPage.URL, true)
				afterLayer := runReviewerLearningLayer(ctx, options.Model, config, pageVersion, blocks, gold, newRules)
				report.addLayerUsage(afterLayer)
				page.AfterNewReviewerPrompt = afterLayer
				page.ReviewerTurnDetails = append(page.ReviewerTurnDetails, ReviewerLearningTurn{
					Turn:           turn,
					RuleID:         rule.ID,
					Scope:          scope,
					Prompt:         prompt,
					BeforeMetrics:  currentLayer.Metrics,
					AfterMetrics:   afterLayer.Metrics,
					MismatchLabels: currentLayer.MismatchLabels,
				})
				page.ReviewerTurns = len(page.ReviewerTurnDetails)
				page.ReviewerPrompt = prompt
				page.ReviewerRuleScope = scope
				page.ReviewerRuleID = rule.ID
				if err := checkpointReviewerLearningCancellation(ctx, options.ReportDirectory, &report, lock, page); err != nil {
					return report, err
				}
				currentLayer = afterLayer
				if reviewerLearningPasses(afterLayer.Metrics, options.QualityThreshold) {
					page.FinalApproved = afterLayer
					page.FinalApprovalMethod = ReviewerLearningApprovalNewReviewerPrompt
					break
				}
			}
			if page.FinalApprovalMethod == "" {
				page.FinalApproved = ReviewerLearningLayer{Actual: gold, Metrics: EvaluateBenchmark(gold, gold)}
				page.FinalApprovalMethod = ReviewerLearningApprovalAuditedHumanEdit
			}
		}
		finalizeReviewerLearningPage(&page)

		storeReviewerLearningPage(&report, page)
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

func checkpointReviewerLearningCancellation(ctx context.Context, directory string, report *ReviewerLearningReport, lock ReviewerLearningLock, pages ...ReviewerLearningPage) error {
	cancelErr := ctx.Err()
	if cancelErr == nil {
		return nil
	}
	for _, page := range pages {
		if page.Index > 0 && page.URL != "" {
			storeReviewerLearningPage(report, page)
		}
	}
	report.RunID = lock.RunID
	report.RunStatus = ReviewerLearningRunStatusInterrupted
	report.CompletedAt = time.Time{}
	report.updateReviewerLearningSummary()
	return errors.Join(cancelErr, WriteReviewerLearningReports(directory, *report, lock))
}

func writeReviewerLearningProgress(writer io.Writer, report ReviewerLearningReport, page ReviewerLearningPage) {
	if writer == nil {
		return
	}
	fmt.Fprintf(writer, "reviewer-learning page %d/%d status=%s final=%s f1=%.4f turns=%d stable_streak=%d url=%s\n",
		page.Index,
		report.ExpectedURLs,
		report.RunStatus,
		page.FinalApprovalMethod,
		page.FinalApproved.Metrics.RelevantTextF1,
		page.ReviewerTurns,
		report.CurrentStableStreak,
		page.URL,
	)
}

type ReviewerLearningStatus struct {
	RunStatus                              ReviewerLearningRunStatus `json:"run_status"`
	ImplementationVersion                  string                    `json:"implementation_version,omitempty"`
	CurrentImplementationVersion           string                    `json:"current_implementation_version,omitempty"`
	PagesCompleted                         int                       `json:"pages_completed"`
	ExpectedURLs                           int                       `json:"expected_urls"`
	FetchFailedPages                       int                       `json:"fetch_failed_pages"`
	ProofUsable                            bool                      `json:"proof_usable"`
	ProofBlockedReason                     string                    `json:"proof_blocked_reason,omitempty"`
	LastCompletedPage                      int                       `json:"last_completed_page"`
	CurrentStableStreak                    int                       `json:"current_stable_streak"`
	StableStreakStartPage                  int                       `json:"stable_streak_start_page"`
	MaxStableStreak                        int                       `json:"max_stable_streak"`
	MaxStableStreakStartPage               int                       `json:"max_stable_streak_start_page"`
	MaxStableStreakEndPage                 int                       `json:"max_stable_streak_end_page"`
	TotalReviewerPrompts                   int                       `json:"total_reviewer_prompts"`
	PagesFixedByExistingRules              int                       `json:"pages_fixed_by_existing_rules"`
	PagesEverFixedByExistingRules          int                       `json:"pages_ever_fixed_by_existing_rules"`
	PagesCurrentlyPassingWithExistingRules int                       `json:"pages_currently_passing_with_existing_rules"`
	PagesWhereExistingRulesHurtScore       int                       `json:"pages_where_existing_rules_hurt_score"`
	SourceRulesHelpedCount                 int                       `json:"source_rules_helped_count"`
	SourceRulesHurtCount                   int                       `json:"source_rules_hurt_count"`
	SourceRulesNeutralCount                int                       `json:"source_rules_neutral_count"`
	StrictModelPassPages                   int                       `json:"strict_model_pass_pages"`
	ReviewReady97Pages                     int                       `json:"review_ready_97_pages"`
	SoftReviewCandidatePages               int                       `json:"soft_review_candidate_pages"`
	PagesNeedingAuditedEdit                int                       `json:"pages_needing_audited_human_edit"`
	PagesRequiringManualEditAfterStall     int                       `json:"pages_requiring_manual_edit_after_stall"`
	PagesRequiringHumanConfirmation        int                       `json:"pages_requiring_human_confirmation"`
	ModelInputTokens                       int                       `json:"model_input_tokens"`
	ModelOutputTokens                      int                       `json:"model_output_tokens"`
	ReportPath                             string                    `json:"report_path"`
	LearningMode                           ReviewerLearningMode      `json:"learning_mode,omitempty"`
	RunID                                  string                    `json:"run_id,omitempty"`
	LastCheckpointAt                       time.Time                 `json:"last_checkpoint_at,omitempty"`
	Locked                                 bool                      `json:"locked"`
	ActiveRunID                            string                    `json:"active_run_id,omitempty"`
	LockAgeSeconds                         int64                     `json:"lock_age_seconds,omitempty"`
}

func LoadReviewerLearningReport(directory string) (ReviewerLearningReport, error) {
	path := filepath.Join(directory, "reviewer-learning.json")
	content, err := os.ReadFile(path)
	if err != nil {
		return ReviewerLearningReport{}, err
	}
	if trimmed := bytes.TrimSpace(content); len(trimmed) == 0 || trimmed[0] != '{' {
		return ReviewerLearningReport{}, fmt.Errorf("reviewer learning canonical report must be a nonempty JSON object")
	}
	var report ReviewerLearningReport
	if err := json.Unmarshal(content, &report); err != nil {
		return ReviewerLearningReport{}, err
	}
	if !reviewerLearningReportHasContent(report) {
		return ReviewerLearningReport{}, fmt.Errorf("reviewer learning canonical report contains no report evidence")
	}
	return report, nil
}

func RepairReviewerLearningReport(directory string) (ReviewerLearningReport, error) {
	lock, err := acquireReviewerLearningLock(directory, reviewerLearningRunID(), 0, false)
	if err != nil {
		return ReviewerLearningReport{}, err
	}
	defer releaseReviewerLearningLock(directory, lock)
	report, err := LoadReviewerLearningReport(directory)
	if err != nil {
		return ReviewerLearningReport{}, err
	}
	report.trackRuleIdentities()
	report.Pages = completedReviewerLearningPrefix(report.Pages)
	report.Rules = completedReviewerLearningRules(report.Rules, report.Pages)
	report.ImplementationVersion = ReviewerLearningImplementationVersion
	repairLegacyReviewerLearningSourceRules(report.Rules)
	normalizeReviewerLearningRuleEffects(report.Rules)
	report.mergeSourceRules()
	repairLegacyReviewerLearningAuditedPages(report.Pages)
	recomputeReviewerLearningPageMetrics(report.Pages, report.QualityThreshold, report.ReviewReadyThreshold)
	obsoleteTurns := clearObsoleteReviewerLearningPromptState(report.Pages)
	report.Rules = pruneObsoleteReviewerLearningRules(report.Rules, obsoleteTurns)
	if report.ExpectedURLs > 0 && len(report.Pages) < report.ExpectedURLs && report.RunStatus != ReviewerLearningRunStatusPausedChunk {
		report.RunStatus = ReviewerLearningRunStatusInterrupted
		report.CompletedAt = time.Time{}
	}
	report.updateReviewerLearningSummary()
	if err := WriteReviewerLearningReports(directory, report, lock); err != nil {
		return report, err
	}
	return report, nil
}

func repairLegacyReviewerLearningSourceRules(rules []ReviewerLearningRule) {
	for index := range rules {
		rule := &rules[index]
		if rule.Scope != RuleScopeSource {
			continue
		}
		if !sourceRuleContainsPageSpecificEvidence(rule.Instruction) {
			continue
		}
		rule.Instruction = generalizedSourceRuleInstruction(rule.Instruction)
	}
}

func normalizeReviewerLearningRuleEffects(rules []ReviewerLearningRule) {
	for index := range rules {
		rule := &rules[index]
		rule.AttemptedOnURLs = uniqueStrings(rule.AttemptedOnURLs)
		rule.HelpedURLs = uniqueStrings(rule.HelpedURLs)
		rule.HurtURLs = uniqueStrings(rule.HurtURLs)
		rule.NeutralURLs = uniqueStrings(rule.NeutralURLs)
		for _, url := range rule.HelpedURLs {
			rule.HurtURLs = removeString(rule.HurtURLs, url)
			rule.NeutralURLs = removeString(rule.NeutralURLs, url)
		}
		for _, url := range rule.HurtURLs {
			rule.NeutralURLs = removeString(rule.NeutralURLs, url)
		}
		if rule.Scope == RuleScopeSource {
			rule.ReuseCount = len(rule.HelpedURLs)
		}
	}
}

func mergeDuplicateReviewerLearningSourceRules(rules []ReviewerLearningRule) []ReviewerLearningRule {
	result := make([]ReviewerLearningRule, 0, len(rules))
	sourceIndexByInstruction := map[string]int{}
	for _, rule := range rules {
		if rule.Scope != RuleScopeSource {
			result = append(result, rule)
			continue
		}
		if existingIndex, ok := sourceIndexByInstruction[rule.Instruction]; ok {
			existing := &result[existingIndex]
			existing.AttemptedOnURLs = mergeUniqueStrings(existing.AttemptedOnURLs, rule.AttemptedOnURLs)
			existing.HelpedURLs = mergeUniqueStrings(existing.HelpedURLs, rule.HelpedURLs)
			existing.HurtURLs = mergeUniqueStrings(existing.HurtURLs, rule.HurtURLs)
			existing.NeutralURLs = mergeUniqueStrings(existing.NeutralURLs, rule.NeutralURLs)
			existing.PromotedFromURLRules = appendUniqueInt(existing.PromotedFromURLRules, rule.PromotedFromURLRules...)
			if existing.CreatedOnPage == 0 || rule.CreatedOnPage > 0 && rule.CreatedOnPage < existing.CreatedOnPage {
				existing.CreatedOnPage = rule.CreatedOnPage
				existing.CreatedOnTurn = rule.CreatedOnTurn
			}
			continue
		}
		sourceIndexByInstruction[rule.Instruction] = len(result)
		result = append(result, rule)
	}
	normalizeReviewerLearningRuleEffects(result)
	return result
}

func mergeUniqueStrings(values []string, additions []string) []string {
	for _, value := range additions {
		values = appendUniqueString(values, value)
	}
	return values
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = appendUniqueString(result, value)
	}
	return result
}

func sourceRuleContainsPageSpecificEvidence(instruction string) bool {
	lower := strings.ToLower(instruction)
	return strings.Contains(lower, "include source blocks containing") ||
		strings.Contains(lower, "exact block_id") ||
		strings.Contains(lower, "exact block_ids") ||
		strings.Contains(lower, "block_id values") ||
		strings.Contains(lower, "extra text pattern:") ||
		strings.Contains(lower, "for https://") ||
		strings.Contains(lower, "for http://")
}

func generalizedSourceRuleInstruction(instruction string) string {
	switch reviewerLearningPromptFamily(instruction) {
	case "critical_type_mismatch":
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "critical_type_mismatch", Scope: RuleScopeSource})
	case "missing_warning_info_box":
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_warning_info_box", Scope: RuleScopeSource})
	case "missing_table":
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_table", Scope: RuleScopeSource})
	case "missing_media":
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_media", Scope: RuleScopeSource})
	case "missing_metadata":
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_metadata", Scope: RuleScopeSource})
	case "missing_breadcrumb":
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_breadcrumb", Scope: RuleScopeSource})
	case "extra_boilerplate":
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "extra_boilerplate", Scope: RuleScopeSource})
	default:
		return promptForEvidence(reviewerLearningPromptEvidence{Category: "repeated_source_pattern", Scope: RuleScopeSource})
	}
}

func repairLegacyReviewerLearningAuditedPages(pages []ReviewerLearningPage) {
	for index := range pages {
		page := &pages[index]
		if len(page.Expected) == 0 || page.FinalApproved.Metrics.ExactParity {
			continue
		}
		switch page.FinalApprovalMethod {
		case ReviewerLearningApprovalAuditedHumanEdit, ReviewerLearningApprovalAuditedBlockSelect, ReviewerLearningApprovalStalledHumanEdit:
			page.FinalApproved = auditedHumanEditLayer(page.Expected)
			page.FinalApprovalMethod = ReviewerLearningApprovalStalledHumanEdit
			page.ManualEditRequired = true
			page.Stalled = true
			const repairReason = "legacy audited final layer was not exact; repaired to explicit audited human edit"
			if page.StallReason == "" {
				page.StallReason = repairReason
			} else if !strings.Contains(page.StallReason, repairReason) {
				page.StallReason = strings.TrimSpace(page.StallReason) + "; " + repairReason
			}
		}
	}
}

func recomputeReviewerLearningPageMetrics(pages []ReviewerLearningPage, qualityThreshold, reviewReadyThreshold float64) {
	if qualityThreshold == 0 {
		qualityThreshold = 0.98
	}
	if reviewReadyThreshold == 0 {
		reviewReadyThreshold = 0.97
	}
	for index := range pages {
		page := &pages[index]
		if len(page.Expected) == 0 {
			continue
		}
		recomputeReviewerLearningLayerMetrics(&page.FirstPass, page.Expected)
		recomputeReviewerLearningLayerMetrics(&page.WithExistingRules, page.Expected)
		recomputeReviewerLearningLayerMetrics(&page.AfterNewReviewerPrompt, page.Expected)
		recomputeReviewerLearningLayerMetrics(&page.FinalApproved, page.Expected)
		if page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict && len(page.AuditedEditOperations) > 0 {
			if reviewerLearningAuditedOperationsSourceEvidenced(page.AuditedEditOperations) {
				page.FinalApproved = auditedHumanEditLayer(page.Expected)
				page.FinalApprovalMethod = ReviewerLearningApprovalAuditedBlockSelect
				page.StallReason = fmt.Sprintf("model did not reach strict %.2f F1 after exact reviewer block-id prompts; benchmark applied exact audited reviewer edits from source evidence", qualityThreshold)
			} else {
				page.FinalApproved = auditedHumanEditLayer(page.Expected)
				page.FinalApprovalMethod = ReviewerLearningApprovalStalledHumanEdit
				page.StallReason = fmt.Sprintf("model did not reach strict %.2f F1 after exact reviewer block-id prompts; final parity required audited human edit", qualityThreshold)
			}
			page.Stalled = true
		}
		if reviewerLearningAuditedMethod(page.FinalApprovalMethod) {
			if layer, method, ok := bestRepairModelLayer(*page, qualityThreshold, reviewReadyThreshold); ok {
				page.FinalApproved = layer
				page.FinalApprovalMethod = method
				page.Stalled = false
				page.StallReason = ""
			}
		}
		if len(page.RawExpected) > 0 && len(page.FinalApproved.Actual) > 0 {
			page.RawSelectorMetrics = EvaluateBenchmark(page.RawExpected, page.FinalApproved.Actual)
		}
		page.SemanticGoldMetrics = page.FinalApproved.Metrics
		if len(page.ReviewerTurnDetails) > 0 {
			page.ReviewerTurnDetails[0].BeforeMetrics = page.WithExistingRules.Metrics
			page.ReviewerTurnDetails[len(page.ReviewerTurnDetails)-1].AfterMetrics = page.AfterNewReviewerPrompt.Metrics
		}
		page.StrictPass = reviewerLearningPasses(page.FinalApproved.Metrics, qualityThreshold)
		page.ReviewReady = reviewerLearningModelReviewReady(*page, reviewReadyThreshold)
		if page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict && page.StrictPass {
			if page.ReviewerTurns > 0 {
				page.FinalApprovalMethod = ReviewerLearningApprovalNewReviewerPrompt
			} else {
				page.FinalApprovalMethod = ReviewerLearningApprovalExistingRules
			}
		}
		page.ManualEditRequired = page.FinalApprovalMethod == ReviewerLearningApprovalAuditedHumanEdit ||
			page.FinalApprovalMethod == ReviewerLearningApprovalAuditedBlockSelect ||
			page.FinalApprovalMethod == ReviewerLearningApprovalStalledHumanEdit ||
			page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict
		page.SoftReviewCandidate = reviewerLearningSoftReviewCandidate(reviewerLearningBestModelFacingMetrics(*page))
	}
}

func clearObsoleteReviewerLearningPromptState(pages []ReviewerLearningPage) map[int]map[int]bool {
	obsoleteTurns := map[int]map[int]bool{}
	for index := range pages {
		page := &pages[index]
		if page.ReviewerTurns == 0 {
			continue
		}
		switch page.FinalApprovalMethod {
		case ReviewerLearningApprovalFirstPass, ReviewerLearningApprovalExistingRules:
			turns := map[int]bool{}
			for turn := 1; turn <= page.ReviewerTurns; turn++ {
				turns[turn] = true
			}
			obsoleteTurns[page.Index] = turns
			page.ReviewerTurns = 0
			page.ReviewerTurnDetails = nil
			page.ReviewerPrompt = ""
			page.ReviewerRuleScope = ""
			page.ReviewerRuleID = 0
			page.AfterNewReviewerPrompt = ReviewerLearningLayer{}
			page.Stalled = false
			page.StallReason = ""
			page.ManualEditRequired = false
			page.TurnsUntilStrictPass = 0
			page.TurnsUntilReviewReady = 0
			if page.FinalApprovalMethod == ReviewerLearningApprovalFirstPass {
				page.FixedByExistingRules = false
				page.AutonomousAfterLearning = false
				page.ActiveRuleIDs = nil
			}
		}
	}
	return obsoleteTurns
}

func pruneObsoleteReviewerLearningRules(rules []ReviewerLearningRule, obsoleteTurns map[int]map[int]bool) []ReviewerLearningRule {
	if len(obsoleteTurns) == 0 {
		return rules
	}
	kept := make([]ReviewerLearningRule, 0, len(rules))
	for _, rule := range rules {
		if !obsoleteTurns[rule.CreatedOnPage][rule.CreatedOnTurn] {
			kept = append(kept, rule)
			continue
		}
		if rule.Scope == RuleScopeSource && reviewerLearningRuleHasObservedEffect(rule) {
			kept = append(kept, rule)
		}
	}
	return kept
}

func reviewerLearningRuleHasObservedEffect(rule ReviewerLearningRule) bool {
	return len(rule.AttemptedOnURLs) > 0 ||
		len(rule.HelpedURLs) > 0 ||
		len(rule.HurtURLs) > 0 ||
		len(rule.NeutralURLs) > 0
}

func reviewerLearningAuditedMethod(method string) bool {
	return method == ReviewerLearningApprovalAuditedHumanEdit ||
		method == ReviewerLearningApprovalAuditedBlockSelect ||
		method == ReviewerLearningApprovalStalledHumanEdit
}

func reviewerLearningModelReviewReady(page ReviewerLearningPage, threshold float64) bool {
	if reviewerLearningPasses(page.FirstPass.Metrics, threshold) ||
		reviewerLearningPasses(page.WithExistingRules.Metrics, threshold) {
		return true
	}
	if page.ReviewerTurns > 0 && reviewerLearningPasses(page.AfterNewReviewerPrompt.Metrics, threshold) {
		return true
	}
	return reviewerLearningModelSuccessMethod(page.FinalApprovalMethod) &&
		reviewerLearningPasses(page.FinalApproved.Metrics, threshold)
}

func bestRepairModelLayer(page ReviewerLearningPage, qualityThreshold, reviewReadyThreshold float64) (ReviewerLearningLayer, string, bool) {
	candidates := []struct {
		layer  ReviewerLearningLayer
		method string
	}{
		{layer: page.FirstPass, method: ReviewerLearningApprovalFirstPass},
		{layer: page.WithExistingRules, method: ReviewerLearningApprovalExistingRules},
	}
	if page.ReviewerTurns > 0 {
		candidates = append(candidates, struct {
			layer  ReviewerLearningLayer
			method string
		}{layer: page.AfterNewReviewerPrompt, method: ReviewerLearningApprovalNewReviewerPrompt})
	}
	var best ReviewerLearningLayer
	bestMethod := ""
	for _, candidate := range candidates {
		if len(candidate.layer.Actual) == 0 {
			continue
		}
		if candidate.layer.Metrics.RelevantTextF1 > best.Metrics.RelevantTextF1 {
			best = candidate.layer
			bestMethod = candidate.method
		}
	}
	if bestMethod == "" {
		return ReviewerLearningLayer{}, "", false
	}
	if reviewerLearningPasses(best.Metrics, qualityThreshold) {
		return best, bestMethod, true
	}
	return ReviewerLearningLayer{}, "", false
}

func reviewerLearningAuditedOperationsSourceEvidenced(operations []ReviewerLearningAuditedEditOperation) bool {
	if len(operations) == 0 {
		return false
	}
	for _, operation := range operations {
		if operation.Operation == "copy_from_human_gold" {
			return false
		}
	}
	return true
}

func recomputeReviewerLearningLayerMetrics(layer *ReviewerLearningLayer, expected []BenchmarkSection) {
	if len(layer.Actual) == 0 {
		return
	}
	layer.Metrics = EvaluateBenchmark(expected, layer.Actual)
	layer.MismatchLabels = classifyMismatches(layer.Metrics)
}

func LoadReviewerLearningStatus(directory string) (ReviewerLearningStatus, error) {
	writerActive, err := reviewerLearningWriterActive(directory)
	if err != nil {
		return ReviewerLearningStatus{}, err
	}
	report, err := LoadReviewerLearningReport(directory)
	if err != nil {
		if os.IsNotExist(err) {
			status := ReviewerLearningStatus{RunStatus: ReviewerLearningRunStatusNotStarted, ReportPath: filepath.Join(directory, "reviewer-learning.json")}
			if writerActive {
				status.RunStatus = ReviewerLearningRunStatusRunning
				status.Locked = true
				if lock, lockErr := readReviewerLearningLock(directory); lockErr == nil {
					status.ActiveRunID = lock.RunID
					status.LockAgeSeconds = int64(time.Since(lock.UpdatedAt).Seconds())
				}
			}
			return status, nil
		}
		return ReviewerLearningStatus{}, err
	}
	runStatus := report.RunStatus
	if runStatus == "" {
		runStatus = ReviewerLearningRunStatusRunning
	}
	report.RunStatus = runStatus
	report.updateReviewerLearningSummary()
	lastCompletedPage := report.LastCompletedPage
	hasIncompletePages := reviewerLearningHasIncompletePages(report.Pages)
	if lastCompletedPage == 0 && runStatus != ReviewerLearningRunStatusRunning && !hasIncompletePages {
		lastCompletedPage = len(report.Pages)
	}
	if hasIncompletePages && runStatus != ReviewerLearningRunStatusRunning {
		runStatus = ReviewerLearningRunStatusInterrupted
		report.RunStatus = runStatus
		report.ProofUsable = false
		report.ProofBlockedReason = "run is not complete"
	}
	status := ReviewerLearningStatus{
		RunStatus:                              runStatus,
		ImplementationVersion:                  report.ImplementationVersion,
		CurrentImplementationVersion:           ReviewerLearningImplementationVersion,
		PagesCompleted:                         lastCompletedPage,
		ExpectedURLs:                           report.ExpectedURLs,
		FetchFailedPages:                       report.FetchFailedPages,
		ProofUsable:                            report.ProofUsable,
		ProofBlockedReason:                     report.ProofBlockedReason,
		LastCompletedPage:                      lastCompletedPage,
		CurrentStableStreak:                    report.CurrentStableStreak,
		StableStreakStartPage:                  report.StableStreakStartPage,
		MaxStableStreak:                        report.MaxStableStreak,
		MaxStableStreakStartPage:               report.MaxStableStreakStartPage,
		MaxStableStreakEndPage:                 report.MaxStableStreakEndPage,
		TotalReviewerPrompts:                   report.TotalReviewerPrompts,
		PagesFixedByExistingRules:              report.PagesFixedByExistingRules,
		PagesEverFixedByExistingRules:          report.PagesEverFixedByExistingRules,
		PagesCurrentlyPassingWithExistingRules: report.PagesCurrentlyPassingWithExistingRules,
		PagesWhereExistingRulesHurtScore:       report.PagesWhereExistingRulesHurtScore,
		SourceRulesHelpedCount:                 report.SourceRulesHelpedCount,
		SourceRulesHurtCount:                   report.SourceRulesHurtCount,
		SourceRulesNeutralCount:                report.SourceRulesNeutralCount,
		StrictModelPassPages:                   report.StrictModelPassPages,
		ReviewReady97Pages:                     report.ReviewReady97Pages,
		SoftReviewCandidatePages:               report.SoftReviewCandidatePages,
		PagesNeedingAuditedEdit:                report.PagesNeedingAuditedEdit,
		PagesRequiringManualEditAfterStall:     report.PagesRequiringManualEditAfterStall,
		PagesRequiringHumanConfirmation:        report.PagesRequiringHumanConfirmation,
		ModelInputTokens:                       report.ModelInputTokens,
		ModelOutputTokens:                      report.ModelOutputTokens,
		ReportPath:                             filepath.Join(directory, "reviewer-learning.json"),
		LearningMode:                           report.LearningMode,
		RunID:                                  report.RunID,
		LastCheckpointAt:                       report.LastCheckpointAt,
	}
	if writerActive {
		status.Locked = true
		status.RunStatus = ReviewerLearningRunStatusRunning
		if lock, lockErr := readReviewerLearningLock(directory); lockErr == nil {
			status.ActiveRunID = lock.RunID
			status.LockAgeSeconds = int64(time.Since(lock.UpdatedAt).Seconds())
		}
	} else if status.RunStatus == ReviewerLearningRunStatusRunning {
		status.RunStatus = ReviewerLearningRunStatusInterrupted
	}
	if status.RunStatus == ReviewerLearningRunStatusRunning ||
		status.RunStatus == ReviewerLearningRunStatusInterrupted ||
		status.RunStatus == ReviewerLearningRunStatusPausedChunk ||
		status.RunStatus == ReviewerLearningRunStatusNotStarted {
		status.ProofUsable = false
		if status.ProofBlockedReason == "" {
			status.ProofBlockedReason = "run is not complete"
		}
	}
	return status, nil
}

func runReviewerLearningLayer(ctx context.Context, model SemanticModel, config CrawlConfig, page PageVersion, blocks []CandidateBlock, gold []BenchmarkSection, rules []SkillRule) ReviewerLearningLayer {
	response, usage, err := extractWithValidationRetry(ctx, model, buildExtractionPrompt(config, page, blocks, rules), blocks)
	layer := ReviewerLearningLayer{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, LatencyMS: usage.Latency.Milliseconds()}
	if err != nil {
		layer.ModelError = err.Error()
		layer.Metrics = EvaluateBenchmark(gold, nil)
		layer.MismatchLabels = append(classifyMismatches(layer.Metrics), "schema_or_extraction_error")
		return layer
	}
	layer.Actual = benchmarkSections(sectionsFromDecisions(blocks, response.Blocks))
	layer.Metrics = EvaluateBenchmark(gold, layer.Actual)
	layer.MismatchLabels = classifyMismatches(layer.Metrics)
	return layer
}

func reviewerLearningPasses(metrics BenchmarkMetrics, threshold float64) bool {
	return metrics.ExactParity || metrics.RelevantTextF1 >= threshold && reviewerLearningClinicalCoveragePasses(metrics)
}

func reviewerLearningClinicalCoveragePasses(metrics BenchmarkMetrics) bool {
	return metrics.CriticalRecall >= 1 &&
		metrics.TableRecall >= 1 &&
		metrics.MediaRecall >= 1
}

func reviewerLearningSoftReviewCandidate(metrics BenchmarkMetrics) bool {
	return metrics.RelevantTextF1 >= 0.95 &&
		reviewerLearningClinicalCoveragePasses(metrics) &&
		metrics.BoilerplateRate <= 0.05
}

func applicableLearningRules(rules []ReviewerLearningRule, url string, includeSource bool) []SkillRule {
	var result []SkillRule
	seenSourceInstructions := map[string]bool{}
	for _, rule := range rules {
		if rule.Status != "active" {
			continue
		}
		if rule.Scope == RuleScopeSource && sourceRuleInstructionIsHarmful(rules, rule) {
			continue
		}
		if rule.Scope == RuleScopeSource && includeSource {
			if seenSourceInstructions[rule.Instruction] {
				continue
			}
			seenSourceInstructions[rule.Instruction] = true
			result = append(result, SkillRule{ID: int64(rule.ID), Scope: RuleScopeSource, Instruction: rule.Instruction, Version: rule.Version, Status: rule.Status})
		}
		if rule.Scope == RuleScopeURL && rule.OriginURL == url {
			result = append(result, SkillRule{ID: int64(rule.ID), Scope: RuleScopeURL, OriginURL: rule.OriginURL, Instruction: rule.Instruction, Version: rule.Version, Status: rule.Status})
		}
	}
	return result
}

func sourceRuleInstructionIsHarmful(rules []ReviewerLearningRule, rule ReviewerLearningRule) bool {
	if rule.Scope != RuleScopeSource {
		return false
	}
	helped := 0
	hurt := 0
	for _, candidate := range rules {
		if candidate.Scope != RuleScopeSource || candidate.Instruction != rule.Instruction {
			continue
		}
		helped += len(candidate.HelpedURLs)
		hurt += len(candidate.HurtURLs)
	}
	return hurt > helped && hurt > 0
}

func usedReviewerLearningPrompts(rules []ReviewerLearningRule, url string) map[string]bool {
	used := map[string]bool{}
	for _, rule := range rules {
		if rule.Status != "active" {
			continue
		}
		if rule.Scope == RuleScopeSource || rule.Scope == RuleScopeURL && rule.OriginURL == url {
			used[rule.Instruction] = true
		}
	}
	return used
}

func skillRuleIDs(rules []SkillRule) []int {
	ids := make([]int, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, int(rule.ID))
	}
	return ids
}

func originURLForScope(scope RuleScope, url string) string {
	if scope == RuleScopeURL {
		return url
	}
	return ""
}

type reviewerLearningPromptCandidate struct {
	prompt string
	scope  RuleScope
}

func generateReviewerLearningPromptAvoiding(url string, gold, actual []BenchmarkSection, metrics BenchmarkMetrics, used map[string]bool) (string, RuleScope) {
	var candidates []reviewerLearningPromptCandidate
	if hasGoldCritical(gold) && metrics.CriticalRecall < 1 {
		candidates = append(candidates, reviewerLearningPromptCandidate{promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_warning_info_box", Scope: RuleScopeSource}), RuleScopeSource})
	}
	if hasGoldRole(gold, RoleTable) && metrics.TableRecall < 1 {
		candidates = append(candidates, reviewerLearningPromptCandidate{promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_table", Scope: RuleScopeSource}), RuleScopeSource})
	}
	if hasGoldMedia(gold) && metrics.MediaRecall < 1 {
		candidates = append(candidates, reviewerLearningPromptCandidate{promptForEvidence(reviewerLearningPromptEvidence{Category: "missing_media", Scope: RuleScopeSource}), RuleScopeSource})
	}
	if containsGoldText(gold, "du är här") || containsGoldText(gold, "sample") && containsGoldText(gold, "start") {
		candidates = append(candidates, reviewerLearningPromptCandidate{"For this source: keep breadcrumb/topic path blocks when they are part of the human baseline, but do not include global navigation menus.", RuleScopeSource})
	}
	if containsGoldAny(gold, []string{"godkänd", "författare", "granskare", "faktagranskare", "illustratör", "fotograf"}) {
		candidates = append(candidates, reviewerLearningPromptCandidate{"For this source: keep article metadata such as approved date, author, reviewer, fact reviewer, illustrator, photographer, and source owner when visible in the article body/footer.", RuleScopeSource})
	}
	if containsActualAny(actual, []string{"läs mer i vårdhandboken", "tipsa & dela", "kopiera länk", "skriv ut sidan"}) || metrics.RelevantTextPrecision < 0.98 {
		candidates = append(candidates, reviewerLearningPromptCandidate{promptForEvidence(reviewerLearningPromptEvidence{Category: "extra_boilerplate", Scope: RuleScopeSource}), RuleScopeSource})
	}
	candidates = append(candidates, reviewerLearningPromptCandidate{fmt.Sprintf("For this page only: review %s against the human selector baseline and keep the missing page-specific source content without applying this instruction to other pages.", url), RuleScopeURL})
	for _, candidate := range candidates {
		if used == nil || !used[candidate.prompt] {
			return candidate.prompt, candidate.scope
		}
	}
	return fmt.Sprintf("For this page only: additional review turn for %s. Compare the remaining mismatch to the human selector baseline and keep only the still-missing page-specific source content.", url), RuleScopeURL
}

func hasGoldCritical(gold []BenchmarkSection) bool {
	for _, section := range gold {
		if section.Type == TypeCritical {
			return true
		}
	}
	return false
}

func hasGoldRole(gold []BenchmarkSection, role BlockRole) bool {
	for _, section := range gold {
		if section.Role == role {
			return true
		}
	}
	return false
}

func hasGoldMedia(gold []BenchmarkSection) bool {
	for _, section := range gold {
		if section.Role == RoleMedia || section.Role == RoleMediaGroup || section.Role == RoleVideo {
			return true
		}
	}
	return false
}

func containsGoldText(gold []BenchmarkSection, needle string) bool {
	return containsGoldAny(gold, []string{needle})
}

func containsGoldAny(gold []BenchmarkSection, needles []string) bool {
	for _, section := range gold {
		text := strings.ToLower(section.Text)
		for _, needle := range needles {
			if strings.Contains(text, strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

func containsActualAny(actual []BenchmarkSection, needles []string) bool {
	for _, section := range actual {
		text := strings.ToLower(section.Text)
		for _, needle := range needles {
			if strings.Contains(text, strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

func (report *ReviewerLearningReport) addLayerUsage(layer ReviewerLearningLayer) {
	report.ModelInputTokens += layer.InputTokens
	report.ModelOutputTokens += layer.OutputTokens
}

func (report *ReviewerLearningReport) countReviewerLearningBaselineStatus(status BaselineStatus) {
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
		report.FetchFailedPages++
	}
}

func (report *ReviewerLearningReport) recordRuleReuse(rules []SkillRule, url string) {
	for _, active := range rules {
		if active.Scope != RuleScopeSource {
			continue
		}
		for index := range report.Rules {
			if report.Rules[index].ID != int(active.ID) {
				continue
			}
			markReviewerLearningRuleHelped(&report.Rules[index], url)
		}
	}
}

func (report *ReviewerLearningReport) updateReviewerLearningSummary() {
	var first, existing, newPrompt, final, rawSelectorFinal []PageBenchmark
	var turns []int
	normalizeReviewerLearningRuleEffects(report.Rules)
	report.mergeSourceRules()
	*report = reviewerLearningResetSummary(*report)
	reviewReadyThreshold := report.ReviewReadyThreshold
	if reviewReadyThreshold == 0 {
		reviewReadyThreshold = 0.97
	}
	report.UpdatedAt = time.Now().UTC()
	for index := range report.Pages {
		page := &report.Pages[index]
		pageComplete := reviewerLearningPageComplete(*page)
		if pageComplete && page.Index > report.LastCompletedPage {
			report.LastCompletedPage = page.Index
		}
		report.TotalReviewerTurns += page.ReviewerTurns
		if !pageComplete {
			report.TotalReviewerPrompts += page.ReviewerTurns
			report.CurrentStableStreak = 0
			report.StableStreakStartPage = 0
			continue
		}
		report.countReviewerLearningBaselineStatus(page.BaselineStatus)
		if page.BaselineStatus == BaselineFetchFailed {
			report.MissingURLs = append(report.MissingURLs, page.URL)
		} else {
			report.DiscoveredURLs++
		}
		if page.BaselineStatus == BaselineFetchFailed || page.BaselineStatus == BaselineZeroGold {
			report.CurrentStableStreak = 0
			report.StableStreakStartPage = 0
			continue
		}
		page.SemanticGoldMetrics = page.FinalApproved.Metrics
		if len(page.RawExpected) > 0 && len(page.FinalApproved.Actual) > 0 {
			page.RawSelectorMetrics = EvaluateBenchmark(page.RawExpected, page.FinalApproved.Actual)
			rawSelectorFinal = append(rawSelectorFinal, PageBenchmark{URL: page.URL, BaselineStatus: page.BaselineStatus, Expected: page.RawExpected, Actual: page.FinalApproved.Actual, Metrics: page.RawSelectorMetrics})
		}
		page.SoftReviewCandidate = reviewerLearningSoftReviewCandidate(reviewerLearningBestModelFacingMetrics(*page))
		page.ManualEditRequired = page.FinalApprovalMethod == ReviewerLearningApprovalAuditedHumanEdit ||
			page.FinalApprovalMethod == ReviewerLearningApprovalAuditedBlockSelect ||
			page.FinalApprovalMethod == ReviewerLearningApprovalStalledHumanEdit ||
			page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict
		first = append(first, PageBenchmark{URL: page.URL, BaselineStatus: page.BaselineStatus, Expected: page.Expected, Actual: page.FirstPass.Actual, Metrics: page.FirstPass.Metrics})
		existing = append(existing, PageBenchmark{URL: page.URL, BaselineStatus: page.BaselineStatus, Expected: page.Expected, Actual: page.WithExistingRules.Actual, Metrics: page.WithExistingRules.Metrics})
		if page.ReviewerTurns > 0 {
			newPrompt = append(newPrompt, PageBenchmark{URL: page.URL, BaselineStatus: page.BaselineStatus, Expected: page.Expected, Actual: page.AfterNewReviewerPrompt.Actual, Metrics: page.AfterNewReviewerPrompt.Metrics})
		}
		final = append(final, PageBenchmark{URL: page.URL, BaselineStatus: page.BaselineStatus, Expected: page.Expected, Actual: page.FinalApproved.Actual, Metrics: page.FinalApproved.Metrics})
		turns = append(turns, page.ReviewerTurns)
		if page.ReviewerTurns == 0 {
			report.PagesRequiringZeroReviewerPrompts++
		} else {
			report.TotalReviewerPrompts += page.ReviewerTurns
			report.PagesRequiringNewPrompt++
		}
		if page.FixedByExistingRules {
			report.PagesFixedByExistingRules++
			report.PagesEverFixedByExistingRules++
			report.PagesCurrentlyPassingWithExistingRules++
		}
		if page.AutonomousAfterLearning {
			report.AutonomousAfterLearningPages++
		}
		if page.FinalApprovalMethod == ReviewerLearningApprovalAuditedHumanEdit || page.FinalApprovalMethod == ReviewerLearningApprovalAuditedBlockSelect {
			report.PagesNeedingAuditedEdit++
		}
		if page.FinalApprovalMethod == ReviewerLearningApprovalStalledHumanEdit {
			report.PagesNeedingAuditedEdit++
			report.PagesRequiringManualEditAfterStall++
		}
		if page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict {
			report.PagesRequiringHumanConfirmation++
		}
		if page.FirstPass.Metrics.RelevantTextF1 > page.WithExistingRules.Metrics.RelevantTextF1 {
			report.PagesWhereExistingRulesHurtScore++
		}
		if reviewerLearningModelSuccessMethod(page.FinalApprovalMethod) &&
			(page.StrictPass || reviewerLearningPasses(page.FinalApproved.Metrics, report.QualityThreshold)) {
			report.StrictModelPassPages++
		}
		if reviewerLearningModelReviewReady(*page, reviewReadyThreshold) {
			report.ReviewReady97Pages++
		}
		if page.SoftReviewCandidate {
			report.SoftReviewCandidatePages++
		}
		if reviewerLearningStablePage(*page, report.QualityThreshold) {
			if report.CurrentStableStreak == 0 {
				report.StableStreakStartPage = page.Index
			}
			report.CurrentStableStreak++
			if report.CurrentStableStreak > report.MaxStableStreak {
				report.MaxStableStreak = report.CurrentStableStreak
				report.MaxStableStreakStartPage = report.StableStreakStartPage
				report.MaxStableStreakEndPage = page.Index
			}
		} else {
			report.CurrentStableStreak = 0
			report.StableStreakStartPage = 0
		}
	}
	for _, rule := range report.Rules {
		if rule.Scope == RuleScopeSource {
			report.SourceScopedRulesCreated++
			report.SourceRulesHelpedCount += len(rule.HelpedURLs)
			report.SourceRulesHurtCount += len(rule.HurtURLs)
			report.SourceRulesNeutralCount += len(rule.NeutralURLs)
		}
		if rule.Scope == RuleScopeURL {
			report.URLScopedRulesCreated++
		}
	}
	report.FirstPassAggregate = aggregateMetrics(first)
	report.WithExistingRulesAggregate = aggregateMetrics(existing)
	report.AfterNewPromptAggregate = aggregateMetrics(newPrompt)
	report.FinalApprovedAggregate = aggregateMetrics(final)
	report.RawSelectorMetrics = aggregateMetrics(rawSelectorFinal)
	report.SemanticGoldMetrics = report.FinalApprovedAggregate
	report.ModelFirstPassMetrics = report.FirstPassAggregate
	report.ModelAfterRulesMetrics = report.WithExistingRulesAggregate
	report.ModelAfterReviewerPromptMetrics = report.AfterNewPromptAggregate
	report.FinalAuditedMetrics = report.FinalApprovedAggregate
	if len(turns) > 0 {
		report.AverageReviewerTurnsPerPage = float64(report.TotalReviewerTurns) / float64(len(turns))
		report.MedianReviewerTurnsPerPage = medianInts(turns)
	}
	if report.RunStatus == ReviewerLearningRunStatusRunning ||
		report.RunStatus == ReviewerLearningRunStatusInterrupted ||
		report.RunStatus == ReviewerLearningRunStatusPausedChunk ||
		report.RunStatus == ReviewerLearningRunStatusNotStarted {
		report.ProofUsable = false
		report.ProofBlockedReason = "run is not complete"
	} else if report.FetchFailedPages > 0 {
		report.ProofUsable = false
		report.ProofBlockedReason = fmt.Sprintf("%d fetch failed page(s) prevent full proof coverage", report.FetchFailedPages)
	} else if report.ExpectedURLs <= 0 || report.UsableGoldPages+report.PartialGoldPages+report.RepairedGoldPages == 0 {
		report.ProofUsable = false
		report.ProofBlockedReason = "no usable selector gold pages were available"
	} else {
		report.ProofUsable = true
		report.ProofBlockedReason = ""
	}
	report.updateReviewerLearningUsageSummary()
}

func reviewerLearningModelSuccessMethod(method string) bool {
	return method == ReviewerLearningApprovalFirstPass ||
		method == ReviewerLearningApprovalExistingRules ||
		method == ReviewerLearningApprovalNewReviewerPrompt ||
		method == ReviewerLearningApprovalOraclePrompt
}

func reviewerLearningPageComplete(page ReviewerLearningPage) bool {
	if page.BaselineStatus == BaselineFetchFailed {
		return page.FetchError != ""
	}
	if page.BaselineStatus == BaselineZeroGold {
		return true
	}
	return page.FinalApprovalMethod != ""
}

func reviewerLearningBestModelFacingMetrics(page ReviewerLearningPage) BenchmarkMetrics {
	best := page.FirstPass.Metrics
	if page.WithExistingRules.Metrics.RelevantTextF1 > best.RelevantTextF1 {
		best = page.WithExistingRules.Metrics
	}
	if page.ReviewerTurns > 0 && page.AfterNewReviewerPrompt.Metrics.RelevantTextF1 > best.RelevantTextF1 {
		best = page.AfterNewReviewerPrompt.Metrics
	}
	if reviewerLearningModelSuccessMethod(page.FinalApprovalMethod) ||
		page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict {
		if page.FinalApproved.Metrics.RelevantTextF1 > best.RelevantTextF1 {
			best = page.FinalApproved.Metrics
		}
	}
	return best
}

func reviewerLearningHasIncompletePages(pages []ReviewerLearningPage) bool {
	for _, page := range pages {
		if !reviewerLearningPageComplete(page) {
			return true
		}
	}
	return false
}

func completedReviewerLearningPrefix(pages []ReviewerLearningPage) []ReviewerLearningPage {
	for index, page := range pages {
		if !reviewerLearningPageComplete(page) {
			return append([]ReviewerLearningPage(nil), pages[:index]...)
		}
	}
	return pages
}

func completedReviewerLearningRules(rules []ReviewerLearningRule, pages []ReviewerLearningPage) []ReviewerLearningRule {
	lastPage := 0
	for _, page := range pages {
		if page.Index > lastPage {
			lastPage = page.Index
		}
	}
	kept := make([]ReviewerLearningRule, 0, len(rules))
	for _, rule := range rules {
		if rule.CreatedOnPage == 0 || rule.CreatedOnPage <= lastPage {
			kept = append(kept, rule)
		}
	}
	return kept
}

func reviewerLearningResetSummary(report ReviewerLearningReport) ReviewerLearningReport {
	report.FirstPassAggregate = BenchmarkMetrics{}
	report.WithExistingRulesAggregate = BenchmarkMetrics{}
	report.AfterNewPromptAggregate = BenchmarkMetrics{}
	report.FinalApprovedAggregate = BenchmarkMetrics{}
	report.RawSelectorMetrics = BenchmarkMetrics{}
	report.SemanticGoldMetrics = BenchmarkMetrics{}
	report.ModelFirstPassMetrics = BenchmarkMetrics{}
	report.ModelAfterRulesMetrics = BenchmarkMetrics{}
	report.ModelAfterReviewerPromptMetrics = BenchmarkMetrics{}
	report.FinalAuditedMetrics = BenchmarkMetrics{}
	report.DiscoveredURLs = 0
	report.MissingURLs = nil
	report.UsableGoldPages = 0
	report.PartialGoldPages = 0
	report.StaleSelectorPages = 0
	report.RepairedGoldPages = 0
	report.FetchFailedPages = 0
	report.ProofUsable = false
	report.ProofBlockedReason = ""
	report.LastCompletedPage = 0
	report.TotalReviewerPrompts = 0
	report.TotalReviewerTurns = 0
	report.AverageReviewerTurnsPerPage = 0
	report.MedianReviewerTurnsPerPage = 0
	report.PagesRequiringZeroReviewerPrompts = 0
	report.PagesFixedByExistingRules = 0
	report.PagesEverFixedByExistingRules = 0
	report.PagesCurrentlyPassingWithExistingRules = 0
	report.PagesWhereExistingRulesHurtScore = 0
	report.PagesRequiringNewPrompt = 0
	report.PagesNeedingAuditedEdit = 0
	report.PagesRequiringManualEditAfterStall = 0
	report.PagesRequiringHumanConfirmation = 0
	report.AutonomousAfterLearningPages = 0
	report.StrictModelPassPages = 0
	report.ReviewReady97Pages = 0
	report.SoftReviewCandidatePages = 0
	report.SourceRulesHelpedCount = 0
	report.SourceRulesHurtCount = 0
	report.SourceRulesNeutralCount = 0
	report.CurrentStableStreak = 0
	report.StableStreakStartPage = 0
	report.MaxStableStreak = 0
	report.MaxStableStreakStartPage = 0
	report.MaxStableStreakEndPage = 0
	report.SourceScopedRulesCreated = 0
	report.URLScopedRulesCreated = 0
	return report
}

func reviewerLearningStablePage(page ReviewerLearningPage, threshold float64) bool {
	if page.ReviewerTurns != 0 {
		return false
	}
	if page.FinalApprovalMethod == ReviewerLearningApprovalAuditedHumanEdit || page.FinalApprovalMethod == "" {
		return false
	}
	return reviewerLearningPasses(page.FinalApproved.Metrics, threshold)
}

func lastSourceRulePage(pages []ReviewerLearningPage) int {
	last := 0
	for _, page := range pages {
		if page.ReviewerRuleScope == RuleScopeSource {
			last = page.Index
		}
	}
	return last
}

func medianInts(values []int) float64 {
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[middle])
	}
	return float64(sorted[middle-1]+sorted[middle]) / 2
}

func (report *ReviewerLearningReport) updateReviewerLearningUsageSummary() {
	if report.ModelMode == "" || report.ModelMode == "fixture_regression" || report.ModelMode == "fixture" {
		report.TokenUsageAvailable = false
		report.RawUsageMissing = false
		report.UsageNote = "Fixture regression: no LLM call, no token usage, deterministic fixture model."
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

func reviewerLearningModelMode(mode string) string {
	if mode == "" || mode == "fixture" {
		return "fixture_regression"
	}
	return mode
}

func WriteReviewerLearningReports(directory string, report ReviewerLearningReport, heldLocks ...ReviewerLearningLock) error {
	var lock ReviewerLearningLock
	if len(heldLocks) > 0 {
		lock = heldLocks[0]
	} else {
		var err error
		lock, err = acquireReviewerLearningLock(directory, reviewerLearningRunID(), 0, false)
		if err != nil {
			return err
		}
		defer releaseReviewerLearningLock(directory, lock)
	}
	if err := refreshReviewerLearningLock(directory, lock); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create reviewer learning directory: %w", err)
	}
	report.LastCheckpointAt = time.Now().UTC()
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reviewer learning report: %w", err)
	}
	if err := writeReviewerLearningCheckpoint(directory, encoded); err != nil {
		return fmt.Errorf("write reviewer learning JSON: %w", err)
	}
	status, statusErr := json.MarshalIndent(ReviewerLearningStatus{
		RunStatus:                              report.RunStatus,
		PagesCompleted:                         report.LastCompletedPage,
		ExpectedURLs:                           report.ExpectedURLs,
		FetchFailedPages:                       report.FetchFailedPages,
		ProofUsable:                            report.ProofUsable,
		ProofBlockedReason:                     report.ProofBlockedReason,
		LastCompletedPage:                      report.LastCompletedPage,
		CurrentStableStreak:                    report.CurrentStableStreak,
		StableStreakStartPage:                  report.StableStreakStartPage,
		MaxStableStreak:                        report.MaxStableStreak,
		MaxStableStreakStartPage:               report.MaxStableStreakStartPage,
		MaxStableStreakEndPage:                 report.MaxStableStreakEndPage,
		TotalReviewerPrompts:                   report.TotalReviewerPrompts,
		PagesFixedByExistingRules:              report.PagesFixedByExistingRules,
		PagesEverFixedByExistingRules:          report.PagesEverFixedByExistingRules,
		PagesCurrentlyPassingWithExistingRules: report.PagesCurrentlyPassingWithExistingRules,
		PagesWhereExistingRulesHurtScore:       report.PagesWhereExistingRulesHurtScore,
		SourceRulesHelpedCount:                 report.SourceRulesHelpedCount,
		SourceRulesHurtCount:                   report.SourceRulesHurtCount,
		SourceRulesNeutralCount:                report.SourceRulesNeutralCount,
		StrictModelPassPages:                   report.StrictModelPassPages,
		ReviewReady97Pages:                     report.ReviewReady97Pages,
		SoftReviewCandidatePages:               report.SoftReviewCandidatePages,
		PagesNeedingAuditedEdit:                report.PagesNeedingAuditedEdit,
		PagesRequiringManualEditAfterStall:     report.PagesRequiringManualEditAfterStall,
		PagesRequiringHumanConfirmation:        report.PagesRequiringHumanConfirmation,
		ModelInputTokens:                       report.ModelInputTokens,
		ModelOutputTokens:                      report.ModelOutputTokens,
		ReportPath:                             filepath.Join(directory, "reviewer-learning.json"),
		LearningMode:                           report.LearningMode,
		RunID:                                  report.RunID,
		LastCheckpointAt:                       report.LastCheckpointAt,
	}, "", "  ")
	if statusErr != nil {
		return fmt.Errorf("encode reviewer learning status: %w", statusErr)
	}
	if err := os.WriteFile(filepath.Join(directory, "reviewer-learning-status.json"), status, 0o644); err != nil {
		return fmt.Errorf("write reviewer learning status: %w", err)
	}
	rules, err := json.MarshalIndent(report.Rules, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reviewer learning rules: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "reviewer-learning-rules.json"), rules, 0o644); err != nil {
		return fmt.Errorf("write reviewer learning rules: %w", err)
	}
	pages, err := json.MarshalIndent(report.Pages, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reviewer learning pages: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "reviewer-learning-pages.json"), pages, 0o644); err != nil {
		return fmt.Errorf("write reviewer learning pages: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "reviewer-learning-prompts.md"), []byte(reviewerLearningPromptsMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write reviewer learning prompts: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "source-working-prompt.md"), []byte(reviewerLearningSourceWorkingPrompt(report)), 0o644); err != nil {
		return fmt.Errorf("write source working prompt: %w", err)
	}
	file, err := os.Create(filepath.Join(directory, "reviewer-learning.html"))
	if err != nil {
		return fmt.Errorf("create reviewer learning HTML: %w", err)
	}
	defer file.Close()
	functions := template.FuncMap{"mul": func(left, right float64) float64 { return left * right }}
	return template.Must(template.New("reviewer-learning").Funcs(functions).Parse(reviewerLearningHTML)).Execute(file, report)
}

func writeReviewerLearningCheckpoint(directory string, encoded []byte) error {
	file, err := os.CreateTemp(directory, ".reviewer-learning-*.json.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, "reviewer-learning.json"))
}

func reviewerLearningSourceWorkingPrompt(report ReviewerLearningReport) string {
	var builder strings.Builder
	builder.WriteString("# Source Working Prompt\n\n")
	builder.WriteString("These are the active source-scoped reviewer lessons accumulated by the benchmark. They are appended to extraction prompts as reviewer rules; they are not written into the global system prompt.\n\n")
	seenSourceInstructions := map[string]bool{}
	for _, rule := range report.Rules {
		if rule.Scope != RuleScopeSource || rule.Status != "active" {
			continue
		}
		if sourceRuleInstructionIsHarmful(report.Rules, rule) || seenSourceInstructions[rule.Instruction] {
			continue
		}
		seenSourceInstructions[rule.Instruction] = true
		fmt.Fprintf(&builder, "- Rule %d, created on page %d", rule.ID, rule.CreatedOnPage)
		if rule.CreatedOnTurn > 0 {
			fmt.Fprintf(&builder, " turn %d", rule.CreatedOnTurn)
		}
		fmt.Fprintf(&builder, ": %s\n", rule.Instruction)
	}
	return builder.String()
}

func reviewerLearningPromptsMarkdown(report ReviewerLearningReport) string {
	var builder strings.Builder
	builder.WriteString("# Reviewer Learning Prompt Transcript\n\n")
	builder.WriteString("This transcript records the simulated human reviewer prompts used during the sequential benchmark. Source-scoped rules are carried forward to later pages; URL-scoped rules apply only to their originating page.\n\n")
	for _, page := range report.Pages {
		fmt.Fprintf(&builder, "## Page %d\n\n- URL: %s\n- First-pass F1: %.2f%%\n- With existing rules F1: %.2f%%\n- Final approval method: `%s`\n", page.Index, page.URL, page.FirstPass.Metrics.RelevantTextF1*100, page.WithExistingRules.Metrics.RelevantTextF1*100, page.FinalApprovalMethod)
		if page.ReviewerPrompt == "" {
			builder.WriteString("- Reviewer prompt: none\n\n")
		} else {
			for _, turn := range page.ReviewerTurnDetails {
				fmt.Fprintf(&builder, "- Reviewer turn %d\n- Rule scope: `%s`\n- Before F1: %.2f%%\n- Rerun F1: %.2f%%\n\n```text\n%s\n```\n\n", turn.Turn, turn.Scope, turn.BeforeMetrics.RelevantTextF1*100, turn.AfterMetrics.RelevantTextF1*100, turn.Prompt)
			}
		}
		if len(page.AuditedEditOperations) > 0 {
			builder.WriteString("### Audited reviewer edit operations\n\n")
			builder.WriteString("These operations represent what the simulated reviewer changed after model prompting stopped helping. They are final approved edits, not model success.\n\n")
			for _, operation := range page.AuditedEditOperations {
				fmt.Fprintf(&builder, "- `%s`", operation.Operation)
				if operation.BlockID != "" {
					fmt.Fprintf(&builder, " on `%s`", operation.BlockID)
				}
				if operation.Reason != "" {
					fmt.Fprintf(&builder, ": %s", operation.Reason)
				}
				if operation.TextSnippet != "" {
					fmt.Fprintf(&builder, "\n  - Evidence: %s", operation.TextSnippet)
				}
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}
	builder.WriteString("## Learned Rules\n\n")
	for _, rule := range report.Rules {
		fmt.Fprintf(&builder, "- Rule %d (`%s`, reuse %d): %s\n", rule.ID, rule.Scope, rule.ReuseCount, rule.Instruction)
	}
	return builder.String()
}

const reviewerLearningHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Reviewer learning benchmark</title><style>body{font:14px system-ui;max-width:1280px;margin:30px auto;padding:0 20px;color:#172033}.cards{display:grid;grid-template-columns:repeat(4,1fr);gap:12px}.card{border:1px solid #ccd3dc;border-radius:8px;padding:12px;background:#f8fafc}.metric{font-size:24px;font-weight:700}.muted{color:#536274}.yes{color:#087830}.no{color:#b42318}table{border-collapse:collapse;width:100%;margin-top:16px}th,td{border:1px solid #ccd3dc;padding:8px;text-align:left;vertical-align:top}code{background:#eef2f7;padding:2px 4px;border-radius:4px}.turn{border-bottom:1px solid #e2e8f0;margin-bottom:8px;padding-bottom:8px}.turn:last-child{border-bottom:0}</style></head><body><h1>Reviewer Learning Benchmark: {{.FixtureSet}}</h1><p>This report measures semantic extraction plus sequential reviewer learning against the human selector baseline. Final parity may include audited human edits and is not counted as autonomous model success.</p><p>Model mode: <strong>{{.ModelMode}}</strong> · Expected URLs: {{.ExpectedURLs}} · Discovered URLs: {{.DiscoveredURLs}} · Usage: {{.UsageNote}}</p><div class="cards"><section class="card"><div class="muted">First-pass F1</div><div class="metric">{{printf "%.1f%%" (mul .FirstPassAggregate.RelevantTextF1 100)}}</div><div>Exact {{.FirstPassAggregate.ExactParity}}</div></section><section class="card"><div class="muted">With existing rules F1</div><div class="metric">{{printf "%.1f%%" (mul .WithExistingRulesAggregate.RelevantTextF1 100)}}</div><div>{{.PagesEverFixedByExistingRules}} ever fixed · {{.PagesCurrentlyPassingWithExistingRules}} currently passing · {{.PagesWhereExistingRulesHurtScore}} hurt</div></section><section class="card"><div class="muted">After new prompts F1</div><div class="metric">{{printf "%.1f%%" (mul .AfterNewPromptAggregate.RelevantTextF1 100)}}</div><div>{{.TotalReviewerPrompts}} prompts · {{printf "%.2f" .AverageReviewerTurnsPerPage}} turns/page</div></section><section class="card"><div class="muted">Final approved F1</div><div class="metric">{{printf "%.1f%%" (mul .FinalApprovedAggregate.RelevantTextF1 100)}}</div><div>Exact {{.FinalApprovedAggregate.ExactParity}} · audited edits {{.PagesNeedingAuditedEdit}} · human confirmations {{.PagesRequiringHumanConfirmation}} · stalls {{.PagesRequiringManualEditAfterStall}}</div></section></div><div class="cards"><section class="card"><div class="muted">Rules</div><div class="metric">{{len .Rules}}</div><div>{{.SourceScopedRulesCreated}} source · {{.URLScopedRulesCreated}} URL · helped {{.SourceRulesHelpedCount}} · hurt {{.SourceRulesHurtCount}}</div></section><section class="card"><div class="muted">Strict model pass</div><div class="metric">{{.StrictModelPassPages}}</div><div>{{.ReviewReady97Pages}} review-ready at 97%</div></section><section class="card"><div class="muted">Token usage</div><div class="metric">{{if .TokenUsageAvailable}}{{.ModelInputTokens}} / {{.ModelOutputTokens}}{{else}}n/a{{end}}</div><div>input / output</div></section><section class="card"><div class="muted">Selector baseline</div><div class="metric">{{.UsableGoldPages}}</div><div>{{.PartialGoldPages}} partial · {{.StaleSelectorPages}} stale · {{.RepairedGoldPages}} repaired</div></section></div><h2>Pages</h2><table><thead><tr><th>#</th><th>URL</th><th>First F1</th><th>Existing Rules F1</th><th>New Prompt F1</th><th>Turns</th><th>Final Method</th><th>Review Ready</th><th>Stalled</th><th>Prompts</th><th>Audited edits</th></tr></thead><tbody>{{range .Pages}}<tr><td>{{.Index}}</td><td>{{.URL}}</td><td>{{printf "%.1f%%" (mul .FirstPass.Metrics.RelevantTextF1 100)}}</td><td>{{printf "%.1f%%" (mul .WithExistingRules.Metrics.RelevantTextF1 100)}}</td><td>{{printf "%.1f%%" (mul .AfterNewReviewerPrompt.Metrics.RelevantTextF1 100)}}</td><td>{{.ReviewerTurns}}</td><td><code>{{.FinalApprovalMethod}}</code></td><td>{{.ReviewReady}}</td><td>{{.Stalled}}</td><td>{{if .ReviewerTurnDetails}}{{range .ReviewerTurnDetails}}<div class="turn"><strong>Turn {{.Turn}}</strong> <code>{{.Scope}}</code><br>Before {{printf "%.1f%%" (mul .BeforeMetrics.RelevantTextF1 100)}} · After {{printf "%.1f%%" (mul .AfterMetrics.RelevantTextF1 100)}}<br>{{.Prompt}}</div>{{end}}{{else}}none{{end}}</td><td>{{if .AuditedEditOperations}}{{range .AuditedEditOperations}}<div class="turn"><code>{{.Operation}}</code> {{.BlockID}}<br>{{.Reason}}</div>{{end}}{{else}}none{{end}}</td></tr>{{end}}</tbody></table><h2>Rules</h2><table><thead><tr><th>ID</th><th>Scope</th><th>Created page</th><th>Reuse</th><th>Helped</th><th>Hurt</th><th>Neutral</th><th>Instruction</th></tr></thead><tbody>{{range .Rules}}<tr><td>{{.ID}}</td><td><code>{{.Scope}}</code></td><td>{{.CreatedOnPage}}</td><td>{{.ReuseCount}}</td><td>{{len .HelpedURLs}}</td><td>{{len .HurtURLs}}</td><td>{{len .NeutralURLs}}</td><td>{{.Instruction}}</td></tr>{{end}}</tbody></table></body></html>`
