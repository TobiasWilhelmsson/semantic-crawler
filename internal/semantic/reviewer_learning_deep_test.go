package semantic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type promptSensitiveLearningModel struct {
	extracts int
}

func (model *promptSensitiveLearningModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4, Latency: time.Millisecond}, nil
}

type blockIDReviewerModel struct{}

func (model *blockIDReviewerModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4, Latency: time.Millisecond}, nil
}

func (model *blockIDReviewerModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	decisions := make([]BlockDecision, 0, len(blocks))
	for _, block := range blocks {
		include := block.ID == "b0001" || strings.Contains(prompt, "block_id b0002")
		role, blockType, parseMode := fixtureClassification(block)
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: include, Role: role, Type: blockType, ParseMode: parseMode, Reason: "block-id reviewer model"})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions}
	return response, ModelUsage{InputTokens: 11, OutputTokens: 4, TotalTokens: 15, Latency: time.Millisecond}, ValidateExtraction(response, blocks)
}

type failFirstExtractModel struct{}

func (model *failFirstExtractModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{}, nil
}

func (model *failFirstExtractModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	return ExtractionResponse{}, ModelUsage{Latency: time.Millisecond}, errors.New("forced model timeout")
}

type failReviewerTurnModel struct {
	calls int
}

func (model *failReviewerTurnModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{}, nil
}

func (model *failReviewerTurnModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.calls++
	if model.calls > 1 {
		return ExtractionResponse{}, ModelUsage{InputTokens: 5, OutputTokens: 0, TotalTokens: 5, Latency: time.Millisecond}, fmt.Errorf("model response contained no structured content")
	}
	decisions := make([]BlockDecision, 0, len(blocks))
	for _, block := range blocks {
		role, blockType, parseMode := fixtureClassification(block)
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: block.Tag == "h1", Role: role, Type: blockType, ParseMode: parseMode, Reason: "first pass title only"})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions}
	return response, ModelUsage{InputTokens: 8, OutputTokens: 3, TotalTokens: 11, Latency: time.Millisecond}, ValidateExtraction(response, blocks)
}

type reviewReadyNoProgressModel struct{}

func (model *reviewReadyNoProgressModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{}, nil
}

func (model *reviewReadyNoProgressModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	decisions := make([]BlockDecision, 0, len(blocks))
	for _, block := range blocks {
		role, blockType, parseMode := fixtureClassification(block)
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: !strings.Contains(block.Text, "tiny omitted note"), Role: role, Type: blockType, ParseMode: parseMode, Reason: "stable review-ready output"})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions}
	return response, ModelUsage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7, Latency: time.Millisecond}, ValidateExtraction(response, blocks)
}

type worseningReviewerTurnModel struct {
	calls int
}

func (model *worseningReviewerTurnModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{}, nil
}

func (model *worseningReviewerTurnModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.calls++
	includeBody := model.calls == 2
	decisions := make([]BlockDecision, 0, len(blocks))
	for _, block := range blocks {
		role, blockType, parseMode := fixtureClassification(block)
		include := strings.Contains(block.Text, "Title") || includeBody && strings.Contains(block.Text, "Body")
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: include, Role: role, Type: blockType, ParseMode: parseMode, Reason: "worsening reviewer turn model"})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions}
	return response, ModelUsage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7, Latency: time.Millisecond}, ValidateExtraction(response, blocks)
}

func TestReviewerLearningDeepContinuesFromBestModelLayerAfterHarmfulTurn(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"body","role":"content","container":"p.body","parse_mode":true},
		{"name":"note","role":"content","container":"p.note","parse_mode":true}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1>Title A</h1><p class="body">Body clinical paragraph A</p><p class="note">Note clinical paragraph A</p></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-keep-best-layer",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      filepath.Join(directory, "reports"),
		Model:                &worseningReviewerTurnModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.99,
		ReviewReadyThreshold: 0.99,
		StallWindow:          3,
		MinImprovement:       0.002,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run deep reviewer learning benchmark: %v", err)
	}
	page := report.Pages[0]
	if len(page.ReviewerTurnDetails) < 3 {
		t.Fatalf("test expected at least three reviewer turns, got %#v", page.ReviewerTurnDetails)
	}
	improved := page.ReviewerTurnDetails[0].AfterMetrics.RelevantTextF1
	hurt := page.ReviewerTurnDetails[1].AfterMetrics.RelevantTextF1
	nextBefore := page.ReviewerTurnDetails[2].BeforeMetrics.RelevantTextF1
	if !(improved > hurt) {
		t.Fatalf("test setup expected second turn to hurt score, got improved=%f hurt=%f details=%#v", improved, hurt, page.ReviewerTurnDetails)
	}
	if nextBefore != improved {
		t.Fatalf("next reviewer turn should continue from best candidate, got before=%f best=%f hurt=%f details=%#v", nextBefore, improved, hurt, page.ReviewerTurnDetails)
	}
	if page.AfterNewReviewerPrompt.Metrics.RelevantTextF1 != improved {
		t.Fatalf("after_new_reviewer_prompt should retain best post-prompt model candidate, got %#v best=%f", page.AfterNewReviewerPrompt.Metrics, improved)
	}
}

func TestReviewerLearningDeepContinuesReviewReadyNoProgressUntilStall(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"body","role":"content","container":"p.body","parse_mode":true},
		{"name":"note","role":"content","container":"aside","parse_mode":false}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	bodyTerms := make([]string, 0, 120)
	for index := 0; index < 120; index++ {
		bodyTerms = append(bodyTerms, fmt.Sprintf("clinicaltoken%d", index))
	}
	html := `<html><body><main><p class="body">` + strings.Join(bodyTerms, " ") + `</p><aside>tiny omitted note extra detail</aside></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-review-ready-no-progress",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      filepath.Join(directory, "reports"),
		Model:                &reviewReadyNoProgressModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		StallWindow:          3,
		MinImprovement:       0.002,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run deep reviewer learning benchmark: %v", err)
	}
	page := report.Pages[0]
	if page.FinalApprovalMethod == ReviewerLearningApprovalReviewReadyNoStrict {
		t.Fatalf("review-ready no-progress page must keep prompting toward strict pass until stall/oracle/audited edit, not stop as human confirmation: %#v", page)
	}
	if page.ReviewerTurns < 3 {
		t.Fatalf("review-ready no-progress page should not stop before the configured stall window, got turns=%d details=%#v", page.ReviewerTurns, page.ReviewerTurnDetails)
	}
	if !page.Stalled || !page.ManualEditRequired || page.FinalApprovalMethod != ReviewerLearningApprovalAuditedBlockSelect {
		t.Fatalf("after stall the benchmark should apply source-evidenced audited reviewer edits, got %#v", page)
	}
	if !strings.Contains(page.StallReason, "strict 0.98 F1") {
		t.Fatalf("stall reason should explain that strict pass was not reached, got %q", page.StallReason)
	}
	if report.PagesRequiringHumanConfirmation != 0 || report.PagesNeedingAuditedEdit != 1 {
		t.Fatalf("summary should not count review-ready no-progress as model success or simple human confirmation: %#v", report)
	}
}

func TestReviewerLearningDeepReviewerPromptCanTargetExactCandidateBlockID(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"body","role":"content","container":"p.body","parse_mode":true}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1>Title A</h1><p class="body">Important clinical paragraph A</p></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-block-id-prompt",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      filepath.Join(directory, "reports"),
		Model:                &blockIDReviewerModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		StallWindow:          3,
		MinImprovement:       0.002,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run deep reviewer learning benchmark: %v", err)
	}
	page := report.Pages[0]
	if page.FinalApprovalMethod != ReviewerLearningApprovalNewReviewerPrompt || page.ReviewerTurns != 1 {
		t.Fatalf("reviewer should fix the page with one exact block-id prompt, got %#v", page)
	}
	if !strings.Contains(page.ReviewerPrompt, "block_id b0002") {
		t.Fatalf("reviewer prompt should point at the exact missing candidate block ID, got %q", page.ReviewerPrompt)
	}
}

func (model *promptSensitiveLearningModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extracts++
	decisions := make([]BlockDecision, 0, len(blocks))
	include := map[string]bool{"title": true}
	if strings.Contains(prompt, "Include source blocks containing: \"Observera: critical warning A") {
		include["warning"] = true
	}
	if strings.Contains(prompt, "warning/info boxes") {
		include["warning"] = true
	}
	if strings.Contains(prompt, "Include source blocks containing: \"Dosage table A") {
		include["table"] = true
	}
	if strings.Contains(prompt, "Include source blocks containing: \"Clinical image A") {
		include["image"] = true
	}
	if strings.Contains(prompt, "Include source blocks containing: \"Approved by A") {
		include["metadata"] = true
	}
	for _, block := range blocks {
		role, blockType, parseMode := fixtureClassification(block)
		text := strings.ToLower(block.Text)
		includeBlock := include[strings.ToLower(block.Locator)] ||
			strings.Contains(text, "title") ||
			(strings.Contains(text, "observera") && include["warning"]) ||
			(strings.Contains(text, "dosage table") && include["table"]) ||
			(strings.Contains(text, "clinical image") && include["image"]) ||
			(strings.Contains(text, "approved by") && include["metadata"])
		decisions = append(decisions, BlockDecision{
			BlockID:   block.ID,
			Include:   includeBlock,
			Role:      role,
			Type:      blockType,
			ParseMode: parseMode,
			Reason:    "prompt-sensitive decision",
		})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions}
	return response, ModelUsage{InputTokens: 13, OutputTokens: 5, TotalTokens: 18, Latency: 2 * time.Millisecond}, ValidateExtraction(response, blocks)
}

func TestReviewerLearningDeepHasNoFixedMaxTurnCutoff(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false},
		{"name":"table","role":"table","container":"table","parse_mode":true},
		{"name":"image","role":"media","container":"figure","parse_mode":false},
		{"name":"metadata","role":"metadata","container":".metadata","parse_mode":false}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1 data-locator="title">Title A</h1><aside data-locator="warning">Observera: critical warning A</aside><table data-locator="table"><tr><td>Dosage table A</td></tr></table><figure data-locator="image">Clinical image A</figure><p class="metadata" data-locator="metadata">Approved by A</p></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-no-fixed-max",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      filepath.Join(directory, "reports"),
		Model:                &promptSensitiveLearningModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		StallWindow:          3,
		MinImprovement:       0.002,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run deep reviewer learning benchmark: %v", err)
	}
	page := report.Pages[0]
	if page.ReviewerTurns <= 3 {
		t.Fatalf("deep mode should continue beyond the old fixed 3-turn cap, got %#v", page)
	}
	if page.FinalApprovalMethod != ReviewerLearningApprovalNewReviewerPrompt || page.FinalApproved.Metrics.RelevantTextF1 < 0.98 {
		t.Fatalf("deep mode should keep prompting until model strict pass, got %#v", page)
	}
	if report.PagesRequiringManualEditAfterStall != 0 || report.StrictModelPassPages != 1 {
		t.Fatalf("strict model success should not be counted as audited edit: %#v", report)
	}
	prompt, err := os.ReadFile(filepath.Join(directory, "reports", "reviewer-learning-prompts.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "Observera: critical warning A") || !strings.Contains(string(prompt), "Dosage table A") {
		t.Fatalf("prompt transcript should contain accumulated reviewer lessons:\n%s", string(prompt))
	}
}

func TestReviewerLearningDeepUsesAuditedReviewerEditOperationsWhenOraclePromptStillFallsShort(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"body","role":"content","container":"p.body","parse_mode":true}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	longTitle := "Title one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen"
	html := `<html><body><main><h1 data-locator="title">` + longTitle + `</h1><p class="body" data-locator="body">omitted alpha beta gamma delta epsilon zeta</p></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	model := &learningModel{}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-stall",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      filepath.Join(directory, "reports"),
		Model:                model,
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		StallWindow:          3,
		MinImprovement:       0.002,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run deep reviewer learning benchmark: %v", err)
	}
	page := report.Pages[0]
	if page.FinalApprovalMethod != ReviewerLearningApprovalAuditedBlockSelect {
		t.Fatalf("deep mode should use source-evidenced audited reviewer edits when oracle prompt still falls short: %#v", page)
	}
	if page.ReviewerTurns != 4 {
		t.Fatalf("oracle reviewer prompt should be recorded as an additional turn, got %#v", page)
	}
	if !strings.Contains(page.ReviewerTurnDetails[len(page.ReviewerTurnDetails)-1].Prompt, "final reviewer correction") {
		t.Fatalf("last turn should be the oracle reviewer prompt: %#v", page.ReviewerTurnDetails)
	}
	if page.AfterNewReviewerPrompt.Metrics.RelevantTextF1 >= 0.97 {
		t.Fatalf("model/oracle layer should remain below review-ready in this fallback test: %#v", page)
	}
	if page.ReviewReady || page.SoftReviewCandidate {
		t.Fatalf("audited human final edit must not make the model layer look review-ready: %#v", page)
	}
	if !page.FinalApproved.Metrics.ExactParity || page.FinalApproved.Metrics.RelevantTextF1 != 1 {
		t.Fatalf("audited reviewer edit should produce exact final parity: %#v", page.FinalApproved.Metrics)
	}
	if len(page.AuditedEditOperations) == 0 {
		t.Fatalf("audited reviewer edit should record the operations a reviewer performed: %#v", page)
	}
	if report.StrictModelPassPages != 0 || report.ReviewReady97Pages != 0 || report.SoftReviewCandidatePages != 0 || report.PagesNeedingAuditedEdit != 1 || report.PagesRequiringManualEditAfterStall != 0 {
		t.Fatalf("audited reviewer edit counters are wrong: %#v", report)
	}
}

func TestReviewerLearningDeepPromotesRepeatedHelpfulURLRuleAndTracksRuleEffects(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false}]},
		{"url":"https://example.test/topic/c/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		html := `<html><body><main><h1 data-locator="title">Title ` + strings.ToUpper(name) + `</h1><aside data-locator="warning">Observera: critical warning A</aside></main></body></html>`
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-promote",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      filepath.Join(directory, "reports"),
		Model:                &promptSensitiveLearningModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		StallWindow:          3,
		MinImprovement:       0.002,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run deep reviewer learning benchmark: %v", err)
	}
	if report.PagesEverFixedByExistingRules == 0 || report.SourceRulesHelpedCount == 0 {
		t.Fatalf("later pages should be fixed by learned source rules: %#v", report)
	}
	var sourceRule ReviewerLearningRule
	for _, rule := range report.Rules {
		if rule.Scope == RuleScopeSource {
			sourceRule = rule
			break
		}
	}
	if sourceRule.ID == 0 || len(sourceRule.AttemptedOnURLs) == 0 || len(sourceRule.HelpedURLs) == 0 || len(sourceRule.PromotedFromURLRules) == 0 {
		t.Fatalf("rule effect URLs were not tracked: %#v", report.Rules)
	}
}

func TestReviewerLearningDeepCountsClinicalCoverageImprovementAsRuleHelp(t *testing.T) {
	report := ReviewerLearningReport{
		QualityThreshold: 0.98,
		Rules: []ReviewerLearningRule{{
			ID:          1,
			Scope:       RuleScopeSource,
			Status:      "active",
			Instruction: "For this source: label clinical risk boxes as type=critical.",
		}},
	}
	before := BenchmarkMetrics{
		RelevantTextF1: 0.995,
		CriticalRecall: 0,
		TableRecall:    1,
		MediaRecall:    1,
	}
	after := BenchmarkMetrics{
		RelevantTextF1: 0.995,
		CriticalRecall: 1,
		TableRecall:    1,
		MediaRecall:    1,
	}

	report.recordDeepRuleEffects([]SkillRule{{ID: 1, Scope: RuleScopeSource}}, "https://example.test/topic/a/", before, after, 0.002)

	if len(report.Rules[0].HelpedURLs) != 1 || report.Rules[0].HelpedURLs[0] != "https://example.test/topic/a/" {
		t.Fatalf("clinical recall improvement should count as source rule help: %#v", report.Rules[0])
	}
	if len(report.Rules[0].NeutralURLs) != 0 || len(report.Rules[0].HurtURLs) != 0 {
		t.Fatalf("clinical recall improvement should not be neutral or harmful: %#v", report.Rules[0])
	}
}

func TestReviewerLearningDeepPromotesRepeatedCriticalTypeMismatchAsGeneralSourceRule(t *testing.T) {
	report := ReviewerLearningReport{
		Rules: []ReviewerLearningRule{{
			ID:          1,
			Scope:       RuleScopeURL,
			OriginURL:   "https://example.test/topic/a/",
			Instruction: `For this page only: keep the already included highlighted clinical warning/info block but label it as type=critical. Include exact block_id b0042. The text that must be critical is: "Ofri luftväg är ett livshotande tillstånd."`,
			Status:      "active",
		}},
	}
	repeatedRule := ReviewerLearningRule{
		ID:          2,
		Scope:       RuleScopeURL,
		OriginURL:   "https://example.test/topic/b/",
		Instruction: `For this page only: keep the already included highlighted clinical warning/info block but label it as type=critical. Include exact block_id b0017. The text that must be critical is: "Observera sena tecken på försämring."`,
		Status:      "active",
	}

	maybePromoteHelpfulRule(&report, repeatedRule, "critical_type_mismatch", repeatedRule.OriginURL, true)

	if len(report.Rules) != 2 {
		t.Fatalf("expected one promoted source rule, got %#v", report.Rules)
	}
	sourceRule := report.Rules[1]
	if sourceRule.Scope != RuleScopeSource || len(sourceRule.PromotedFromURLRules) != 2 {
		t.Fatalf("expected promoted source rule with both URL rules as evidence: %#v", sourceRule)
	}
	if strings.Contains(sourceRule.Instruction, "Ofri luftväg") ||
		strings.Contains(sourceRule.Instruction, "Observera sena") ||
		strings.Contains(sourceRule.Instruction, "block_id") ||
		strings.Contains(sourceRule.Instruction, "For this page only") {
		t.Fatalf("promoted source rule must be generalized and not carry page-specific snippets: %s", sourceRule.Instruction)
	}
	if !strings.Contains(sourceRule.Instruction, "type=critical") {
		t.Fatalf("promoted critical mismatch rule should teach critical labeling: %s", sourceRule.Instruction)
	}
}

func TestReviewerLearningDeepPromotionMergesDuplicateSourceInstruction(t *testing.T) {
	instruction := promptForEvidence(reviewerLearningPromptEvidence{Category: "critical_type_mismatch", Scope: RuleScopeSource})
	report := ReviewerLearningReport{
		Rules: []ReviewerLearningRule{
			{
				ID:                   1,
				Scope:                RuleScopeSource,
				Instruction:          instruction,
				Status:               "active",
				PromotedFromURLRules: []int{10, 11},
				HelpedURLs:           []string{"https://example.test/topic/a/"},
			},
			{
				ID:          12,
				Scope:       RuleScopeURL,
				OriginURL:   "https://example.test/topic/b/",
				Instruction: `For this page only: keep the already included highlighted clinical warning/info block but label it as type=critical. Include exact block_id b0042. The text that must be critical is: "B".`,
				Status:      "active",
			},
		},
	}
	repeatedRule := ReviewerLearningRule{
		ID:          13,
		Scope:       RuleScopeURL,
		OriginURL:   "https://example.test/topic/c/",
		Instruction: `For this page only: keep the already included highlighted clinical warning/info block but label it as type=critical. Include exact block_id b0017. The text that must be critical is: "C".`,
		Status:      "active",
	}

	maybePromoteHelpfulRule(&report, repeatedRule, "critical_type_mismatch", repeatedRule.OriginURL, true)

	sourceRules := 0
	for _, rule := range report.Rules {
		if rule.Scope != RuleScopeSource {
			continue
		}
		sourceRules++
		if len(rule.HelpedURLs) != 1 || !containsString(rule.HelpedURLs, "https://example.test/topic/a/") {
			t.Fatalf("existing source rule should preserve prior runtime helped URLs only: %#v", rule)
		}
		if !containsInt(rule.PromotedFromURLRules, repeatedRule.ID) || !containsInt(rule.PromotedFromURLRules, 12) {
			t.Fatalf("existing source rule should merge promotion evidence: %#v", rule)
		}
	}
	if sourceRules != 1 {
		t.Fatalf("expected duplicate source instruction to merge, got %#v", report.Rules)
	}
}

func TestReviewerLearningPromotedSourceRuleIsNotCountedHelpedUntilInjectedLater(t *testing.T) {
	existingURLRule := ReviewerLearningRule{
		ID:          1,
		Version:     1,
		Scope:       RuleScopeURL,
		OriginURL:   "https://example.test/topic/a/",
		Instruction: "For this page only: keep the already included highlighted clinical warning/info block but label it as type=critical.",
		Status:      "active",
	}
	repeatedRule := ReviewerLearningRule{
		ID:          2,
		Version:     2,
		Scope:       RuleScopeURL,
		OriginURL:   "https://example.test/topic/b/",
		Instruction: "For this page only: keep the already included highlighted clinical warning/info block but label it as type=critical.",
		Status:      "active",
	}
	report := ReviewerLearningReport{Rules: []ReviewerLearningRule{existingURLRule, repeatedRule}}

	maybePromoteHelpfulRule(&report, repeatedRule, "critical_type_mismatch", repeatedRule.OriginURL, true)

	var sourceRule ReviewerLearningRule
	for _, rule := range report.Rules {
		if rule.Scope == RuleScopeSource {
			sourceRule = rule
			break
		}
	}
	if sourceRule.ID == 0 {
		t.Fatalf("expected repeated URL evidence to promote a source rule: %#v", report.Rules)
	}
	if len(sourceRule.PromotedFromURLRules) != 2 {
		t.Fatalf("promotion evidence should preserve source URL rule ids: %#v", sourceRule)
	}
	if len(sourceRule.HelpedURLs) != 0 || sourceRule.ReuseCount != 0 {
		t.Fatalf("promoted source rule must not count as helped until it is injected into a later page: %#v", sourceRule)
	}
}

func TestReviewerLearningSummaryKeepsHistoricalStableStreakAfterReset(t *testing.T) {
	pass := func(index int, method string, turns int) ReviewerLearningPage {
		return ReviewerLearningPage{
			Index:               index,
			URL:                 fmt.Sprintf("https://example.test/topic/%d/", index),
			BaselineStatus:      BaselineValidGold,
			Expected:            []BenchmarkSection{{BlockID: "body", Role: RoleContent, Text: "Clinical text"}},
			FinalApprovalMethod: method,
			FinalApproved:       ReviewerLearningLayer{Actual: []BenchmarkSection{{BlockID: "body", Role: RoleContent, Text: "Clinical text"}}, Metrics: BenchmarkMetrics{RelevantTextF1: 1, CriticalRecall: 1, TableRecall: 1, MediaRecall: 1}},
			ReviewerTurns:       turns,
			StrictPass:          true,
		}
	}
	report := ReviewerLearningReport{
		QualityThreshold: 0.98,
		Pages: []ReviewerLearningPage{
			pass(1, ReviewerLearningApprovalFirstPass, 0),
			pass(2, ReviewerLearningApprovalFirstPass, 0),
			pass(3, ReviewerLearningApprovalFirstPass, 0),
			pass(4, ReviewerLearningApprovalFirstPass, 0),
			pass(5, ReviewerLearningApprovalFirstPass, 0),
			pass(6, ReviewerLearningApprovalNewReviewerPrompt, 1),
		},
	}

	report.updateReviewerLearningSummary()

	if report.CurrentStableStreak != 0 || report.StableStreakStartPage != 0 {
		t.Fatalf("current stable streak should reset after prompted page: current=%d start=%d", report.CurrentStableStreak, report.StableStreakStartPage)
	}
	if report.MaxStableStreak != 5 || report.MaxStableStreakStartPage != 1 || report.MaxStableStreakEndPage != 5 {
		t.Fatalf("historical stable streak should preserve previous pass/pass/pass run: max=%d start=%d end=%d", report.MaxStableStreak, report.MaxStableStreakStartPage, report.MaxStableStreakEndPage)
	}
}

func TestReviewerLearningStatusIncludesSourceRuleEffectCounts(t *testing.T) {
	directory := t.TempDir()
	if err := WriteReviewerLearningReports(directory, ReviewerLearningReport{
		ImplementationVersion: ReviewerLearningImplementationVersion,
		RunStatus:             ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs:          1,
		Rules: []ReviewerLearningRule{{
			ID:          1,
			Scope:       RuleScopeSource,
			Status:      "active",
			Instruction: "source rule",
			HelpedURLs:  []string{"https://example.test/a/"},
			HurtURLs:    []string{"https://example.test/b/"},
			NeutralURLs: []string{"https://example.test/c/"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	status, err := LoadReviewerLearningStatus(directory)
	if err != nil {
		t.Fatal(err)
	}

	if status.SourceRulesHelpedCount != 1 || status.SourceRulesHurtCount != 1 || status.SourceRulesNeutralCount != 1 {
		t.Fatalf("status should expose source rule effect counts: %#v", status)
	}
}

func TestReviewerLearningRecordRuleReuseDeduplicatesHelpedURLs(t *testing.T) {
	report := ReviewerLearningReport{
		Rules: []ReviewerLearningRule{{
			ID:          7,
			Scope:       RuleScopeSource,
			Status:      "active",
			Instruction: "source rule",
			HelpedURLs:  []string{"https://example.test/topic/a/"},
			ReuseCount:  1,
		}},
	}
	active := []SkillRule{{ID: 7, Scope: RuleScopeSource, Instruction: "source rule"}}

	report.recordRuleReuse(active, "https://example.test/topic/a/")
	report.recordRuleReuse(active, "https://example.test/topic/b/")

	if report.Rules[0].ReuseCount != 2 {
		t.Fatalf("reuse count should track unique helped URLs, got %#v", report.Rules[0])
	}
	if len(report.Rules[0].HelpedURLs) != 2 {
		t.Fatalf("helped URLs should be unique, got %#v", report.Rules[0].HelpedURLs)
	}
}

func TestReviewerLearningRuleEffectBucketsAreMutuallyExclusive(t *testing.T) {
	report := ReviewerLearningReport{
		QualityThreshold: 0.98,
		Rules: []ReviewerLearningRule{{
			ID:          1,
			Scope:       RuleScopeSource,
			Status:      "active",
			Instruction: "source rule",
			NeutralURLs: []string{"https://example.test/topic/a/"},
			HurtURLs:    []string{"https://example.test/topic/a/"},
		}},
	}
	before := BenchmarkMetrics{RelevantTextF1: 0.99, CriticalRecall: 0, TableRecall: 1, MediaRecall: 1}
	after := BenchmarkMetrics{RelevantTextF1: 0.99, CriticalRecall: 1, TableRecall: 1, MediaRecall: 1}

	report.recordDeepRuleEffects([]SkillRule{{ID: 1, Scope: RuleScopeSource}}, "https://example.test/topic/a/", before, after, 0.002)

	rule := report.Rules[0]
	if !containsString(rule.HelpedURLs, "https://example.test/topic/a/") {
		t.Fatalf("expected helped URL: %#v", rule)
	}
	if containsString(rule.NeutralURLs, "https://example.test/topic/a/") || containsString(rule.HurtURLs, "https://example.test/topic/a/") {
		t.Fatalf("helped URL should not remain in neutral/hurt buckets: %#v", rule)
	}
}

func TestReviewerLearningRepairMergesDuplicateSourceRules(t *testing.T) {
	rules := []ReviewerLearningRule{
		{
			ID:                   1,
			Scope:                RuleScopeSource,
			Instruction:          "same source rule",
			Status:               "active",
			HelpedURLs:           []string{"https://example.test/a/"},
			PromotedFromURLRules: []int{10},
		},
		{
			ID:                   2,
			Scope:                RuleScopeSource,
			Instruction:          "same source rule",
			Status:               "active",
			HelpedURLs:           []string{"https://example.test/b/"},
			NeutralURLs:          []string{"https://example.test/c/"},
			PromotedFromURLRules: []int{11},
		},
		{
			ID:          3,
			Scope:       RuleScopeURL,
			Instruction: "url rule",
			Status:      "active",
		},
	}

	merged := mergeDuplicateReviewerLearningSourceRules(rules)

	if len(merged) != 2 {
		t.Fatalf("expected duplicate source rules to merge while URL rule remains, got %#v", merged)
	}
	source := merged[0]
	if source.ID != 1 || source.ReuseCount != 2 {
		t.Fatalf("first source rule should be retained and reuse count recomputed: %#v", source)
	}
	if !containsString(source.HelpedURLs, "https://example.test/a/") || !containsString(source.HelpedURLs, "https://example.test/b/") {
		t.Fatalf("source helped URLs should merge: %#v", source)
	}
	if !containsString(source.NeutralURLs, "https://example.test/c/") {
		t.Fatalf("source neutral URLs should merge: %#v", source)
	}
	if !containsInt(source.PromotedFromURLRules, 10) || !containsInt(source.PromotedFromURLRules, 11) {
		t.Fatalf("promotion evidence should merge: %#v", source)
	}
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func containsInt(values []int, value int) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func TestReviewerLearningDeepMarksProofIncompleteWhenFetchCoverageFails(t *testing.T) {
	directory := t.TempDir()
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "deep-fetch-incomplete",
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Model:            &learningModel{},
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		Fetcher:          mapFetcher{"https://example.test/topic/a/": `<html><body><main><h1>A</h1></main></body></html>`},
		LiveFetch:        true,
		QualityThreshold: 0.98,
		LockTimeout:      time.Minute,
	})
	if err != nil {
		t.Fatalf("run deep reviewer learning benchmark: %v", err)
	}
	if report.RunStatus != ReviewerLearningRunStatusIncompleteFetchFailed || report.FetchFailedPages != 1 || report.ProofUsable {
		t.Fatalf("fetch failures should make proof incomplete and not usable: %#v", report)
	}
	if report.Pages[1].BaselineStatus != BaselineFetchFailed || report.Pages[1].FetchError == "" {
		t.Fatalf("fetch failed page should retain error detail: %#v", report.Pages[1])
	}
	status, err := LoadReviewerLearningStatus(filepath.Join(directory, "reports"))
	if err != nil {
		t.Fatal(err)
	}
	if status.RunStatus != ReviewerLearningRunStatusIncompleteFetchFailed || status.FetchFailedPages != 1 || status.ProofUsable {
		t.Fatalf("status should expose incomplete proof: %#v", status)
	}
}

func TestReviewerLearningSourcePromptOmitsPageSpecificSnippet(t *testing.T) {
	warningPrompt := promptForEvidence(reviewerLearningPromptEvidence{
		Category: "missing_warning_info_box",
		Snippet:  "Ofri luftväg är ett livshotande tillstånd from one exact page",
		Scope:    RuleScopeSource,
		Risky:    true,
	})
	if strings.Contains(warningPrompt, "Ofri luftväg") || strings.Contains(warningPrompt, "Include source blocks containing") {
		t.Fatalf("source-scoped prompts must be generalized, not page-snippet-specific: %s", warningPrompt)
	}
	if !strings.Contains(warningPrompt, "keep highlighted warning/info boxes") {
		t.Fatalf("source-scoped prompt should keep reusable reviewer lesson: %s", warningPrompt)
	}

	boilerplatePrompt := promptForEvidence(reviewerLearningPromptEvidence{
		Category: "extra_boilerplate",
		Snippet:  "Läs mer om trakeostomi i Sample Sources text om trakeostomi.",
		Scope:    RuleScopeSource,
	})
	if strings.Contains(boilerplatePrompt, "Läs mer om trakeostomi") || strings.Contains(boilerplatePrompt, "Extra text pattern") {
		t.Fatalf("source-scoped boilerplate prompt should generalize exact extra text: %s", boilerplatePrompt)
	}
	if !strings.Contains(boilerplatePrompt, "share, print") || !strings.Contains(boilerplatePrompt, "related-card UI") {
		t.Fatalf("source-scoped boilerplate prompt should keep reusable drop guidance: %s", boilerplatePrompt)
	}
}

func TestReviewerLearningBlockHintsIgnoreTinyPartialFragments(t *testing.T) {
	expected := BenchmarkSection{
		Text: "Säkerhet Bedöm alltid säkerheten initialt om personen befinner sig utanför sjukhus eller annan vårdenhet. Använd tillgänglig skyddsutrustning.",
	}
	blocks := []CandidateBlock{
		{ID: "b0009", Text: "Säkerhet"},
		{ID: "b0010", Text: "Bedöm alltid säkerheten initialt om personen befinner sig utanför sjukhus eller annan vårdenhet."},
		{ID: "b0011", Text: "Använd tillgänglig skyddsutrustning."},
	}

	ids := matchingCandidateBlockIDs(expected, blocks)
	if strings.Contains(strings.Join(ids, ","), "b0009") {
		t.Fatalf("short heading fragment should not be treated as exact block hint: %#v", ids)
	}
	if len(ids) != 2 || ids[0] != "b0010" || ids[1] != "b0011" {
		t.Fatalf("expected substantive partial blocks, got %#v", ids)
	}
}

func TestReviewerLearningBlockHintsIgnoreLowCoverageFragments(t *testing.T) {
	expected := BenchmarkSection{
		Text: "Prioritera alltid din egen säkerhet så att du inte hamnar i en situation där du själv utsätts för fara. Använd tillgänglig skyddsutrustning, till exempel skyddshandskar, visir, munskydd och förkläde.",
	}
	blocks := []CandidateBlock{
		{ID: "b0009", Text: "Prioritera alltid din egen säkerhet"},
		{ID: "b0010", Text: "Prioritera alltid din egen säkerhet så att du inte hamnar i en situation där du själv utsätts för fara. Använd tillgänglig skyddsutrustning, till exempel skyddshandskar, visir, munskydd och förkläde."},
	}

	ids := matchingCandidateBlockIDs(expected, blocks)
	if len(ids) != 1 || ids[0] != "b0010" {
		t.Fatalf("expected only the high-coverage warning block, got %#v", ids)
	}
}

func TestHarmfulSourceRulesAreNotInjectedIntoFuturePrompts(t *testing.T) {
	rules := []ReviewerLearningRule{
		{ID: 1, Version: 1, Scope: RuleScopeSource, Status: "active", Instruction: "bad source rule", HurtURLs: []string{"a", "b"}, HelpedURLs: []string{"c"}},
		{ID: 2, Version: 2, Scope: RuleScopeSource, Status: "active", Instruction: "good source rule", HelpedURLs: []string{"a"}},
		{ID: 3, Version: 3, Scope: RuleScopeSource, Status: "active", Instruction: "bad source rule", NeutralURLs: []string{"d"}},
		{ID: 4, Version: 4, Scope: RuleScopeSource, Status: "active", Instruction: "good source rule", NeutralURLs: []string{"d"}},
	}
	active := applicableLearningRules(rules, "https://example.test/next", true)
	if len(active) != 1 || active[0].Instruction != "good source rule" {
		t.Fatalf("harmful and duplicate source rules should be suppressed, got %#v", active)
	}
}

func TestUsedReviewerLearningPromptsIncludesSuppressedSourceButOnlyCurrentURLRules(t *testing.T) {
	rules := []ReviewerLearningRule{
		{ID: 1, Scope: RuleScopeSource, Status: "active", Instruction: "source warning rule", HurtURLs: []string{"a"}},
		{ID: 2, Scope: RuleScopeURL, OriginURL: "https://example.test/current", Status: "active", Instruction: "current page correction"},
		{ID: 3, Scope: RuleScopeURL, OriginURL: "https://example.test/other", Status: "active", Instruction: "other page correction"},
		{ID: 4, Scope: RuleScopeSource, Status: "disabled", Instruction: "disabled source rule"},
	}
	used := usedReviewerLearningPrompts(rules, "https://example.test/current")
	if !used["source warning rule"] || !used["current page correction"] {
		t.Fatalf("used prompts should include source rules and current URL rules: %#v", used)
	}
	if used["other page correction"] || used["disabled source rule"] {
		t.Fatalf("used prompts should not include unrelated URL or disabled rules: %#v", used)
	}
}

func TestReviewerLearningDeepLockPreventsOverlappingWriters(t *testing.T) {
	directory := t.TempDir()
	reportDirectory := filepath.Join(directory, "reports")
	if err := os.MkdirAll(reportDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireReviewerLearningLock(reportDirectory, "active", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseReviewerLearningLock(reportDirectory, lock)
	_, err = acquireReviewerLearningLock(reportDirectory, "new", time.Hour, false)
	if err == nil {
		t.Fatal("expected active deep benchmark lock to reject overlapping writer")
	}
	status, err := LoadReviewerLearningStatus(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Locked || status.ActiveRunID != "active" {
		t.Fatalf("status should expose active lock: %#v", status)
	}
}

func TestReviewerLearningStatusReportsInterruptedWhenRunningReportHasNoLock(t *testing.T) {
	reportDirectory := t.TempDir()
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:     "interrupted",
		RunStatus:      ReviewerLearningRunStatusRunning,
		LearningMode:   ReviewerLearningModeDeep,
		ExpectedURLs:   2,
		DiscoveredURLs: 1,
		Pages: []ReviewerLearningPage{{
			Index:               1,
			URL:                 "https://example.test/a/",
			BaselineStatus:      BaselineValidGold,
			FinalApprovalMethod: ReviewerLearningApprovalFirstPass,
			FinalApproved:       ReviewerLearningLayer{Metrics: BenchmarkMetrics{RelevantTextF1: 1}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	status, err := LoadReviewerLearningStatus(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if status.RunStatus != ReviewerLearningRunStatusInterrupted || status.Locked {
		t.Fatalf("running report without lock should be reported as interrupted, got %#v", status)
	}
	if status.ProofUsable || status.ProofBlockedReason == "" {
		t.Fatalf("interrupted status should not be usable proof: %#v", status)
	}
}

func TestReviewerLearningSummaryExcludesInProgressPageFromFinalProof(t *testing.T) {
	expectedA := []BenchmarkSection{{BlockID: "a", Text: "Completed clinical section"}}
	actualA := []BenchmarkSection{{BlockID: "a", Text: "Completed clinical section"}}
	expectedB := []BenchmarkSection{{BlockID: "b", Text: "Still running clinical section"}}
	actualB := []BenchmarkSection{{BlockID: "b", Text: "Wrong partial model output"}}
	report := ReviewerLearningReport{
		RunStatus:            ReviewerLearningRunStatusRunning,
		ExpectedURLs:         2,
		QualityThreshold:     0.98,
		FixtureSet:           "interrupted-summary",
		ModelMode:            "openai",
		ReviewReadyThreshold: 0.97,
		Pages: []ReviewerLearningPage{
			{
				Index:               1,
				URL:                 "https://example.test/a/",
				BaselineStatus:      BaselineValidGold,
				RawExpected:         expectedA,
				Expected:            expectedA,
				FinalApprovalMethod: ReviewerLearningApprovalFirstPass,
				FinalApproved: ReviewerLearningLayer{
					Actual:  actualA,
					Metrics: EvaluateBenchmark(expectedA, actualA),
				},
			},
			{
				Index:          2,
				URL:            "https://example.test/b/",
				BaselineStatus: BaselineValidGold,
				RawExpected:    expectedB,
				Expected:       expectedB,
				ReviewerTurns:  3,
				AfterNewReviewerPrompt: ReviewerLearningLayer{
					Actual:  actualB,
					Metrics: EvaluateBenchmark(expectedB, actualB),
				},
			},
		},
	}
	report.updateReviewerLearningSummary()

	if report.LastCompletedPage != 1 {
		t.Fatalf("in-progress page should not advance last completed page: %#v", report)
	}
	if report.FinalApprovedAggregate.RelevantTextF1 != 1 {
		t.Fatalf("in-progress page should not poison final aggregate: %#v", report.FinalApprovedAggregate)
	}
	if report.RawSelectorMetrics.RelevantTextF1 != 1 || report.SemanticGoldMetrics.RelevantTextF1 != 1 {
		t.Fatalf("raw and semantic final metrics should reflect only completed proof pages: raw=%#v semantic=%#v", report.RawSelectorMetrics, report.SemanticGoldMetrics)
	}
	if report.TotalReviewerPrompts != 3 || report.TotalReviewerTurns != 3 {
		t.Fatalf("partial reviewer turns should remain visible as spent work: %#v", report)
	}
	if report.ProofUsable || report.ProofBlockedReason != "run is not complete" {
		t.Fatalf("running report should not be usable proof: %#v", report)
	}
}

func TestReviewerLearningDeepModelErrorCheckpointsAndStopsWithoutAuditedSuccess(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	reportDirectory := filepath.Join(directory, "reports")

	_, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "deep-model-error",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &failFirstExtractModel{},
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		QualityThreshold: 0.98,
		LockTimeout:      time.Minute,
	})
	if err == nil || !strings.Contains(err.Error(), "forced model timeout") {
		t.Fatalf("deep benchmark should stop on model transport failure, got %v", err)
	}
	status, statusErr := LoadReviewerLearningStatus(reportDirectory)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.RunStatus != ReviewerLearningRunStatusInterrupted || status.PagesCompleted != 0 || status.ProofUsable {
		t.Fatalf("model-error checkpoint should be interrupted and not usable proof: %#v", status)
	}
	report, loadErr := LoadReviewerLearningReport(reportDirectory)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(report.Pages) != 1 || report.Pages[0].FinalApprovalMethod != "" || report.Pages[0].FirstPass.ModelError == "" {
		t.Fatalf("partial page should be checkpointed without final approval: %#v", report.Pages)
	}
}

func TestReviewerLearningDeepDoesNotAbortOnReviewerTurnModelError(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"body","role":"content","container":"p.body","parse_mode":true}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1>Title A</h1><p class="body">Important clinical paragraph A</p></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-reviewer-turn-model-error",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      filepath.Join(directory, "reports"),
		Model:                &failReviewerTurnModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		StallWindow:          1,
		MinImprovement:       0.002,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("reviewer-turn model error should not abort the benchmark: %v", err)
	}
	if report.RunStatus != ReviewerLearningRunStatusCompleted || len(report.Pages) != 1 {
		t.Fatalf("benchmark should complete the page with an audited fallback: %#v", report)
	}
	page := report.Pages[0]
	if page.FinalApprovalMethod != ReviewerLearningApprovalAuditedBlockSelect {
		t.Fatalf("reviewer-turn model failure should be labeled as source-evidenced audited reviewer edit when source blocks are complete: %#v", page)
	}
	if page.AfterNewReviewerPrompt.ModelError == "" || !strings.Contains(page.AfterNewReviewerPrompt.ModelError, "no structured content") {
		t.Fatalf("failed reviewer turn should be visible on the page layer: %#v", page.AfterNewReviewerPrompt)
	}
	if len(page.ReviewerTurnDetails) == 0 || page.ReviewerTurnDetails[0].ModelError == "" {
		t.Fatalf("failed reviewer turn should be visible in the turn transcript: %#v", page.ReviewerTurnDetails)
	}
	if !page.FinalApproved.Metrics.ExactParity || page.FinalApproved.Metrics.RelevantTextF1 != 1 {
		t.Fatalf("audited fallback should still make final approved parity explicit: %#v", page.FinalApproved.Metrics)
	}
	if len(page.AuditedEditOperations) == 0 {
		t.Fatalf("audited fallback should record reviewer edit operations: %#v", page)
	}
	if report.StrictModelPassPages != 0 || report.PagesRequiringManualEditAfterStall != 0 || report.PagesNeedingAuditedEdit != 1 {
		t.Fatalf("model error fallback must not be counted as model success: %#v", report)
	}
}

func TestReviewerLearningDeepResumeDropsModelErrorPartialPageAndReruns(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"body","role":"content","container":"p.body","parse_mode":true}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1>Title A</h1><p class="body">Important clinical paragraph A</p></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	reportDirectory := filepath.Join(directory, "reports")
	_, firstErr := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "deep-model-error-resume",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &failFirstExtractModel{},
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		QualityThreshold: 0.98,
		LockTimeout:      time.Minute,
	})
	if firstErr == nil {
		t.Fatal("expected initial model failure")
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-model-error-resume",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      reportDirectory,
		Model:                &blockIDReviewerModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		Resume:               true,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("resume should rerun dropped partial page: %v", err)
	}
	if report.RunStatus != ReviewerLearningRunStatusCompleted || len(report.Pages) != 1 || report.LastCompletedPage != 1 {
		t.Fatalf("resume should complete the page after dropping partial checkpoint: %#v", report)
	}
	if report.Pages[0].FirstPass.ModelError != "" {
		t.Fatalf("partial failed page should not be preserved after successful resume: %#v", report.Pages[0])
	}
}

func TestReviewerLearningDeepResumeDropsInvalidFetchFailedPlaceholders(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/c/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		html := `<html><body><main><h1>Title ` + strings.ToUpper(name) + `</h1></main></body></html>`
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reportDirectory := filepath.Join(directory, "reports")
	initial, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet: "deep-placeholder-resume", FixtureDirectory: fixtureDirectory,
		MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: reportDirectory,
		Model: &learningModel{}, ModelMode: "openai", LearningMode: ReviewerLearningModeDeep,
		QualityThreshold: 0.98, ReviewReadyThreshold: 0.97, LockTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	initial.Pages[1] = ReviewerLearningPage{Index: 2, URL: "https://example.test/topic/b/", BaselineStatus: BaselineFetchFailed}
	initial.Pages[2] = ReviewerLearningPage{Index: 3, URL: "https://example.test/topic/c/", BaselineStatus: BaselineFetchFailed}
	if err := WriteReviewerLearningReports(reportDirectory, initial); err != nil {
		t.Fatal(err)
	}
	status, err := LoadReviewerLearningStatus(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if status.RunStatus != ReviewerLearningRunStatusInterrupted || status.PagesCompleted != 1 || status.FetchFailedPages != 0 || status.ProofUsable {
		t.Fatalf("invalid fetch_failed placeholders should not count as completed fetch failures: %#v", status)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-placeholder-resume",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      reportDirectory,
		Model:                &learningModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		Resume:               true,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("resume should drop invalid placeholder suffix and continue: %v", err)
	}
	if report.RunStatus != ReviewerLearningRunStatusCompleted || len(report.Pages) != 3 || report.LastCompletedPage != 3 || report.FetchFailedPages != 0 {
		t.Fatalf("resume should complete from the last valid page, got %#v", report)
	}
}

func TestRepairReviewerLearningReportNormalizesInterruptedPlaceholderSuffix(t *testing.T) {
	reportDirectory := t.TempDir()
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:     "deep-placeholder-repair",
		ModelMode:      "openai",
		LearningMode:   ReviewerLearningModeDeep,
		RunStatus:      ReviewerLearningRunStatusCompleted,
		ExpectedURLs:   3,
		DiscoveredURLs: 3,
		MissingURLs: []string{
			"https://example.test/topic/b/",
			"https://example.test/topic/c/",
		},
		Pages: []ReviewerLearningPage{
			{
				Index:               1,
				URL:                 "https://example.test/topic/a/",
				BaselineStatus:      BaselineValidGold,
				Expected:            []BenchmarkSection{{BlockID: "title", Text: "Title A"}},
				FinalApprovalMethod: ReviewerLearningApprovalFirstPass,
				FinalApproved:       ReviewerLearningLayer{Actual: []BenchmarkSection{{BlockID: "title", Text: "Title A"}}, Metrics: BenchmarkMetrics{RelevantTextF1: 1, ExactParity: true}},
			},
			{Index: 2, URL: "https://example.test/topic/b/", BaselineStatus: BaselineFetchFailed},
			{Index: 3, URL: "https://example.test/topic/c/", BaselineStatus: BaselineFetchFailed},
		},
		Rules: []ReviewerLearningRule{
			{ID: 1, Scope: RuleScopeSource, Status: "active", CreatedOnPage: 1, Instruction: "For this source: useful completed rule."},
			{ID: 2, Scope: RuleScopeURL, Status: "active", CreatedOnPage: 3, Instruction: "For this page only: incomplete placeholder rule."},
		},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.RunStatus != ReviewerLearningRunStatusInterrupted || len(repaired.Pages) != 1 || repaired.LastCompletedPage != 1 {
		t.Fatalf("repair should keep only completed prefix and mark report interrupted: %#v", repaired)
	}
	if repaired.FetchFailedPages != 0 || len(repaired.MissingURLs) != 0 || repaired.DiscoveredURLs != 1 {
		t.Fatalf("repair should recompute coverage from completed prefix: %#v", repaired)
	}
	if len(repaired.Rules) != 1 || repaired.Rules[0].CreatedOnPage != 1 {
		t.Fatalf("repair should drop rules created after the completed prefix: %#v", repaired.Rules)
	}
	loaded, err := LoadReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunStatus != ReviewerLearningRunStatusInterrupted || len(loaded.Pages) != 1 || loaded.FetchFailedPages != 0 {
		t.Fatalf("repair should rewrite raw JSON artifact, got %#v", loaded)
	}
	status, err := LoadReviewerLearningStatus(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if status.RunStatus != ReviewerLearningRunStatusInterrupted || status.PagesCompleted != 1 || status.FetchFailedPages != 0 || status.ProofUsable {
		t.Fatalf("repair should rewrite consistent status artifact: %#v", status)
	}
}

func TestRepairReviewerLearningReportUpgradesLegacyNonExactAuditedBlockSelection(t *testing.T) {
	reportDirectory := t.TempDir()
	expected := []BenchmarkSection{{BlockID: "title", Text: "Title A"}, {BlockID: "body", Text: "Important clinical text."}}
	legacyActual := []BenchmarkSection{{BlockID: "title", Text: "Title A"}}
	legacyMetrics := EvaluateBenchmark(expected, legacyActual)
	if legacyMetrics.ExactParity {
		t.Fatal("test setup expected non-exact legacy metrics")
	}
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:   "deep-legacy-audited-repair",
		ModelMode:    "openai",
		LearningMode: ReviewerLearningModeDeep,
		RunStatus:    ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs: 2,
		Pages: []ReviewerLearningPage{
			{
				Index:               1,
				URL:                 "https://example.test/topic/a/",
				BaselineStatus:      BaselineValidGold,
				Expected:            expected,
				RawExpected:         expected,
				FinalApprovalMethod: ReviewerLearningApprovalAuditedBlockSelect,
				FinalApproved:       ReviewerLearningLayer{Actual: legacyActual, Metrics: legacyMetrics},
				ReviewerTurns:       3,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.RunStatus != ReviewerLearningRunStatusPausedChunk {
		t.Fatalf("repair should preserve paused chunk status, got %q", repaired.RunStatus)
	}
	if len(repaired.Pages) != 1 {
		t.Fatalf("expected one repaired page, got %#v", repaired.Pages)
	}
	page := repaired.Pages[0]
	if page.FinalApprovalMethod != ReviewerLearningApprovalStalledHumanEdit || !page.ManualEditRequired || !page.Stalled {
		t.Fatalf("legacy non-exact block selection should be relabeled as audited human edit after stall: %#v", page)
	}
	if !page.FinalApproved.Metrics.ExactParity || page.FinalApproved.Metrics.RelevantTextF1 != 1 || len(page.FinalApproved.Actual) != len(expected) {
		t.Fatalf("repair should replace final approved layer with exact audited gold: %#v", page.FinalApproved)
	}
	if repaired.FinalApprovedAggregate.RelevantTextF1 != 1 || !repaired.FinalApprovedAggregate.ExactParity || repaired.PagesRequiringManualEditAfterStall != 1 {
		t.Fatalf("repair should recompute exact final audited metrics and counters: %#v", repaired)
	}
	loaded, err := LoadReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Pages[0].FinalApprovalMethod != ReviewerLearningApprovalStalledHumanEdit || !loaded.Pages[0].FinalApproved.Metrics.ExactParity {
		t.Fatalf("repair should persist upgraded final approval: %#v", loaded.Pages[0])
	}
}

func TestAuditedReviewerEditLayerRecordsReviewerOperationsFromSourceEvidence(t *testing.T) {
	blocks := []CandidateBlock{
		{ID: "h1", Tag: "h1", Text: "Title A"},
		{ID: "warn", Tag: "aside", Text: "Observera risk for clinical deterioration."},
		{ID: "table", Tag: "table", Text: "Dos 1 mg Tid 08:00"},
		{ID: "share", Tag: "p", Text: "Tipsa & dela Kopiera länk"},
	}
	gold := []BenchmarkSection{
		{BlockID: "h1", Role: RoleHeader, Type: TypeNone, ParseMode: true, Text: "Title A"},
		{BlockID: "warn", Role: RoleInfoBox, Type: TypeCritical, ParseMode: false, Text: "Observera risk for clinical deterioration."},
		{BlockID: "table", Role: RoleTable, Type: TypeNone, ParseMode: true, Text: "Dos 1 mg Tid 08:00"},
	}
	actual := []BenchmarkSection{
		{BlockID: "h1", Role: RoleHeader, Type: TypeNone, ParseMode: true, Text: "Title A"},
		{BlockID: "warn", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Observera risk for clinical deterioration."},
		{BlockID: "share", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: "Tipsa & dela Kopiera länk"},
	}

	layer, operations, sourceEvidenced := auditedReviewerEditLayer(blocks, gold, actual)

	if !sourceEvidenced {
		t.Fatalf("all expected sections have source evidence, operations=%#v", operations)
	}
	if !layer.Metrics.ExactParity || layer.Metrics.RelevantTextF1 != 1 {
		t.Fatalf("audited reviewer edit should reach exact semantic parity from source evidence: %#v", layer)
	}
	if len(layer.Actual) != len(gold) {
		t.Fatalf("audited reviewer edit should drop extra non-gold sections: %#v", layer.Actual)
	}
	assertOperation := func(operation, blockID string) {
		t.Helper()
		for _, op := range operations {
			if op.Operation == operation && op.BlockID == blockID {
				return
			}
		}
		t.Fatalf("missing operation %s for %s in %#v", operation, blockID, operations)
	}
	assertOperation("keep", "h1")
	assertOperation("correct_metadata", "warn")
	assertOperation("include_from_source_block", "table")
	assertOperation("drop_extra", "share")
}

func TestReviewerLearningPromptTranscriptIncludesAuditedEditOperations(t *testing.T) {
	report := ReviewerLearningReport{
		Pages: []ReviewerLearningPage{{
			Index:               1,
			URL:                 "https://example.test/topic/a/",
			FinalApprovalMethod: ReviewerLearningApprovalAuditedBlockSelect,
			AuditedEditOperations: []ReviewerLearningAuditedEditOperation{
				{Operation: "include_from_source_block", BlockID: "table", Reason: "reviewer added a missing source table"},
				{Operation: "drop_extra", BlockID: "share", Reason: "reviewer removed non-clinical UI"},
			},
		}},
	}

	markdown := reviewerLearningPromptsMarkdown(report)

	if !strings.Contains(markdown, "Audited reviewer edit operations") ||
		!strings.Contains(markdown, "`include_from_source_block`") ||
		!strings.Contains(markdown, "`drop_extra`") {
		t.Fatalf("prompt transcript should explain audited edit operations:\n%s", markdown)
	}
}

func TestReviewerLearningSnippetTrimsTruncatedWhitespace(t *testing.T) {
	snippet := reviewerLearningSnippet(strings.Repeat("a", 119) + " " + strings.Repeat("b", 30))
	if strings.HasSuffix(snippet, " ") {
		t.Fatalf("truncated snippet should not leave trailing whitespace: %q", snippet)
	}
}

func TestRepairReviewerLearningReportRecomputesStoredLayerMetrics(t *testing.T) {
	reportDirectory := t.TempDir()
	expected := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Text: "Clinical text."}}
	actual := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Text: "Clinical text [12,13]."}}
	stale := BenchmarkMetrics{RelevantTextF1: 0.5, RelevantTextPrecision: 0.5, RelevantTextRecall: 0.5}
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:   "deep-recompute-metrics",
		ModelMode:    "openai",
		LearningMode: ReviewerLearningModeDeep,
		RunStatus:    ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs: 1,
		Pages: []ReviewerLearningPage{{
			Index:                   1,
			URL:                     "https://example.test/topic/a/",
			BaselineStatus:          BaselineValidGold,
			Expected:                expected,
			RawExpected:             expected,
			FirstPass:               ReviewerLearningLayer{Actual: actual, Metrics: stale},
			WithExistingRules:       ReviewerLearningLayer{Actual: actual, Metrics: stale},
			AfterNewReviewerPrompt:  ReviewerLearningLayer{Actual: actual, Metrics: stale},
			FinalApproved:           ReviewerLearningLayer{Actual: actual, Metrics: stale},
			FinalApprovalMethod:     ReviewerLearningApprovalFirstPass,
			ReviewerTurns:           1,
			ReviewReady:             false,
			StrictPass:              false,
			ManualEditRequired:      true,
			TurnsUntilReviewReady:   0,
			TurnsUntilStrictPass:    0,
			ReviewerTurnDetails:     []ReviewerLearningTurn{{Turn: 1, BeforeMetrics: stale, AfterMetrics: stale}},
			RawSelectorMetrics:      stale,
			SemanticGoldMetrics:     stale,
			SoftReviewCandidate:     false,
			FixedByExistingRules:    false,
			AutonomousAfterLearning: false,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	page := repaired.Pages[0]
	if page.FinalApproved.Metrics.RelevantTextF1 != 1 || page.FirstPass.Metrics.RelevantTextF1 != 1 || page.WithExistingRules.Metrics.RelevantTextF1 != 1 {
		t.Fatalf("repair should recompute model/final layer metrics with current evaluator: %#v", page)
	}
	if !page.StrictPass || !page.ReviewReady || page.ManualEditRequired {
		t.Fatalf("repair should refresh derived pass/manual flags from recomputed metrics: %#v", page)
	}
	if page.ReviewerTurns != 0 || len(page.ReviewerTurnDetails) != 0 || page.AfterNewReviewerPrompt.Metrics.RelevantTextF1 != 0 {
		t.Fatalf("repair should clear obsolete prompt-layer data on first-pass success: %#v", page)
	}
}

func TestRepairReviewerLearningReportDoesNotDowngradeAuditedFallbackWhenModelLayerIsOnlyReviewReady(t *testing.T) {
	reportDirectory := t.TempDir()
	expectedWords := make([]string, 0, 80)
	for index := 0; index < 80; index++ {
		expectedWords = append(expectedWords, fmt.Sprintf("clinical%d", index))
	}
	expectedText := strings.Join(expectedWords, " ")
	expected := []BenchmarkSection{{BlockID: "body", Text: expectedText}}
	modelActual := []BenchmarkSection{{BlockID: "body", Text: expectedText + "harmless extra context."}}
	modelMetrics := EvaluateBenchmark(expected, modelActual)
	if !reviewerLearningPasses(modelMetrics, 0.97) || reviewerLearningPasses(modelMetrics, 0.98) {
		t.Fatalf("test setup expected review-ready but not strict metrics: %#v", modelMetrics)
	}
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:       "deep-audited-downgrade",
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		RunStatus:        ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs:     1,
		QualityThreshold: 0.98,
		Pages: []ReviewerLearningPage{{
			Index:                   1,
			URL:                     "https://example.test/topic/a/",
			BaselineStatus:          BaselineValidGold,
			Expected:                expected,
			FirstPass:               ReviewerLearningLayer{Actual: modelActual, Metrics: modelMetrics},
			WithExistingRules:       ReviewerLearningLayer{Actual: modelActual, Metrics: modelMetrics},
			AfterNewReviewerPrompt:  ReviewerLearningLayer{Actual: modelActual, Metrics: modelMetrics},
			FinalApproved:           auditedHumanEditLayer(expected),
			FinalApprovalMethod:     ReviewerLearningApprovalStalledHumanEdit,
			ManualEditRequired:      true,
			Stalled:                 true,
			ReviewerTurns:           3,
			ReviewerTurnDetails:     []ReviewerLearningTurn{{Turn: 1}},
			StrictPass:              false,
			ReviewReady:             false,
			SoftReviewCandidate:     false,
			FixedByExistingRules:    false,
			AutonomousAfterLearning: false,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	page := repaired.Pages[0]
	if page.FinalApprovalMethod != ReviewerLearningApprovalStalledHumanEdit || !page.ManualEditRequired || !page.Stalled {
		t.Fatalf("audited fallback should stay audited when model layer is only review-ready, got %#v", page)
	}
	if page.FinalApproved.Metrics.RelevantTextF1 != 1 || repaired.PagesNeedingAuditedEdit != 1 || repaired.PagesRequiringHumanConfirmation != 0 {
		t.Fatalf("repair should count this as audited edit, not human confirmation: page=%#v report=%#v", page, repaired)
	}
}

func TestRepairReviewerLearningReportRestoresAuditedBlockSelectionFromOperations(t *testing.T) {
	reportDirectory := t.TempDir()
	terms := make([]string, 0, 80)
	for index := 0; index < 80; index++ {
		terms = append(terms, fmt.Sprintf("clinical%d", index))
	}
	expectedText := strings.Join(terms, " ")
	expected := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: expectedText}}
	modelActual := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Type: TypeNone, ParseMode: true, Text: expectedText + " harmless extra context words"}}
	modelMetrics := EvaluateBenchmark(expected, modelActual)
	if !reviewerLearningPasses(modelMetrics, 0.97) || reviewerLearningPasses(modelMetrics, 0.98) {
		t.Fatalf("test setup expected review-ready but not strict metrics: %#v", modelMetrics)
	}
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:           "deep-audited-restore",
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		RunStatus:            ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs:         1,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		Pages: []ReviewerLearningPage{{
			Index:                  1,
			URL:                    "https://example.test/topic/a/",
			BaselineStatus:         BaselineValidGold,
			Expected:               expected,
			FirstPass:              ReviewerLearningLayer{Actual: modelActual, Metrics: modelMetrics},
			WithExistingRules:      ReviewerLearningLayer{Actual: modelActual, Metrics: modelMetrics},
			AfterNewReviewerPrompt: ReviewerLearningLayer{Actual: modelActual, Metrics: modelMetrics},
			FinalApproved:          ReviewerLearningLayer{Actual: modelActual, Metrics: modelMetrics},
			FinalApprovalMethod:    ReviewerLearningApprovalReviewReadyNoStrict,
			ManualEditRequired:     true,
			ReviewerTurns:          4,
			AuditedEditOperations: []ReviewerLearningAuditedEditOperation{
				{Operation: "correct_metadata", BlockID: "body", Reason: "reviewer corrected source-evidenced output"},
				{Operation: "drop_extra", BlockID: "extra", Reason: "reviewer removed model output that is not part of the approved semantic baseline"},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	page := repaired.Pages[0]
	if page.FinalApprovalMethod != ReviewerLearningApprovalAuditedBlockSelect || !page.ManualEditRequired || !page.Stalled {
		t.Fatalf("repair should restore source-evidenced audited block selection, got %#v", page)
	}
	if page.FinalApproved.Metrics.RelevantTextF1 != 1 || repaired.PagesNeedingAuditedEdit != 1 || repaired.PagesRequiringHumanConfirmation != 0 {
		t.Fatalf("restored audited block selection should be counted separately from model/human-confirmation success: page=%#v report=%#v", page, repaired)
	}
	if !strings.Contains(page.StallReason, "strict 0.98 F1") {
		t.Fatalf("restored stall reason should explain strict pass was not reached, got %q", page.StallReason)
	}
}

func TestRepairReviewerLearningReportClearsObsoleteTurnsWhenFirstPassNowStrict(t *testing.T) {
	reportDirectory := t.TempDir()
	expected := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Text: "Clinical text with current-normalized media and related information."}}
	modelActual := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Text: "Clinical text with current-normalized media and related information."}}
	modelMetrics := EvaluateBenchmark(expected, modelActual)
	if !reviewerLearningPasses(modelMetrics, 0.98) {
		t.Fatalf("test setup expected strict model metrics: %#v", modelMetrics)
	}
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:       "deep-obsolete-turn-repair",
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		RunStatus:        ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs:     1,
		QualityThreshold: 0.98,
		Pages: []ReviewerLearningPage{{
			Index:                  1,
			URL:                    "https://example.test/topic/a/",
			BaselineStatus:         BaselineValidGold,
			Expected:               expected,
			RawExpected:            expected,
			FirstPass:              ReviewerLearningLayer{Actual: modelActual, Metrics: BenchmarkMetrics{RelevantTextF1: 0.62}},
			WithExistingRules:      ReviewerLearningLayer{Actual: modelActual, Metrics: BenchmarkMetrics{RelevantTextF1: 0.62}},
			AfterNewReviewerPrompt: ReviewerLearningLayer{Actual: modelActual, Metrics: BenchmarkMetrics{RelevantTextF1: 0.62}},
			FinalApproved:          auditedHumanEditLayer(expected),
			FinalApprovalMethod:    ReviewerLearningApprovalStalledHumanEdit,
			ManualEditRequired:     true,
			Stalled:                true,
			StallReason:            "legacy scoring stalled",
			ReviewerTurns:          3,
			ReviewerPrompt:         "For this page only: obsolete prompt.",
			ReviewerRuleScope:      RuleScopeURL,
			ReviewerRuleID:         3,
			ReviewerTurnDetails: []ReviewerLearningTurn{
				{Turn: 1, Scope: RuleScopeURL, Prompt: "obsolete 1"},
				{Turn: 2, Scope: RuleScopeURL, Prompt: "obsolete 2"},
				{Turn: 3, Scope: RuleScopeURL, Prompt: "obsolete 3"},
			},
		}},
		Rules: []ReviewerLearningRule{
			{ID: 1, Version: 1, Scope: RuleScopeURL, OriginURL: "https://example.test/topic/a/", Instruction: "obsolete 1", Status: "active", CreatedOnPage: 1, CreatedOnTurn: 1},
			{ID: 2, Version: 2, Scope: RuleScopeURL, OriginURL: "https://example.test/topic/a/", Instruction: "obsolete 2", Status: "active", CreatedOnPage: 1, CreatedOnTurn: 2},
			{ID: 3, Version: 3, Scope: RuleScopeURL, OriginURL: "https://example.test/topic/a/", Instruction: "obsolete 3", Status: "active", CreatedOnPage: 1, CreatedOnTurn: 3},
		},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	page := repaired.Pages[0]
	if page.FinalApprovalMethod != ReviewerLearningApprovalFirstPass || !page.StrictPass || page.ManualEditRequired || page.Stalled {
		t.Fatalf("strict current first pass should be repaired to clean model success: %#v", page)
	}
	if page.ReviewerTurns != 0 || len(page.ReviewerTurnDetails) != 0 || page.ReviewerPrompt != "" || page.ReviewerRuleID != 0 || page.ReviewerRuleScope != "" {
		t.Fatalf("obsolete reviewer metadata should be cleared when no turn is needed under current scoring: %#v", page)
	}
	if len(repaired.Rules) != 0 || repaired.TotalReviewerPrompts != 0 || repaired.TotalReviewerTurns != 0 || repaired.PagesRequiringZeroReviewerPrompts != 1 {
		t.Fatalf("obsolete rules and prompt counts should be removed from repaired proof: %#v", repaired)
	}
}

func TestRepairReviewerLearningReportDoesNotCountAuditedGoldAsReviewReadyModelOutput(t *testing.T) {
	reportDirectory := t.TempDir()
	expected := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Type: TypeCritical, Text: "Critical clinical text that must be retained."}}
	weakModel := []BenchmarkSection{{BlockID: "body", Role: RoleContent, Type: TypeInfo, Text: "Critical clinical text"}}
	weakMetrics := EvaluateBenchmark(expected, weakModel)
	if reviewerLearningPasses(weakMetrics, 0.97) {
		t.Fatalf("test setup expected model output below review-ready threshold: %#v", weakMetrics)
	}
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:           "deep-audited-not-review-ready",
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		RunStatus:            ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs:         1,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		Pages: []ReviewerLearningPage{{
			Index:                  1,
			URL:                    "https://example.test/topic/a/",
			BaselineStatus:         BaselineValidGold,
			Expected:               expected,
			RawExpected:            expected,
			FirstPass:              ReviewerLearningLayer{Actual: weakModel, Metrics: weakMetrics},
			WithExistingRules:      ReviewerLearningLayer{Actual: weakModel, Metrics: weakMetrics},
			AfterNewReviewerPrompt: ReviewerLearningLayer{Actual: weakModel, Metrics: weakMetrics},
			FinalApproved:          auditedHumanEditLayer(expected),
			FinalApprovalMethod:    ReviewerLearningApprovalStalledHumanEdit,
			ManualEditRequired:     true,
			Stalled:                true,
			ReviewerTurns:          3,
			ReviewerTurnDetails:    []ReviewerLearningTurn{{Turn: 1}, {Turn: 2}, {Turn: 3}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	page := repaired.Pages[0]
	if !page.StrictPass || !page.ManualEditRequired || page.FinalApprovalMethod != ReviewerLearningApprovalStalledHumanEdit {
		t.Fatalf("audited final output should still be final-strict and manually required: %#v", page)
	}
	if page.ReviewReady || repaired.ReviewReady97Pages != 0 || repaired.StrictModelPassPages != 0 {
		t.Fatalf("audited gold must not inflate model review-ready/strict counters: page=%#v report=%#v", page, repaired)
	}
}

func TestRepairReviewerLearningReportGeneralizesLegacySourceScopedSnippetRules(t *testing.T) {
	reportDirectory := t.TempDir()
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:   "deep-legacy-rule-repair",
		ModelMode:    "openai",
		LearningMode: ReviewerLearningModeDeep,
		RunStatus:    ReviewerLearningRunStatusPausedChunk,
		ExpectedURLs: 3,
		Pages: []ReviewerLearningPage{
			{
				Index:               1,
				URL:                 "https://example.test/topic/a/",
				BaselineStatus:      BaselineValidGold,
				Expected:            []BenchmarkSection{{BlockID: "title", Text: "Title A"}},
				FinalApprovalMethod: ReviewerLearningApprovalFirstPass,
				FinalApproved:       ReviewerLearningLayer{Actual: []BenchmarkSection{{BlockID: "title", Text: "Title A"}}, Metrics: BenchmarkMetrics{RelevantTextF1: 1, ExactParity: true}},
			},
		},
		Rules: []ReviewerLearningRule{
			{
				ID:             1,
				Version:        1,
				Scope:          RuleScopeSource,
				Status:         "active",
				CreatedOnPage:  1,
				Instruction:    `For this source: keep highlighted warning/info boxes with clinical safety, contraindication, dosage-limit, urgent-care, or patient-risk content. Include source blocks containing: "Larma enligt åtgärdsplan om cirkulationen inte förbättras".`,
				MismatchLabels: []string{"missing_warning_info_box"},
			},
			{
				ID:            2,
				Version:       2,
				Scope:         RuleScopeSource,
				Status:        "active",
				CreatedOnPage: 1,
				Instruction:   "For this source: keep clinical images, figures, videos, and media references when the human baseline includes them. Include exact block_id b0026.",
			},
			{
				ID:            3,
				Version:       3,
				Scope:         RuleScopeSource,
				Status:        "active",
				CreatedOnPage: 1,
				Instruction:   `For this source: drop blocks containing share, print, copy-link, email, navigation, or related-card UI unless they contain clinical instructions. Extra text pattern: "Denna text gäller vuxna. För barn finns specifika riktlinjer. Länk till vårdprogram för barn med trakealkanyl finns".`,
			},
			{
				ID:            4,
				Version:       4,
				Scope:         RuleScopeURL,
				OriginURL:     "https://example.test/topic/a/",
				Status:        "active",
				CreatedOnPage: 1,
				Instruction:   "For this page only: final reviewer correction for https://example.test/topic/a/. Include these exact missing source block_id values with include=true: b0009.",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairReviewerLearningReport(reportDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got := repaired.Rules[0].Instruction; strings.Contains(got, "Larma enligt") || strings.Contains(got, "Include source blocks containing") || !strings.Contains(got, "keep highlighted warning/info boxes") {
		t.Fatalf("source warning rule should be generalized, got %q", got)
	}
	if got := repaired.Rules[1].Instruction; strings.Contains(got, "block_id") || !strings.Contains(got, "keep clinical images") {
		t.Fatalf("source media rule should be generalized without exact block ids, got %q", got)
	}
	if got := repaired.Rules[2].Instruction; strings.Contains(got, "Denna text gäller vuxna") || strings.Contains(got, "Extra text pattern") || !strings.Contains(got, "drop share, print") {
		t.Fatalf("source extra-text rule should be generalized without exact snippets, got %q", got)
	}
	if got := repaired.Rules[3].Instruction; !strings.Contains(got, "block_id values") {
		t.Fatalf("URL-scoped exact correction should be preserved, got %q", got)
	}
	sourcePrompt, err := os.ReadFile(filepath.Join(reportDirectory, "source-working-prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sourcePrompt), "Larma enligt") || strings.Contains(string(sourcePrompt), "b0026") || strings.Contains(string(sourcePrompt), "Denna text gäller vuxna") {
		t.Fatalf("source working prompt should not contain page-specific snippets or block ids:\n%s", sourcePrompt)
	}
}

func TestReviewerLearningDeepChunkPreservesFullExpectedURLScope(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/c/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/d/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		html := `<html><body><main><h1>Title ` + strings.ToUpper(name) + `</h1></main></body></html>`
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reportDirectory := filepath.Join(directory, "reports")

	first, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-chunk",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      reportDirectory,
		Model:                &learningModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		ChunkPages:           2,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run first chunk: %v", err)
	}
	if first.RunStatus != ReviewerLearningRunStatusPausedChunk || first.ExpectedURLs != 4 || len(first.Pages) != 2 || first.LastCompletedPage != 2 || first.ProofUsable {
		t.Fatalf("first chunk should pause after two pages while preserving full scope: %#v", first)
	}

	second, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:           "deep-chunk",
		FixtureDirectory:     fixtureDirectory,
		MappingPath:          filepath.Join(directory, "mapping.json"),
		URLContains:          "/topic/",
		ReportDirectory:      reportDirectory,
		Model:                &learningModel{},
		ModelMode:            "openai",
		LearningMode:         ReviewerLearningModeDeep,
		Resume:               true,
		QualityThreshold:     0.98,
		ReviewReadyThreshold: 0.97,
		ChunkPages:           2,
		LockTimeout:          time.Minute,
	})
	if err != nil {
		t.Fatalf("run second chunk: %v", err)
	}
	if second.RunStatus != ReviewerLearningRunStatusCompleted || second.ExpectedURLs != 4 || len(second.Pages) != 4 || second.LastCompletedPage != 4 {
		t.Fatalf("second chunk should complete remaining full scope: %#v", second)
	}
}

func TestReviewerLearningDeepForceIgnoresResumeArtifactAndStartsClean(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "b.html"), []byte(`<html><body><main><h1>Title B</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	reportDirectory := filepath.Join(directory, "reports")
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:     "deep-force-clean",
		RunStatus:      ReviewerLearningRunStatusInterrupted,
		LearningMode:   ReviewerLearningModeDeep,
		ExpectedURLs:   2,
		DiscoveredURLs: 1,
		Pages: []ReviewerLearningPage{{
			Index:               1,
			URL:                 "https://example.test/topic/a/",
			BaselineStatus:      BaselineValidGold,
			FinalApprovalMethod: ReviewerLearningApprovalStalledHumanEdit,
			ReviewerTurns:       99,
			FinalApproved:       ReviewerLearningLayer{Metrics: BenchmarkMetrics{RelevantTextF1: 1}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "deep-force-clean",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &learningModel{},
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		Resume:           true,
		Force:            true,
		QualityThreshold: 0.98,
		LockTimeout:      time.Minute,
	})
	if err != nil {
		t.Fatalf("force clean deep run: %v", err)
	}
	if len(report.Pages) != 2 || report.Pages[0].ReviewerTurns == 99 || report.Pages[0].FinalApprovalMethod == ReviewerLearningApprovalStalledHumanEdit {
		t.Fatalf("force should ignore old resume artifact and rebuild from page 1: %#v", report.Pages)
	}
}

func TestReviewerLearningDeepResumeRejectsStaleImplementationVersion(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	reportDirectory := filepath.Join(directory, "reports")
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:             "deep-stale-version",
		ImplementationVersion:  "old-semantic-extraction",
		RunStatus:              ReviewerLearningRunStatusPausedChunk,
		LearningMode:           ReviewerLearningModeDeep,
		ExpectedURLs:           1,
		DiscoveredURLs:         1,
		LastCompletedPage:      1,
		QualityThreshold:       0.98,
		ReviewReadyThreshold:   0.97,
		FinalApprovedAggregate: BenchmarkMetrics{RelevantTextF1: 1},
		Pages: []ReviewerLearningPage{{
			Index:               1,
			URL:                 "https://example.test/topic/a/",
			BaselineStatus:      BaselineValidGold,
			FinalApprovalMethod: ReviewerLearningApprovalFirstPass,
			FinalApproved:       ReviewerLearningLayer{Metrics: BenchmarkMetrics{RelevantTextF1: 1}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "deep-stale-version",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &learningModel{},
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		Resume:           true,
		QualityThreshold: 0.98,
		LockTimeout:      time.Minute,
	})
	if err == nil || !strings.Contains(err.Error(), "implementation version") {
		t.Fatalf("resume should reject stale implementation version, got %v", err)
	}
}

func TestReviewerLearningDeepResumeRejectsMissingImplementationVersion(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	reportDirectory := filepath.Join(directory, "reports")
	if err := WriteReviewerLearningReports(reportDirectory, ReviewerLearningReport{
		FixtureSet:        "deep-missing-version",
		RunStatus:         ReviewerLearningRunStatusPausedChunk,
		LearningMode:      ReviewerLearningModeDeep,
		ExpectedURLs:      1,
		DiscoveredURLs:    1,
		LastCompletedPage: 1,
		Pages: []ReviewerLearningPage{{
			Index:               1,
			URL:                 "https://example.test/topic/a/",
			BaselineStatus:      BaselineValidGold,
			FinalApprovalMethod: ReviewerLearningApprovalFirstPass,
			FinalApproved:       ReviewerLearningLayer{Metrics: BenchmarkMetrics{RelevantTextF1: 1}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "deep-missing-version",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &learningModel{},
		ModelMode:        "openai",
		LearningMode:     ReviewerLearningModeDeep,
		Resume:           true,
		QualityThreshold: 0.98,
		LockTimeout:      time.Minute,
	})
	if err == nil || !strings.Contains(err.Error(), "implementation version") {
		t.Fatalf("resume should reject legacy report without implementation version, got %v", err)
	}
}
