package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/joho/godotenv"
	"semantic-crawler/internal/semantic"
)

const defaultSourceSlug = "generic-health-sample"

type sourceSelection struct {
	Slug         string
	ConfigPath   string
	DatabasePath string
	Config       semantic.SourceConfig
}

func main() {
	version := flag.Bool("version", false, "print version, revision and runtime without loading configuration")
	action := flag.String("action", "semantic-help", "semantic-help, export, list-sources, crawl-site, start-demo, start-ui, selector-health, prompt-diagnostics, proof-readiness, benchmark, reviewer-learning-benchmark, reviewer-learning-status, reviewer-learning-repair, prove-human-parity, eval, model-compatibility, model-preflight, run, check, serve, or mock-webhook")
	website := flag.String("website", "", "hostname or HTTP(S) seed URL for crawl-site")
	scanPages := flag.Int("scan-pages", 10, "page limit for crawl-site, from 1 to 1000")
	scanDepth := flag.Int("scan-depth", 2, "link depth for crawl-site, from 0 to 10")
	sourceSlug := flag.String("source", "", "semantic source slug from configs/sources, for example generic-health-sample")
	configPath := flag.String("source-config", "configs/sources/generic-health-sample.json", "source configuration JSON")
	sourceConfigRoot := flag.String("source-config-root", "configs/sources", "directory containing semantic source config JSON files")
	envFile := flag.String("env-file", "", "optional env file with SEMANTIC_LLM_PROVIDER, SEMANTIC_LLM_API_KEY, SEMANTIC_LLM_BASE_URL, or SEMANTIC_LLM_MODEL")
	mode := flag.String("mode", "snapshot", "snapshot or live")
	modelMode := flag.String("model-mode", "fixture", "fixture or openai")
	databasePath := flag.String("db", "", "SQLite review database")
	reportDirectory := flag.String("report-dir", "artifacts/semantic-crawler", "benchmark output directory")
	fixtureSet := flag.String("fixture-set", "generic-health-sample", "benchmark fixture set")
	benchmarkURLContains := flag.String("benchmark-url-contains", "", "only benchmark URLs containing this string; empty means all mapped URLs")
	baselinePath := flag.String("baseline", "tests/fixtures/generic-health-sample/baseline.json", "portable selector baseline for benchmark/proof actions")
	benchmarkLive := flag.Bool("benchmark-live", false, "fetch every benchmark page live instead of using local fixture HTML")
	benchmarkLiveMissing := flag.Bool("benchmark-live-missing", false, "fetch benchmark pages live when no local fixture HTML exists")
	benchmarkStartPage := flag.Int("benchmark-start-page", 1, "1-based page offset into the mapped benchmark URLs")
	benchmarkMaxPages := flag.Int("benchmark-max-pages", 0, "optional limit for Model smoke benchmarks; 0 means all mapped pages")
	reviewerLearningAdaptive := flag.Bool("reviewer-learning-adaptive", false, "stop reviewer-learning benchmark after stable no-prompt/no-edit near-parity streak")
	reviewerLearningResume := flag.Bool("reviewer-learning-resume", false, "resume reviewer-learning benchmark from existing report artifacts")
	reviewerLearningForce := flag.Bool("reviewer-learning-force", false, "overwrite existing reviewer-learning report artifacts")
	reviewerLearningMinPages := flag.Int("reviewer-learning-min-pages", 10, "minimum pages before adaptive reviewer-learning stop may trigger")
	reviewerLearningStableStreak := flag.Int("reviewer-learning-stable-streak", 5, "consecutive no-prompt/no-edit near-parity pages required for adaptive stop")
	reviewerLearningChunkPages := flag.Int("reviewer-learning-chunk-pages", 0, "optional number of new pages to process before pausing a deep reviewer-learning run; preserves total expected URL count")
	reviewerLearningMaxTurns := flag.Int("reviewer-learning-max-turns", 3, "maximum simulated reviewer prompt turns per page before audited human edit")
	reviewerLearningMode := flag.String("reviewer-learning-mode", "adaptive", "reviewer-learning benchmark mode: adaptive or deep")
	reviewerLearningStrictThreshold := flag.Float64("reviewer-learning-strict-threshold", 0.98, "strict model pass F1 threshold")
	reviewerLearningReviewReadyThreshold := flag.Float64("reviewer-learning-review-ready-threshold", 0.97, "review-ready F1 threshold tracked separately from strict pass")
	reviewerLearningStallWindow := flag.Int("reviewer-learning-stall-window", 3, "consecutive non-progress reviewer turns before deep mode stalls")
	reviewerLearningMinImprovement := flag.Float64("reviewer-learning-min-improvement", 0.002, "minimum F1 improvement that counts as progress in deep mode")
	reviewerLearningLockTimeout := flag.Duration("reviewer-learning-lock-timeout", 30*time.Minute, "legacy compatibility option; OS writer locks control exclusivity and release on process exit")
	goldOverridePath := flag.String("gold-overrides", "", "optional audited benchmark gold override JSON path")
	fetchDelay := flag.Duration("fetch-delay", 0, "optional polite delay before each live HTTP fetch, for example 750ms")
	fetchRetries := flag.Int("fetch-retries", 2, "number of retries for live HTTP 429/5xx responses")
	fetchRetryDelay := flag.Duration("fetch-retry-delay", 2*time.Second, "delay between retries when Retry-After is absent")
	maxPageBytes := flag.Int64("max-page-bytes", semantic.DefaultMaxPageBytes, "maximum decoded bytes per fetched HTML response")
	address := flag.String("addr", "127.0.0.1:8091", "HTTP listen address")
	reviewAllowedHosts := flag.String("review-allowed-hosts", "", "comma-separated review HTTP hostnames; empty uses localhost, 127.0.0.1, and ::1")
	deliveryAllowlist := flag.String("delivery-allowlist", "http://localhost:8092/approved", "comma-separated approved delivery endpoints for the review UI")
	csrfToken := flag.String("csrf-token", "", "CSRF token for review UI POST actions; defaults to SEMANTIC_REVIEW_CSRF_TOKEN or a random token")
	exportFormat := flag.String("format", "jsonl", "export format: json, jsonl, markdown, text")
	exportOutput := flag.String("output", "", "new export file or bundle directory")
	exportProfile := flag.String("content-profile", "complete", "export content profile: complete or legacy")
	exportCandidate := flag.Int64("candidate-id", 0, "export one saved candidate")
	includeUnreviewed := flag.Bool("include-unreviewed", false, "explicitly export unapproved local content with status labels")
	forceReextract := flag.Bool("force-reextract", false, "re-extract unchanged pages on this requested scan without replacing approvals")
	flag.Parse()
	if *version {
		fmt.Print(versionDescription())
		return
	}
	if *maxPageBytes <= 0 || *maxPageBytes == 1<<63-1 || *fetchRetries < 0 || *fetchDelay < 0 || *fetchRetryDelay < 0 {
		log.Fatal("page byte limit must be positive and fetch delay/retry settings must be nonnegative")
	}

	if err := loadSemanticEnv(*envFile); err != nil {
		log.Fatal(err)
	}
	if !flagWasSet("csrf-token") {
		*csrfToken = os.Getenv("SEMANTIC_REVIEW_CSRF_TOKEN")
	}

	switch *action {
	case "semantic-help":
		printSemanticHelp()
		return
	case "list-sources":
		printSources(*sourceConfigRoot)
		return
	}
	startWebsiteScan := *action == "crawl-site"
	if startWebsiteScan {
		if _, err := websiteURL(*website); err != nil {
			log.Fatal(err)
		}
		if _, _, err := serverModelResolver(semantic.SourceEntry{}, "openai"); err != nil {
			log.Fatal(err)
		}
		source, err := prepareWebsiteSource(*sourceConfigRoot, *website, *scanPages, *scanDepth)
		if err != nil {
			log.Fatal(err)
		}
		*sourceSlug, *configPath = source.Slug, source.ConfigPath
		*action, *modelMode = "serve", "openai"
		if !flagWasSet("fetch-delay") {
			*fetchDelay = 750 * time.Millisecond
		}
	}

	if *action == "start-demo" {
		if *sourceSlug == "" {
			*sourceSlug = getenv("SOURCE", defaultSourceSlug)
		}
		*mode = "snapshot"
		*modelMode = "fixture"
	}
	if *action == "start-ui" {
		*action = "serve"
	}

	dbPath := *databasePath
	if (*sourceSlug != "" || *action == "start-demo") && !flagWasSet("db") {
		dbPath = ""
	}
	selection, err := resolveSourceSelection(*sourceSlug, *configPath, dbPath, *sourceConfigRoot)
	if err != nil {
		log.Fatal(err)
	}
	if *action == "export" {
		if *sourceSlug == "" && !flagWasSet("source-config") {
			log.Fatal("export requires an explicit -source or -source-config")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := exportSource(ctx, selection, *exportOutput, semantic.ExportFormat(*exportFormat), semantic.ContentProfile(*exportProfile), semantic.ExportOptions{CandidateID: *exportCandidate, IncludeUnreviewed: *includeUnreviewed}); err != nil {
			log.Fatal(err)
		}
		return
	}
	config := selection.Config
	if startWebsiteScan {
		fmt.Printf("Website source: %s\nConfig: %s\nInstructions: %s\nDatabase: %s\n", selection.Slug, selection.ConfigPath, config.SourceSkillPath, selection.DatabasePath)
	}
	sourceSkill, err := semantic.LoadSourceSkill(config.SourceSkillPath, config.SkillVersion)
	if err != nil && *action != "serve" {
		log.Fatal(err)
	}
	operationCtx := context.Background()
	if *action != "serve" && *action != "mock-webhook" {
		var stop context.CancelFunc
		operationCtx, stop = signal.NotifyContext(operationCtx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}

	switch *action {
	case "selector-health":
		report, err := semantic.RunSelectorHealth(operationCtx, semantic.SelectorHealthOptions{
			MappingPath:     *baselinePath,
			URLContains:     *benchmarkURLContains,
			Fetcher:         &semantic.HTTPFetcher{UserAgent: "SemanticCrawler/1.0", Delay: *fetchDelay, MaxRetries: *fetchRetries, RetryDelay: *fetchRetryDelay, MaxResponseBytes: *maxPageBytes, AllowedHost: config.AllowedHost, AllowedPathPrefix: config.AllowedPathPrefix},
			ReportDirectory: *reportDirectory,
			StartPage:       *benchmarkStartPage,
			MaxPages:        *benchmarkMaxPages,
		})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(map[string]any{
			"report_dir":           *reportDirectory,
			"total_urls":           report.TotalURLs,
			"total_identifiers":    report.TotalIdentifiers,
			"valid_gold_pages":     report.ValidGoldPages,
			"partial_gold_pages":   report.PartialGoldPages,
			"zero_gold_pages":      report.ZeroGoldPages,
			"fetch_failed_pages":   report.FetchFailedPages,
			"redirected_pages":     report.RedirectedPages,
			"matched_selectors":    report.MatchedSelectors,
			"missing_selectors":    report.MissingSelectors,
			"selector_health_json": filepath.Join(*reportDirectory, "selector-health.json"),
			"selector_health_html": filepath.Join(*reportDirectory, "selector-health.html"),
		})
	case "prompt-diagnostics":
		var fetcher semantic.Fetcher
		if *benchmarkLive || *benchmarkLiveMissing {
			fetcher = &semantic.HTTPFetcher{UserAgent: "SemanticCrawler/1.0", Delay: *fetchDelay, MaxRetries: *fetchRetries, RetryDelay: *fetchRetryDelay, MaxResponseBytes: *maxPageBytes, AllowedHost: config.AllowedHost, AllowedPathPrefix: config.AllowedPathPrefix}
		}
		report, err := semantic.RunPromptDiagnostics(operationCtx, semantic.PromptDiagnosticsOptions{
			FixtureDirectory: config.FixtureDirectory,
			MappingPath:      *baselinePath,
			URLContains:      *benchmarkURLContains,
			Scope:            config.Scope,
			SourceSkill:      sourceSkill,
			Fetcher:          fetcher,
			LiveFetch:        *benchmarkLive,
			StartPage:        *benchmarkStartPage,
			MaxPages:         *benchmarkMaxPages,
			ReportDirectory:  *reportDirectory,
		})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(report)
	case "proof-readiness":
		report, err := semantic.LoadProofReadiness(*reportDirectory)
		if err != nil {
			log.Fatal(err)
		}
		path, err := semantic.WriteProofReadinessReport(*reportDirectory, report)
		if err != nil {
			log.Fatal(err)
		}
		printJSON(map[string]any{
			"ready":               report.Ready,
			"blockers":            report.Blockers,
			"selector_health":     report.SelectorHealth,
			"prompt_diagnostics":  report.PromptDiagnostics,
			"model_compatibility": report.ModelCompatibility,
			"model_preflight":     report.ModelPreflight,
			"artifacts":           report.Artifacts,
			"readiness_artifact":  path,
		})
		if !report.Ready {
			os.Exit(2)
		}
	case "benchmark":
		model := mustSemanticModel(*modelMode)
		var fetcher semantic.Fetcher
		if *benchmarkLive || *benchmarkLiveMissing {
			fetcher = &semantic.HTTPFetcher{UserAgent: "SemanticCrawler/1.0", Delay: *fetchDelay, MaxRetries: *fetchRetries, RetryDelay: *fetchRetryDelay, MaxResponseBytes: *maxPageBytes, AllowedHost: config.AllowedHost, AllowedPathPrefix: config.AllowedPathPrefix}
		}
		report, err := semantic.RunBenchmark(operationCtx, semantic.BenchmarkOptions{
			FixtureSet:       *fixtureSet,
			FixtureDirectory: config.FixtureDirectory,
			MappingPath:      *baselinePath,
			URLContains:      *benchmarkURLContains,
			ReportDirectory:  *reportDirectory,
			GoldOverridePath: *goldOverridePath,
			Scope:            config.Scope,
			SourceSkill:      sourceSkill,
			Model:            model,
			ModelMode:        *modelMode,
			Fetcher:          fetcher,
			LiveFetch:        *benchmarkLive,
			StartPage:        *benchmarkStartPage,
			MaxPages:         *benchmarkMaxPages,
		})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(map[string]any{
			"report_dir":                       *reportDirectory,
			"fixture_set":                      report.FixtureSet,
			"model_mode":                       report.ModelMode,
			"expected_urls":                    report.ExpectedURLs,
			"discovered_urls":                  report.DiscoveredURLs,
			"missing_urls":                     len(report.MissingURLs),
			"usable_gold_pages":                report.UsableGoldPages,
			"partial_gold_pages":               report.PartialGoldPages,
			"stale_selector_pages":             report.StaleSelectorPages,
			"repaired_gold_pages":              report.RepairedGoldPages,
			"first_pass_f1":                    report.Aggregate.RelevantTextF1,
			"after_feedback_f1":                report.AfterFeedbackAggregate.RelevantTextF1,
			"simulated_baseline_correction_f1": report.SimulatedBaselineAggregate.RelevantTextF1,
			"audited_exact_parity":             report.SimulatedBaselineAggregate.ExactParity,
			"token_usage_available":            report.TokenUsageAvailable,
			"raw_usage_missing":                report.RawUsageMissing,
			"model_input_tokens":               report.ModelInputTokens,
			"model_output_tokens":              report.ModelOutputTokens,
			"usage_note":                       report.UsageNote,
			"benchmark_json":                   filepath.Join(*reportDirectory, "benchmark.json"),
			"benchmark_html":                   filepath.Join(*reportDirectory, "benchmark.html"),
			"prompt_iteration_report":          filepath.Join(*reportDirectory, "prompt-iteration.html"),
			"reviewer_prompts":                 filepath.Join(*reportDirectory, "reviewer-prompts.md"),
		})
	case "reviewer-learning-benchmark":
		model := mustSemanticModel(*modelMode)
		var fetcher semantic.Fetcher
		if *benchmarkLive || *benchmarkLiveMissing {
			fetcher = &semantic.HTTPFetcher{UserAgent: "SemanticCrawler/1.0", Delay: *fetchDelay, MaxRetries: *fetchRetries, RetryDelay: *fetchRetryDelay, MaxResponseBytes: *maxPageBytes, AllowedHost: config.AllowedHost, AllowedPathPrefix: config.AllowedPathPrefix}
		}
		report, err := semantic.RunReviewerLearningBenchmark(operationCtx, semantic.ReviewerLearningOptions{
			FixtureSet:           *fixtureSet,
			FixtureDirectory:     config.FixtureDirectory,
			MappingPath:          *baselinePath,
			URLContains:          *benchmarkURLContains,
			ReportDirectory:      *reportDirectory,
			GoldOverridePath:     *goldOverridePath,
			Scope:                config.Scope,
			SourceSkill:          sourceSkill,
			Model:                model,
			ModelMode:            *modelMode,
			Fetcher:              fetcher,
			LiveFetch:            *benchmarkLive,
			StartPage:            *benchmarkStartPage,
			MaxPages:             *benchmarkMaxPages,
			LearningMode:         semantic.ReviewerLearningMode(*reviewerLearningMode),
			QualityThreshold:     *reviewerLearningStrictThreshold,
			ReviewReadyThreshold: *reviewerLearningReviewReadyThreshold,
			AdaptiveStop:         *reviewerLearningAdaptive,
			MinPages:             *reviewerLearningMinPages,
			StableStreak:         *reviewerLearningStableStreak,
			ChunkPages:           *reviewerLearningChunkPages,
			MaxReviewerTurns:     *reviewerLearningMaxTurns,
			StallWindow:          *reviewerLearningStallWindow,
			MinImprovement:       *reviewerLearningMinImprovement,
			LockTimeout:          *reviewerLearningLockTimeout,
			Resume:               *reviewerLearningResume,
			Force:                *reviewerLearningForce,
			ProgressWriter:       os.Stdout,
		})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(map[string]any{
			"report_dir":                                  *reportDirectory,
			"fixture_set":                                 report.FixtureSet,
			"implementation_version":                      report.ImplementationVersion,
			"model_mode":                                  report.ModelMode,
			"learning_mode":                               report.LearningMode,
			"run_id":                                      report.RunID,
			"expected_urls":                               report.ExpectedURLs,
			"discovered_urls":                             report.DiscoveredURLs,
			"fetch_failed_pages":                          report.FetchFailedPages,
			"proof_usable":                                report.ProofUsable,
			"proof_blocked_reason":                        report.ProofBlockedReason,
			"usable_gold_pages":                           report.UsableGoldPages,
			"partial_gold_pages":                          report.PartialGoldPages,
			"stale_selector_pages":                        report.StaleSelectorPages,
			"raw_selector_final_f1":                       report.RawSelectorMetrics.RelevantTextF1,
			"semantic_gold_final_f1":                      report.SemanticGoldMetrics.RelevantTextF1,
			"first_pass_f1":                               report.FirstPassAggregate.RelevantTextF1,
			"with_existing_rules_f1":                      report.WithExistingRulesAggregate.RelevantTextF1,
			"after_new_prompt_f1":                         report.AfterNewPromptAggregate.RelevantTextF1,
			"final_approved_f1":                           report.FinalApprovedAggregate.RelevantTextF1,
			"final_approved_exact_parity":                 report.FinalApprovedAggregate.ExactParity,
			"run_status":                                  report.RunStatus,
			"last_completed_page":                         report.LastCompletedPage,
			"current_stable_streak":                       report.CurrentStableStreak,
			"stable_streak_start_page":                    report.StableStreakStartPage,
			"max_stable_streak":                           report.MaxStableStreak,
			"max_stable_streak_start_page":                report.MaxStableStreakStartPage,
			"max_stable_streak_end_page":                  report.MaxStableStreakEndPage,
			"total_reviewer_prompts":                      report.TotalReviewerPrompts,
			"total_reviewer_turns":                        report.TotalReviewerTurns,
			"pages_fixed_by_existing_rules":               report.PagesFixedByExistingRules,
			"pages_ever_fixed_by_existing_rules":          report.PagesEverFixedByExistingRules,
			"pages_currently_passing_with_existing_rules": report.PagesCurrentlyPassingWithExistingRules,
			"pages_where_existing_rules_hurt_score":       report.PagesWhereExistingRulesHurtScore,
			"strict_model_pass_pages":                     report.StrictModelPassPages,
			"review_ready_97_pages":                       report.ReviewReady97Pages,
			"soft_review_candidate_pages":                 report.SoftReviewCandidatePages,
			"autonomous_after_learning_pages":             report.AutonomousAfterLearningPages,
			"pages_needing_audited_human_edit":            report.PagesNeedingAuditedEdit,
			"pages_requiring_manual_edit_after_stall":     report.PagesRequiringManualEditAfterStall,
			"pages_requiring_human_confirmation":          report.PagesRequiringHumanConfirmation,
			"source_rules_helped_count":                   report.SourceRulesHelpedCount,
			"source_rules_hurt_count":                     report.SourceRulesHurtCount,
			"source_rules_neutral_count":                  report.SourceRulesNeutralCount,
			"source_scoped_rules_created":                 report.SourceScopedRulesCreated,
			"url_scoped_rules_created":                    report.URLScopedRulesCreated,
			"token_usage_available":                       report.TokenUsageAvailable,
			"raw_usage_missing":                           report.RawUsageMissing,
			"model_input_tokens":                          report.ModelInputTokens,
			"model_output_tokens":                         report.ModelOutputTokens,
			"usage_note":                                  report.UsageNote,
			"reviewer_learning_json":                      filepath.Join(*reportDirectory, "reviewer-learning.json"),
			"reviewer_learning_html":                      filepath.Join(*reportDirectory, "reviewer-learning.html"),
			"reviewer_learning_prompt_transcript":         filepath.Join(*reportDirectory, "reviewer-learning-prompts.md"),
		})
	case "reviewer-learning-status":
		status, err := semantic.LoadReviewerLearningStatus(*reportDirectory)
		if err != nil {
			log.Fatal(err)
		}
		printJSON(status)
	case "reviewer-learning-repair":
		report, err := semantic.RepairReviewerLearningReport(*reportDirectory)
		if err != nil {
			log.Fatal(err)
		}
		printJSON(map[string]any{
			"report_dir":                              *reportDirectory,
			"run_status":                              report.RunStatus,
			"implementation_version":                  report.ImplementationVersion,
			"last_completed_page":                     report.LastCompletedPage,
			"expected_urls":                           report.ExpectedURLs,
			"fetch_failed_pages":                      report.FetchFailedPages,
			"proof_usable":                            report.ProofUsable,
			"proof_blocked_reason":                    report.ProofBlockedReason,
			"pages_needing_audited_human_edit":        report.PagesNeedingAuditedEdit,
			"pages_requiring_manual_edit_after_stall": report.PagesRequiringManualEditAfterStall,
			"pages_requiring_human_confirmation":      report.PagesRequiringHumanConfirmation,
			"reviewer_learning_json":                  filepath.Join(*reportDirectory, "reviewer-learning.json"),
			"reviewer_learning_status":                filepath.Join(*reportDirectory, "reviewer-learning-status.json"),
		})
	case "prove-human-parity":
		model := mustSemanticModel(*modelMode)
		report, err := semantic.RunHumanParityProof(operationCtx, semantic.HumanParityOptions{
			FixtureSet:       *fixtureSet,
			FixtureDirectory: config.FixtureDirectory,
			MappingPath:      *baselinePath,
			URLContains:      *benchmarkURLContains,
			ReportDirectory:  *reportDirectory,
			Scope:            config.Scope,
			SourceSkill:      sourceSkill,
			Model:            model,
			MaxPages:         *benchmarkMaxPages,
		})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(report)
		if !report.Passed {
			os.Exit(2)
		}
	case "eval":
		model := mustSemanticModel(*modelMode)
		goldPath := config.GoldPath
		if goldPath == "" {
			log.Fatal("gold_path is required in source config for -action eval")
		}
		report, err := semantic.RunEvaluation(operationCtx, semantic.EvaluationOptions{
			FixtureDirectory: config.FixtureDirectory,
			GoldPath:         goldPath,
			ReportDirectory:  *reportDirectory,
			Scope:            config.Scope,
			SourceSkill:      sourceSkill,
			Model:            model,
			CrawlConfig: semantic.CrawlConfig{
				SourceID:          config.SourceID,
				SeedURL:           config.SeedURL,
				AllowedHost:       config.AllowedHost,
				AllowedPathPrefix: config.AllowedPathPrefix,
				Scope:             config.Scope,
				SourceSkill:       sourceSkill,
				MaxDepth:          config.MaxDepth,
				MaxPages:          config.MaxPages,
				SkillVersion:      config.SkillVersion,
				Mode:              "snapshot",
				Model:             modelName(*modelMode),
				PromptVersion:     semantic.ExtractionVersion,
			},
		})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(report)
		if !report.QualityGates.Passed {
			os.Exit(2)
		}
	case "model-preflight":
		timeout, err := semanticModelTimeout()
		if err != nil {
			log.Fatal(err)
		}
		retries, retryDelay, err := semanticModelRetryConfig()
		if err != nil {
			log.Fatal(err)
		}
		maxTokens, err := semanticPreflightMaxTokens()
		if err != nil {
			log.Fatal(err)
		}
		openAIConfig, err := openAIConfigFromEnv(timeout)
		if err != nil {
			log.Fatal(err)
		}
		openAIConfig.MaxTokens = maxTokens
		openAIConfig.MaxRetries = retries
		openAIConfig.RetryDelay = retryDelay
		model := semantic.NewOpenAIClient(openAIConfig)
		report, err := semantic.RunModelPreflight(operationCtx, semantic.ModelPreflightOptions{
			Model:           model,
			ModelName:       modelName("openai"),
			Scope:           config.Scope,
			URL:             config.SeedURL,
			RequestTimeout:  timeout,
			MaxTokens:       maxTokens,
			MaxRetries:      retries,
			RetryDelay:      retryDelay,
			ReportDirectory: *reportDirectory,
		})
		printJSON(report)
		if err != nil {
			os.Exit(1)
		}
	case "model-compatibility":
		timeout, err := semanticModelTimeout()
		if err != nil {
			log.Fatal(err)
		}
		maxTokens, err := semanticPreflightMaxTokens()
		if err != nil {
			log.Fatal(err)
		}
		openAIConfig, err := openAIConfigFromEnv(timeout)
		if err != nil {
			log.Fatal(err)
		}
		openAIConfig.MaxTokens = maxTokens
		report, err := semantic.RunOpenAICompatibility(operationCtx, semantic.OpenAICompatibilityOptions{
			Config:          openAIConfig,
			ReportDirectory: *reportDirectory,
		})
		printJSON(report)
		if err != nil {
			os.Exit(1)
		}
	case "run", "check":
		if *mode != "snapshot" && *mode != "live" {
			log.Fatalf("unknown crawl mode %q; use snapshot or live", *mode)
		}
		model := mustSemanticModel(*modelMode)
		scanLock, err := semantic.AcquireScanLock(selection.DatabasePath)
		if err != nil {
			log.Fatal(err)
		}
		defer scanLock.Close()
		store := mustStore(selection.DatabasePath)
		fetcher := semantic.Fetcher(&semantic.HTTPFetcher{UserAgent: "SemanticCrawler/1.0", Delay: *fetchDelay, MaxRetries: *fetchRetries, RetryDelay: *fetchRetryDelay, MaxResponseBytes: *maxPageBytes})
		if *mode == "snapshot" {
			fetcher = &semantic.SnapshotFetcher{Directory: config.FixtureDirectory}
		}
		pipeline := semantic.NewPipeline(fetcher, model, store)
		summary, err := pipeline.Run(operationCtx, semantic.CrawlConfig{ForceReextract: *forceReextract, ModelMode: *modelMode, SourceID: config.SourceID, SeedURL: config.SeedURL, AllowedHost: config.AllowedHost, AllowedPathPrefix: config.AllowedPathPrefix, Scope: config.Scope, SourceSkill: sourceSkill, MaxDepth: config.MaxDepth, MaxPages: config.MaxPages, SkillVersion: config.SkillVersion, Mode: *mode, Model: modelName(*modelMode), PromptVersion: semantic.ExtractionVersion})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(summary)
		fmt.Printf("\nReview UI:\n  go run ./cmd/semantic-crawler -action serve -model-mode %s -source-config %s -db %s -addr %s\n  http://%s/semantic\n", *modelMode, selection.ConfigPath, selection.DatabasePath, *address, *address)
		if summary.Failed > 0 {
			os.Exit(2)
		}
	case "start-demo":
		model := mustSemanticModel(*modelMode)
		scanLock, err := semantic.AcquireScanLock(selection.DatabasePath)
		if err != nil {
			log.Fatal(err)
		}
		defer scanLock.Close()
		store := mustStore(selection.DatabasePath)
		fetcher := semantic.Fetcher(&semantic.SnapshotFetcher{Directory: config.FixtureDirectory})
		pipeline := semantic.NewPipeline(fetcher, model, store)
		summary, err := pipeline.Run(operationCtx, semantic.CrawlConfig{ForceReextract: *forceReextract, ModelMode: *modelMode, SourceID: config.SourceID, SeedURL: config.SeedURL, AllowedHost: config.AllowedHost, AllowedPathPrefix: config.AllowedPathPrefix, Scope: config.Scope, SourceSkill: sourceSkill, MaxDepth: config.MaxDepth, MaxPages: config.MaxPages, SkillVersion: config.SkillVersion, Mode: "snapshot", Model: modelName("fixture"), PromptVersion: semantic.ExtractionVersion})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(summary)
		fmt.Printf("\nStart the UI:\n  make semantic-demo-ui\n\nOpen:\n  http://%s/semantic\n", *address)
		if summary.Failed > 0 {
			os.Exit(2)
		}
	case "serve":
		serverCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		startup := semantic.SourceEntry{Slug: selection.Slug, ConfigPath: selection.ConfigPath, Config: config}
		allowlist := splitCSV(*deliveryAllowlist)
		workspaces := semantic.NewSourceWorkspaces(semantic.NewSourceRegistry(*sourceConfigRoot), startup, selection.DatabasePath, func(ctx context.Context, source semantic.SourceEntry, path string, create bool) (*semantic.SourceWorkspace, error) {
			return openReviewWorkspace(ctx, source, path, create, serverModelResolver, allowlist, *modelMode)
		})
		setupLock, lockErr := semantic.AcquireScanLock(selection.DatabasePath)
		create := lockErr == nil
		if lockErr != nil && !errors.Is(lockErr, semantic.ErrScanBusy) {
			log.Fatal(lockErr)
		}
		workspace, openErr := workspaces.Resolve(serverCtx, selection.Slug, create)
		if setupLock != nil {
			setupLock.Close()
		}
		if openErr != nil {
			log.Fatal(openErr)
		}
		if statuses, err := workspaces.List(); err == nil {
			for _, status := range statuses {
				if status.Source != nil {
					if err = workspaces.Recover(serverCtx, status.Slug); err != nil {
						log.Printf("Source recovery is unavailable for %s", status.Slug)
					}
				}
			}
		}
		scans := semantic.NewScanManager(serverCtx, nil)
		fetch := semantic.FetchSettings{Delay: *fetchDelay, Retries: *fetchRetries, RetryDelay: *fetchRetryDelay, MaxPageBytes: *maxPageBytes}
		options := semantic.ReviewRouterOptions{CSRFToken: *csrfToken, SourceConfigRoot: *sourceConfigRoot, AllowedHosts: splitCSV(*reviewAllowedHosts), SourceID: config.SourceID, SourceSlug: selection.Slug, SourceName: config.Name, Workspaces: workspaces, Scans: scans, FetchSettings: fetch, Prepare: func(ctx context.Context, request semantic.ScanRequest) (semantic.PreparedScan, error) {
			return semantic.PrepareScan(ctx, workspaces, serverModelResolver, request, fetch)
		}}
		handler := semantic.NewReviewRouterWithOptions(workspace.Store, workspace.Reviews, workspace.Deliveries, options, workspace.Retrier)
		serverErr := serveReview(serverCtx, *address, handler, scans.Shutdown, func() error {
			log.Printf("Semantic Crawler listening on http://%s/semantic", *address)
			if !startWebsiteScan {
				return nil
			}
			prepared, err := options.Prepare(serverCtx, semantic.ScanRequest{ForceReextract: *forceReextract, SourceSlug: selection.Slug, RequestKey: rand.Text(), ModelMode: "openai", MaxPages: *scanPages, MaxDepth: *scanDepth})
			if err != nil {
				return err
			}
			run, err := scans.Start(serverCtx, prepared)
			if err == nil {
				log.Printf("Website scan started: http://%s/semantic/sources/%s/scans/%d", *address, selection.Slug, run.ID)
			}
			return err
		})
		if serverErr == nil {
			serverErr = workspaces.Close()
		}
		if serverErr != nil {
			log.Fatal(serverErr)
		}
	case "mock-webhook":
		serveMockWebhook(*address)
	default:
		log.Fatalf("unknown action %q", *action)
	}
	if err := operationCtx.Err(); err != nil {
		log.Fatal(err)
	}
}

func modelName(mode string) string {
	if mode == "fixture" {
		return "deterministic-fixture-model"
	}
	return os.Getenv("SEMANTIC_LLM_MODEL")
}

func mustSemanticModel(mode string) semantic.SemanticModel {
	model, err := semanticModel(mode)
	if err != nil {
		log.Fatal(err)
	}
	return model
}

func loadSemanticEnv(path string) error {
	if path != "" {
		if err := godotenv.Overload(path); err != nil {
			return fmt.Errorf("load env file %s: %w", path, err)
		}
		return nil
	}
	for _, filename := range []string{".env.semantic-crawler", ".env"} {
		if err := godotenv.Load(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load env file %s: %w", filename, err)
		}
	}
	return nil
}

func resolveSourceSelection(slug, configPath, databasePath, configRoot string) (sourceSelection, error) {
	if slug == "" && configPath == "" {
		slug = defaultSourceSlug
	}

	if slug != "" {
		source, err := semantic.NewSourceRegistry(configRoot).Get(slug)
		if err != nil {
			return sourceSelection{}, err
		}
		if databasePath == "" {
			databasePath = semantic.DefaultSemanticDBPath(slug)
		}
		return sourceSelection{Slug: slug, ConfigPath: source.ConfigPath, DatabasePath: databasePath, Config: source.Config}, nil
	}
	config, err := semantic.LoadSourceConfigFile(configPath)
	if err != nil {
		return sourceSelection{}, err
	}
	resolvedSlug := strings.TrimSuffix(filepath.Base(configPath), ".json")
	if databasePath == "" {
		databasePath = semantic.DefaultSemanticDBPath(resolvedSlug)
	}
	return sourceSelection{Slug: resolvedSlug, ConfigPath: configPath, DatabasePath: databasePath, Config: config}, nil
}

func printSemanticHelp() {
	fmt.Print(`Semantic Crawler

  make setup                Check prerequisites, download dependencies, and preserve existing .env.
  make start                Build and start the local UI at http://127.0.0.1:8091/semantic.
  make doctor               Check Go, CGO, and C compiler availability.
  make help                 Show Make commands without compiling.
  make example.com          Start the UI and scan a website using your configured model.
  make website URL=https://example.com/docs/  Scan a specific path.
  make demo                 Create offline review candidates from the synthetic sample.
  make review               Alias for make start.
  make eval                 Run the portable offline evaluation.
  make benchmark            Compare extraction against the synthetic selector baseline.
  make reviewer-learning    Run the offline reviewer-learning benchmark.
  make crawl SOURCE=slug    Crawl a configured source using your model endpoint.
  make check SOURCE=slug    Check a source for changes using your model endpoint.
  make preflight            Check configured structured-output model support.
  make compatibility        Check the configured OpenAI-compatible endpoint.

  go run ./cmd/semantic-crawler -action list-sources
  go run ./cmd/semantic-crawler -h

Live model use requires SEMANTIC_LLM_MODEL and your SEMANTIC_LLM_API_KEY in .env.
SEMANTIC_LLM_PROVIDER=openai, gemini, deepseek, qwen, or zai supplies an endpoint; custom requires SEMANTIC_LLM_BASE_URL.
Budget presets use JSON mode with local schema validation. See README.md for model and pricing examples.
MODEL_MODE=openai is the shared API protocol for all provider settings.
The bundled fixture model makes no network requests and does not measure AI quality.
`)
}

func printSources(configRoot string) {
	sources, err := semantic.NewSourceRegistry(configRoot).List()
	if err != nil {
		log.Fatal(err)
	}
	if len(sources) == 0 {
		fmt.Println("No semantic sources found.")
		return
	}
	for _, source := range sources {
		fmt.Printf("%s\t%s\t%s\n", source.Slug, source.Config.Name, source.ConfigPath)
	}
}

func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func semanticModel(mode string) (semantic.SemanticModel, error) {
	if mode == "fixture" {
		return &semantic.FixtureModel{}, nil
	}
	if mode != "openai" {
		return nil, fmt.Errorf("unknown model mode %q", mode)
	}
	timeout, err := semanticModelTimeout()
	if err != nil {
		return nil, err
	}
	maxTokens, err := semanticModelMaxTokens()
	if err != nil {
		return nil, err
	}
	retries, retryDelay, err := semanticModelRetryConfig()
	if err != nil {
		return nil, err
	}
	config, err := openAIConfigFromEnv(timeout)
	if err != nil {
		return nil, err
	}
	config.MaxTokens = maxTokens
	config.MaxRetries = retries
	config.RetryDelay = retryDelay
	return semantic.NewOpenAIClient(config), nil
}

func openAIConfigFromEnv(timeout time.Duration) (semantic.OpenAIConfig, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("SEMANTIC_LLM_PROVIDER")))
	endpoint := strings.TrimSpace(os.Getenv("SEMANTIC_LLM_BASE_URL"))
	switch provider {
	case "", "custom":
		// Keep legacy custom-endpoint settings and report resume identities intact.
		provider = ""
	case "openai":
		if endpoint == "" {
			endpoint = "https://api.openai.com/v1"
		}
	case "gemini":
		if endpoint == "" {
			endpoint = "https://generativelanguage.googleapis.com/v1beta/openai"
		}
	case "deepseek":
		if endpoint == "" {
			endpoint = "https://api.deepseek.com"
		}
	case "qwen":
		if endpoint == "" {
			endpoint = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
		}
	case "zai":
		if endpoint == "" {
			endpoint = "https://api.z.ai/api/paas/v4"
		}
	default:
		return semantic.OpenAIConfig{}, fmt.Errorf("SEMANTIC_LLM_PROVIDER must be openai, gemini, deepseek, qwen, zai, or custom")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return semantic.OpenAIConfig{}, fmt.Errorf("SEMANTIC_LLM_BASE_URL must be an absolute HTTP(S) endpoint without credentials, query, or fragment")
	}
	model := strings.TrimSpace(os.Getenv("SEMANTIC_LLM_MODEL"))
	if model == "" {
		return semantic.OpenAIConfig{}, fmt.Errorf("SEMANTIC_LLM_MODEL is required for configured model extraction")
	}

	key := strings.TrimSpace(os.Getenv("SEMANTIC_LLM_API_KEY"))
	if key == "" {
		return semantic.OpenAIConfig{}, fmt.Errorf("SEMANTIC_LLM_API_KEY is required for configured model extraction")
	}
	if strings.ContainsFunc(key, func(char rune) bool { return unicode.IsControl(char) || unicode.IsSpace(char) }) {
		return semantic.OpenAIConfig{}, fmt.Errorf("SEMANTIC_LLM_API_KEY must be a single token without whitespace or control characters")
	}
	return semantic.OpenAIConfig{
		Provider:  provider,
		BaseURL:   endpoint,
		APIKey:    key,
		Model:     model,
		Timeout:   timeout,
		MaxTokens: 65536,
	}, nil
}

func semanticModelTimeout() (time.Duration, error) {
	raw := getenv("SEMANTIC_LLM_TIMEOUT", "10m")
	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse SEMANTIC_LLM_TIMEOUT %q: %w", raw, err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("SEMANTIC_LLM_TIMEOUT must be > 0")
	}
	return timeout, nil
}

func semanticModelRetryConfig() (int, time.Duration, error) {
	rawRetries := getenv("SEMANTIC_LLM_RETRIES", "1")
	retries, err := strconv.Atoi(rawRetries)
	if err != nil {
		return 0, 0, fmt.Errorf("parse SEMANTIC_LLM_RETRIES %q: %w", rawRetries, err)
	}
	if retries < 0 {
		return 0, 0, fmt.Errorf("SEMANTIC_LLM_RETRIES must be >= 0")
	}
	rawDelay := getenv("SEMANTIC_LLM_RETRY_DELAY", "5s")
	delay, err := time.ParseDuration(rawDelay)
	if err != nil {
		return 0, 0, fmt.Errorf("parse SEMANTIC_LLM_RETRY_DELAY %q: %w", rawDelay, err)
	}
	if delay <= 0 {
		return 0, 0, fmt.Errorf("SEMANTIC_LLM_RETRY_DELAY must be > 0")
	}
	return retries, delay, nil
}

func semanticPreflightMaxTokens() (int, error) {
	raw := getenv("SEMANTIC_LLM_PREFLIGHT_MAX_TOKENS", "4096")
	maxTokens, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse SEMANTIC_LLM_PREFLIGHT_MAX_TOKENS %q: %w", raw, err)
	}
	if maxTokens <= 0 {
		return 0, fmt.Errorf("SEMANTIC_LLM_PREFLIGHT_MAX_TOKENS must be > 0")
	}
	return maxTokens, nil
}

func semanticModelMaxTokens() (int, error) {
	raw := getenv("SEMANTIC_LLM_MAX_TOKENS", "16384")
	maxTokens, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse SEMANTIC_LLM_MAX_TOKENS %q: %w", raw, err)
	}
	if maxTokens <= 0 {
		return 0, fmt.Errorf("SEMANTIC_LLM_MAX_TOKENS must be > 0")
	}
	return maxTokens, nil
}

func mustStore(path string) *semantic.GormStore {
	store, err := semantic.OpenReviewStore(context.Background(), path, true)
	if err != nil {
		log.Fatal(err)
	}
	return store
}

func serveMockWebhook(address string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/approved", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var payload semantic.DeliveryPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("mock receiver accepted candidate=%d origin=%s sections=%d", payload.CandidateID, payload.Origin, len(payload.Sections))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	})
	log.Printf("mock webhook listening on %s (POST /approved)", address)
	log.Fatal(http.ListenAndServe(address, mux))
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func printJSON(value any) {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(encoded))
}
