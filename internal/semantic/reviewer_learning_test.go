package semantic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type learningModel struct {
	routes   int
	extracts int
}

func (model *learningModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	model.routes++
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4, Latency: time.Millisecond}, nil
}

type stagedLearningModel struct {
	extracts int
}

func (model *stagedLearningModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return RouteResponse{Action: RouteExtract, Confidence: 1, Reason: "test article"}, ModelUsage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4, Latency: time.Millisecond}, nil
}

func (model *stagedLearningModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extracts++
	decisions := make([]BlockDecision, 0, len(blocks))
	includeWarning := strings.Contains(prompt, "keep highlighted warning")
	includeTable := strings.Contains(prompt, "keep every dosage")
	for _, block := range blocks {
		text := strings.ToLower(block.Text)
		include := strings.HasPrefix(text, "title")
		if block.Tag == "aside" && includeWarning {
			include = true
		}
		if block.Tag == "table" && includeTable {
			include = true
		}
		role, blockType, parseMode := fixtureClassification(block)
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: include, Role: role, Type: blockType, ParseMode: parseMode, Reason: "staged decision"})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions}
	return response, ModelUsage{InputTokens: 11, OutputTokens: 3, TotalTokens: 14, Latency: 2 * time.Millisecond}, ValidateExtraction(response, blocks)
}

func (model *learningModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extracts++
	decisions := make([]BlockDecision, 0, len(blocks))
	includeWarning := strings.Contains(prompt, "keep highlighted warning")
	for _, block := range blocks {
		include := strings.HasPrefix(strings.ToLower(block.Text), "title")
		if block.Tag == "aside" && includeWarning {
			include = true
		}
		role, blockType, parseMode := fixtureClassification(block)
		decisions = append(decisions, BlockDecision{BlockID: block.ID, Include: include, Role: role, Type: blockType, ParseMode: parseMode, Reason: "test decision"})
	}
	response := ExtractionResponse{PageKind: "article", Blocks: decisions}
	return response, ModelUsage{InputTokens: 7, OutputTokens: 2, TotalTokens: 9, Latency: 2 * time.Millisecond}, ValidateExtraction(response, blocks)
}

func TestReviewerLearningBenchmarkCarriesSourceRulesForward(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1><aside>Observera: critical warning A</aside></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "b.html"), []byte(`<html><body><main><h1>Title B</h1><aside>Observera: critical warning B</aside></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "test-learning",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Model:            &learningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
		MaxReviewerTurns: 1,
	})
	if err != nil {
		t.Fatalf("run reviewer learning benchmark: %v", err)
	}

	if report.TotalReviewerPrompts != 1 || report.SourceScopedRulesCreated != 1 || report.URLScopedRulesCreated != 0 {
		t.Fatalf("expected one reusable source rule, got %#v", report)
	}
	if report.PagesFixedByExistingRules != 1 || report.AutonomousAfterLearningPages != 1 {
		t.Fatalf("second page should be fixed by the previous source rule: %#v", report)
	}
	if report.PagesNeedingAuditedEdit != 0 || report.FinalApprovedAggregate.RelevantTextF1 != 1 {
		t.Fatalf("model should reach full text parity after reviewer learning without audited edit: %#v", report)
	}
	if len(report.Rules) != 1 || report.Rules[0].ReuseCount != 1 || report.Rules[0].Scope != RuleScopeSource {
		t.Fatalf("source rule reuse was not tracked: %#v", report.Rules)
	}
	if report.ModelInputTokens == 0 || !report.TokenUsageAvailable || report.RawUsageMissing {
		t.Fatalf("Model-style usage should be counted: %#v", report)
	}
	for _, name := range []string{"reviewer-learning.json", "reviewer-learning.html", "reviewer-learning-prompts.md", "reviewer-learning-rules.json", "reviewer-learning-pages.json"} {
		if _, err := os.Stat(filepath.Join(directory, "reports", name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
}

func TestReviewerLearningURLRuleDoesNotAffectOtherPagesAndAuditedEditIsSeparate(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"note","role":"content","container":".special-note","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true},{"name":"note","role":"content","container":".special-note","parse_mode":true}]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(`<html><body><main><h1>Title A</h1><p class="special-note">One-off note A</p></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "b.html"), []byte(`<html><body><main><h1>Title B</h1><p class="special-note">One-off note B</p></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "test-learning-url",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Model:            &learningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
		MaxReviewerTurns: 1,
	})
	if err != nil {
		t.Fatalf("run reviewer learning benchmark: %v", err)
	}

	if report.SourceScopedRulesCreated != 0 || report.URLScopedRulesCreated != 2 {
		t.Fatalf("page-specific issues should create URL rules only: %#v", report)
	}
	if report.PagesFixedByExistingRules != 0 || report.AutonomousAfterLearningPages != 0 {
		t.Fatalf("URL rules must not fix later pages: %#v", report)
	}
	if report.PagesNeedingAuditedEdit != 2 || !report.FinalApprovedAggregate.ExactParity || report.AfterNewPromptAggregate.ExactParity {
		t.Fatalf("audited edits should be final parity layer only: %#v", report)
	}
	if report.Pages[0].FinalApprovalMethod != "audited_human_edit" || report.Pages[0].AfterNewReviewerPrompt.Metrics.ExactParity {
		t.Fatalf("model success and audited edit were blended: %#v", report.Pages[0])
	}
}

func TestReviewerLearningBenchmarkLabelsFixtureAndMissingProviderUsage(t *testing.T) {
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

	fixtureReport, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "fixture",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "fixture-reports"),
		Model:            &FixtureModel{},
		ModelMode:        "fixture",
	})
	if err != nil {
		t.Fatalf("run fixture reviewer learning benchmark: %v", err)
	}
	if fixtureReport.ModelMode != "fixture_regression" || fixtureReport.TokenUsageAvailable || !strings.Contains(fixtureReport.UsageNote, "Fixture regression") {
		t.Fatalf("fixture benchmark was not clearly labeled: %#v", fixtureReport)
	}

	missingUsageReport, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "missing-usage",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "missing-usage-reports"),
		Model:            &FixtureModel{},
		ModelMode:        "openai",
	})
	if err != nil {
		t.Fatalf("run missing-usage reviewer learning benchmark: %v", err)
	}
	if missingUsageReport.TokenUsageAvailable || !missingUsageReport.RawUsageMissing || !strings.Contains(missingUsageReport.UsageNote, "provider response did not include token usage") {
		t.Fatalf("missing provider usage should be explicit, not shown as 0/0: %#v", missingUsageReport)
	}
}

func TestReviewerLearningAdaptiveStopsAfterStableStreak(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory, mappingPath := writeReviewerLearningWarningFixture(t, directory, 8)

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "adaptive",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Model:            &learningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
		AdaptiveStop:     true,
		MinPages:         3,
		StableStreak:     2,
	})
	if err != nil {
		t.Fatalf("run adaptive reviewer learning benchmark: %v", err)
	}

	if report.RunStatus != ReviewerLearningRunStatusStoppedStable {
		t.Fatalf("expected stable early stop, got %#v", report)
	}
	if len(report.Pages) != 3 || report.ExpectedURLs != 8 || report.LastCompletedPage != 3 {
		t.Fatalf("expected stop after one taught page plus two stable pages: %#v", report)
	}
	if report.CurrentStableStreak != 2 || report.StableStreakStartPage != 2 {
		t.Fatalf("stable streak metrics were not recorded: %#v", report)
	}
	if report.PagesFixedByExistingRules != 2 || report.TotalReviewerPrompts != 1 {
		t.Fatalf("learned rule should fix two later pages: %#v", report)
	}
}

func TestReviewerLearningResumePreservesRulesAndContinuesRemainingPages(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory, mappingPath := writeReviewerLearningWarningFixture(t, directory, 4)
	reportDirectory := filepath.Join(directory, "reports")

	initial, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "resume",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &learningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
	})
	if err != nil {
		t.Fatalf("run initial reviewer learning benchmark: %v", err)
	}
	initial.Pages = initial.Pages[:2]
	initial.RunStatus = ReviewerLearningRunStatusInterrupted
	initial.CompletedAt = time.Time{}
	initial.updateReviewerLearningSummary()
	if err := WriteReviewerLearningReports(reportDirectory, initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.Pages) != 2 || len(initial.Rules) != 1 {
		t.Fatalf("unexpected initial checkpoint: %#v", initial)
	}

	resumed, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "resume",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &learningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
		Resume:           true,
	})
	if err != nil {
		t.Fatalf("resume reviewer learning benchmark: %v", err)
	}

	if resumed.RunStatus != ReviewerLearningRunStatusCompleted || len(resumed.Pages) != 4 || resumed.LastCompletedPage != 4 {
		t.Fatalf("resume did not complete remaining pages: %#v", resumed)
	}
	if len(resumed.Rules) != 1 || resumed.Rules[0].ReuseCount != 3 {
		t.Fatalf("resume should preserve and reuse original source rule: %#v", resumed.Rules)
	}
	status, err := LoadReviewerLearningStatus(reportDirectory)
	if err != nil {
		t.Fatalf("load reviewer learning status: %v", err)
	}
	if status.RunStatus != ReviewerLearningRunStatusCompleted || status.PagesCompleted != 4 || status.TotalReviewerPrompts != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestReviewerLearningResumeRejectsLegacyIncompatibleReport(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory, mappingPath := writeReviewerLearningWarningFixture(t, directory, 1)
	reportDirectory := filepath.Join(directory, "reports")
	if err := os.MkdirAll(reportDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"fixture_set":"old-smoke-run","model_mode":"openai","expected_urls":10,"pages":[{"index":1,"url":"https://legacy.example.test/"}]}`
	if err := os.WriteFile(filepath.Join(reportDirectory, "reviewer-learning.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "adaptive-proof",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      mappingPath,
		URLContains:      "/topic/",
		ReportDirectory:  reportDirectory,
		Model:            &learningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
		Resume:           true,
	})
	if err == nil || !strings.Contains(err.Error(), "different report directory") {
		t.Fatalf("resume should reject legacy incompatible report without replacing its evidence: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(reportDirectory, "reviewer-learning.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != legacy {
		t.Fatal("legacy report was overwritten")
	}
}

func TestReviewerLearningRecordsMultipleReviewerTurnsForOnePage(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false},
		{"name":"table","role":"table","container":"table","parse_mode":false}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1>Title A</h1><aside>Observera: critical warning A</aside><table><tr><td>Dosage table A</td></tr></table></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "multi-turn",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Model:            &stagedLearningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
		MaxReviewerTurns: 3,
	})
	if err != nil {
		t.Fatalf("run reviewer learning benchmark: %v", err)
	}

	if len(report.Pages) != 1 {
		t.Fatalf("expected one page: %#v", report.Pages)
	}
	page := report.Pages[0]
	if page.ReviewerTurns != 2 || len(page.ReviewerTurnDetails) != 2 {
		t.Fatalf("expected two recorded reviewer turns, got %#v", page)
	}
	if !strings.Contains(page.ReviewerTurnDetails[0].Prompt, "keep highlighted warning") {
		t.Fatalf("first turn should teach warning handling: %#v", page.ReviewerTurnDetails)
	}
	if !strings.Contains(page.ReviewerTurnDetails[1].Prompt, "keep every dosage") {
		t.Fatalf("second turn should teach table handling: %#v", page.ReviewerTurnDetails)
	}
	if page.FinalApprovalMethod != ReviewerLearningApprovalNewReviewerPrompt || page.FinalApproved.Metrics.RelevantTextF1 != 1 {
		t.Fatalf("second reviewer turn should reach model parity, got %#v", page)
	}
	if report.TotalReviewerPrompts != 2 || report.TotalReviewerTurns != 2 || report.SourceScopedRulesCreated != 2 {
		t.Fatalf("summary should count every reviewer turn: %#v", report)
	}
	transcript, err := os.ReadFile(filepath.Join(directory, "reports", "reviewer-learning-prompts.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transcript), "Reviewer turn 1") || !strings.Contains(string(transcript), "Reviewer turn 2") {
		t.Fatalf("prompt transcript should list every turn:\n%s", string(transcript))
	}
}

func TestReviewerLearningStopsReviewerTurnsAtConfiguredMaximum(t *testing.T) {
	directory := t.TempDir()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[
		{"name":"title","role":"header","container":"h1","parse_mode":true},
		{"name":"warning","role":"info_box","type":"critical","container":"aside","parse_mode":false},
		{"name":"table","role":"table","container":"table","parse_mode":false}
	]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	html := `<html><body><main><h1>Title A</h1><aside>Observera: critical warning A</aside><table><tr><td>Dosage table A</td></tr></table></main></body></html>`
	if err := os.WriteFile(filepath.Join(fixtureDirectory, "a.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunReviewerLearningBenchmark(context.Background(), ReviewerLearningOptions{
		FixtureSet:       "max-turns",
		FixtureDirectory: fixtureDirectory,
		MappingPath:      filepath.Join(directory, "mapping.json"),
		URLContains:      "/topic/",
		ReportDirectory:  filepath.Join(directory, "reports"),
		Model:            &stagedLearningModel{},
		ModelMode:        "openai",
		QualityThreshold: 0.98,
		MaxReviewerTurns: 1,
	})
	if err != nil {
		t.Fatalf("run reviewer learning benchmark: %v", err)
	}

	page := report.Pages[0]
	if page.ReviewerTurns != 1 || len(page.ReviewerTurnDetails) != 1 {
		t.Fatalf("expected one turn due configured maximum: %#v", page)
	}
	if page.FinalApprovalMethod != ReviewerLearningApprovalAuditedHumanEdit || page.AfterNewReviewerPrompt.Metrics.RelevantTextF1 == 1 {
		t.Fatalf("page should fall back to audited edit after max turn is exhausted: %#v", page)
	}
}

func writeReviewerLearningWarningFixture(t *testing.T, directory string, pages int) (string, string) {
	t.Helper()
	fixtureDirectory := filepath.Join(directory, "fixtures")
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	type identifier struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		Type      string `json:"type,omitempty"`
		Container string `json:"container"`
		ParseMode bool   `json:"parse_mode"`
	}
	type mappedPage struct {
		URL        string       `json:"url"`
		Identifier []identifier `json:"identifier"`
	}
	mapping := struct {
		URLs []mappedPage `json:"urls"`
	}{}
	for index := 1; index <= pages; index++ {
		url := "https://example.test/topic/page-" + string(rune('a'+index-1)) + "/"
		mapping.URLs = append(mapping.URLs, mappedPage{URL: url, Identifier: []identifier{
			{Name: "title", Role: "header", Container: "h1", ParseMode: true},
			{Name: "warning", Role: "info_box", Type: "critical", Container: "aside", ParseMode: false},
		}})
		name := "page-" + string(rune('a'+index-1)) + ".html"
		html := `<html><body><main><h1>Title ` + string(rune('A'+index-1)) + `</h1><aside>Observera: critical warning ` + string(rune('A'+index-1)) + `</aside></main></body></html>`
		if err := os.WriteFile(filepath.Join(fixtureDirectory, name), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(mapping)
	if err != nil {
		t.Fatal(err)
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	if err := os.WriteFile(mappingPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	return fixtureDirectory, mappingPath
}
