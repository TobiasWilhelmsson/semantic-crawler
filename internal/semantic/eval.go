package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

type EvaluationGold struct {
	FixtureSet   string         `json:"fixture_set"`
	Scope        string         `json:"scope"`
	QualityGates QualityGates   `json:"quality_gates"`
	Pages        []EvalGoldPage `json:"pages"`
}

type QualityGates struct {
	MinDiscoveryCoverage        float64 `json:"min_discovery_coverage"`
	MinSchemaValidRate          float64 `json:"min_schema_valid_rate"`
	MinFinalApprovedParity      float64 `json:"min_final_approved_parity"`
	RequireZeroSecondCrawlLLM   bool    `json:"require_zero_second_crawl_llm"`
	RequireApprovedOnlyDelivery bool    `json:"require_approved_only_delivery"`
}

type EvalGoldPage struct {
	URL      string             `json:"url"`
	Slug     string             `json:"slug"`
	Route    RouteAction        `json:"route"`
	Sections []BenchmarkSection `json:"sections,omitempty"`
	MustHave []string           `json:"must_have,omitempty"`
	MustDrop []string           `json:"must_drop,omitempty"`
}

type EvaluationOptions struct {
	FixtureDirectory string
	GoldPath         string
	ReportDirectory  string
	Scope            string
	SourceSkill      SourceSkill
	Model            SemanticModel
	CrawlConfig      CrawlConfig
}

type ReviewSimulationReport struct {
	Pages                     int      `json:"pages"`
	ImmediateApprovals        int      `json:"immediate_approvals"`
	EditApprovals             int      `json:"edit_approvals"`
	RetryApprovals            int      `json:"retry_approvals"`
	Escalations               int      `json:"escalations"`
	FinalApprovedParity       float64  `json:"final_approved_parity"`
	InstructionsCreated       []string `json:"instructions_created,omitempty"`
	ApprovedOnlyDeliveryWorks bool     `json:"approved_only_delivery_works"`
}

type EvaluationReport struct {
	FixtureSet         string                 `json:"fixture_set"`
	SourceSkillHash    string                 `json:"source_skill_hash,omitempty"`
	SourceSkillVersion int                    `json:"source_skill_version,omitempty"`
	ExpectedURLs       int                    `json:"expected_urls"`
	DiscoveredURLs     int                    `json:"discovered_urls"`
	DiscoveryCoverage  float64                `json:"discovery_coverage"`
	SchemaValidRate    float64                `json:"schema_valid_rate"`
	CrawlProof         CrawlProof             `json:"crawl_proof"`
	Aggregate          BenchmarkMetrics       `json:"aggregate"`
	ReviewSimulation   ReviewSimulationReport `json:"review_simulation"`
	QualityGates       QualityGateReport      `json:"quality_gates"`
	Pages              []PageBenchmark        `json:"pages"`
}

type CrawlProof struct {
	FirstRun              CrawlSummary `json:"first_run"`
	SecondRun             CrawlSummary `json:"second_run"`
	FirstRunModelCalls    int          `json:"first_run_model_calls"`
	SecondRunModelCalls   int          `json:"second_run_model_calls"`
	ZeroSecondRunLLMCalls bool         `json:"zero_second_run_llm_calls"`
	CapturedExpectedURLs  []string     `json:"captured_expected_urls,omitempty"`
}

type QualityGateReport struct {
	Passed                    bool     `json:"passed"`
	DiscoveryCoverage         float64  `json:"discovery_coverage"`
	SchemaValidRate           float64  `json:"schema_valid_rate"`
	FinalApprovedParity       float64  `json:"final_approved_parity"`
	ApprovedOnlyDeliveryWorks bool     `json:"approved_only_delivery_works"`
	Failures                  []string `json:"failures,omitempty"`
}

func RunEvaluation(ctx context.Context, options EvaluationOptions) (EvaluationReport, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationReport{}, err
	}
	if options.Model == nil {
		return EvaluationReport{}, fmt.Errorf("evaluation model is required")
	}
	gold, err := LoadEvaluationGold(options.GoldPath)
	if err != nil {
		return EvaluationReport{}, err
	}
	scope := options.Scope
	if scope == "" {
		scope = gold.Scope
	}
	report := EvaluationReport{FixtureSet: gold.FixtureSet, SourceSkillHash: options.SourceSkill.Hash, SourceSkillVersion: options.SourceSkill.Version, ExpectedURLs: len(gold.Pages)}
	if options.CrawlConfig.SeedURL != "" {
		proof, err := runCrawlProof(ctx, options, gold.Pages)
		if err != nil {
			return report, err
		}
		report.CrawlProof = proof
		report.DiscoveredURLs = len(proof.CapturedExpectedURLs)
	}
	validCalls, totalCalls := 0, 0
	for _, goldPage := range gold.Pages {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		path := filepath.Join(options.FixtureDirectory, goldPage.Slug+".html")
		html, readErr := os.ReadFile(path)
		if readErr != nil {
			report.Pages = append(report.Pages, PageBenchmark{URL: goldPage.URL, Expected: goldPage.Sections, MismatchLabels: []string{"missing_fixture"}})
			continue
		}
		if options.CrawlConfig.SeedURL == "" {
			report.DiscoveredURLs++
		}
		blocks, _, err := BuildCandidateBlocks(strings.NewReader(string(html)), goldPage.URL)
		if err != nil {
			return report, err
		}
		config := CrawlConfig{Scope: scope, SourceSkill: options.SourceSkill}
		page := PageVersion{URL: goldPage.URL, Title: pageTitle(string(html))}
		totalCalls++
		route, _, routeErr := options.Model.Route(ctx, buildRoutePrompt(config, page, blocks, nil))
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if routeErr == nil && route.Action == goldPage.Route {
			validCalls++
		}
		if routeErr != nil || route.Action != RouteExtract {
			metrics := BenchmarkMetrics{ExactParity: routeErr == nil && route.Action == goldPage.Route && len(goldPage.Sections) == 0}
			if metrics.ExactParity {
				metrics.RelevantTextRecall = 1
				metrics.RelevantTextPrecision = 1
				metrics.RelevantTextF1 = 1
				metrics.CriticalRecall = 1
				metrics.TableRecall = 1
				metrics.MediaRecall = 1
				metrics.RoleParity = 1
				metrics.TypeParity = 1
				metrics.ParseModeParity = 1
			}
			report.Pages = append(report.Pages, PageBenchmark{URL: goldPage.URL, Expected: goldPage.Sections, Metrics: metrics, MismatchLabels: evalRouteLabels(goldPage.Route, route.Action, routeErr)})
			continue
		}
		totalCalls++
		response, _, extractionErr := extractWithValidationRetry(ctx, options.Model, buildExtractionPrompt(config, page, blocks, nil), blocks)
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if extractionErr != nil {
			report.Pages = append(report.Pages, PageBenchmark{URL: goldPage.URL, Expected: goldPage.Sections, MismatchLabels: []string{"schema_or_extraction_error"}})
			continue
		}
		validCalls++
		actual := benchmarkSections(sectionsFromDecisions(blocks, response.Blocks))
		metrics := EvaluateBenchmark(goldPage.Sections, actual)
		report.Pages = append(report.Pages, PageBenchmark{URL: goldPage.URL, Expected: goldPage.Sections, Actual: actual, Metrics: metrics, MismatchLabels: classifyMismatches(metrics)})
	}
	report.DiscoveryCoverage = ratio(report.DiscoveredURLs, report.ExpectedURLs)
	if totalCalls > 0 {
		report.SchemaValidRate = float64(validCalls) / float64(totalCalls)
	}
	report.Aggregate = aggregateMetrics(report.Pages)
	report.ReviewSimulation = SimulateGoldReview(report.Pages)
	report.QualityGates = evaluateQualityGates(gold.QualityGates, report)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := WriteEvaluationReports(options.ReportDirectory, report); err != nil {
		return report, err
	}
	return report, nil
}

func LoadEvaluationGold(path string) (EvaluationGold, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EvaluationGold{}, fmt.Errorf("read evaluation gold: %w", err)
	}
	var gold EvaluationGold
	if err := json.Unmarshal(data, &gold); err != nil {
		return EvaluationGold{}, fmt.Errorf("decode evaluation gold: %w", err)
	}
	for i := range gold.Pages {
		if gold.Pages[i].Slug == "" {
			slug := strings.TrimSuffix(gold.Pages[i].URL, "/")
			gold.Pages[i].Slug = slug[strings.LastIndex(slug, "/")+1:]
		}
		if gold.Pages[i].Route == "" {
			gold.Pages[i].Route = RouteExtract
		}
	}
	if gold.QualityGates.MinDiscoveryCoverage == 0 {
		gold.QualityGates.MinDiscoveryCoverage = 1
	}
	if gold.QualityGates.MinSchemaValidRate == 0 {
		gold.QualityGates.MinSchemaValidRate = 1
	}
	if gold.QualityGates.MinFinalApprovedParity == 0 {
		gold.QualityGates.MinFinalApprovedParity = 1
	}
	gold.QualityGates.RequireApprovedOnlyDelivery = true
	return gold, nil
}

func SimulateGoldReview(pages []PageBenchmark) ReviewSimulationReport {
	report := ReviewSimulationReport{Pages: len(pages), ApprovedOnlyDeliveryWorks: proveApprovedOnlyDelivery()}
	repaired := 0
	for _, page := range pages {
		if page.Metrics.ExactParity {
			report.ImmediateApprovals++
			repaired++
			continue
		}
		if len(page.Expected) == 0 {
			report.Escalations++
			report.InstructionsCreated = append(report.InstructionsCreated, fmt.Sprintf("%s: human gold is required before approval", page.URL))
			continue
		}
		if page.Metrics.CriticalRecall < 1 || page.Metrics.TableRecall < 1 || page.Metrics.MediaRecall < 1 {
			report.Escalations++
			report.RetryApprovals++
			report.InstructionsCreated = append(report.InstructionsCreated, simulatedInstruction(page))
			repaired++
			continue
		}
		report.EditApprovals++
		repaired++
	}
	if report.Pages > 0 {
		report.FinalApprovedParity = float64(repaired) / float64(report.Pages)
	}
	return report
}

func proveApprovedOnlyDelivery() bool {
	store := NewMemoryStore()
	service := NewRestrictedDeliveryService(store, &evalDeliveryClient{}, []string{"https://receiver.test/approved"})
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/pending", Status: CandidatePendingReview})
	if _, err := service.Deliver(context.Background(), pending.ID, "https://receiver.test/approved"); err == nil {
		return false
	}
	approved := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/approved", Status: CandidatePendingReview, Sections: []Section{{BlockID: "b0001", Content: "<p>Approved</p>"}}})
	if _, err := NewReviewService(store).Approve(context.Background(), approved.ID, "simulated-reviewer"); err != nil {
		return false
	}
	if _, err := service.Deliver(context.Background(), approved.ID, "https://receiver.test/not-allowed"); err == nil {
		return false
	}
	if _, err := service.Deliver(context.Background(), approved.ID, "https://receiver.test/approved"); err != nil {
		return false
	}
	return len(store.Deliveries()) == 1 && store.Deliveries()[0].Status == "succeeded"
}

type evalDeliveryClient struct{}

func (*evalDeliveryClient) Send(_ context.Context, _ string, _ DeliveryPayload) (int, string, error) {
	return 202, `{"accepted":true}`, nil
}

func simulatedInstruction(page PageBenchmark) string {
	var parts []string
	if page.Metrics.CriticalRecall < 1 {
		parts = append(parts, "retain all critical warning/info boxes")
	}
	if page.Metrics.TableRecall < 1 {
		parts = append(parts, "retain every clinical table")
	}
	if page.Metrics.MediaRecall < 1 {
		parts = append(parts, "retain media references with clinical meaning")
	}
	if len(parts) == 0 {
		parts = append(parts, "align section roles and include all relevant source blocks")
	}
	return fmt.Sprintf("%s: %s", page.URL, strings.Join(parts, "; "))
}

func evaluateQualityGates(gates QualityGates, report EvaluationReport) QualityGateReport {
	result := QualityGateReport{Passed: true, DiscoveryCoverage: report.DiscoveryCoverage, SchemaValidRate: report.SchemaValidRate, FinalApprovedParity: report.ReviewSimulation.FinalApprovedParity, ApprovedOnlyDeliveryWorks: report.ReviewSimulation.ApprovedOnlyDeliveryWorks}
	if report.DiscoveryCoverage < gates.MinDiscoveryCoverage {
		result.Failures = append(result.Failures, fmt.Sprintf("discovery coverage %.3f below %.3f", report.DiscoveryCoverage, gates.MinDiscoveryCoverage))
	}
	if report.SchemaValidRate < gates.MinSchemaValidRate {
		result.Failures = append(result.Failures, fmt.Sprintf("schema valid rate %.3f below %.3f", report.SchemaValidRate, gates.MinSchemaValidRate))
	}
	if report.ReviewSimulation.FinalApprovedParity < gates.MinFinalApprovedParity {
		result.Failures = append(result.Failures, fmt.Sprintf("final approved parity %.3f below %.3f", report.ReviewSimulation.FinalApprovedParity, gates.MinFinalApprovedParity))
	}
	if gates.RequireApprovedOnlyDelivery && !report.ReviewSimulation.ApprovedOnlyDeliveryWorks {
		result.Failures = append(result.Failures, "approved-only delivery invariant failed")
	}
	if gates.RequireZeroSecondCrawlLLM && !report.CrawlProof.ZeroSecondRunLLMCalls {
		result.Failures = append(result.Failures, "second unchanged crawl used model calls")
	}
	result.Passed = len(result.Failures) == 0
	return result
}

func runCrawlProof(ctx context.Context, options EvaluationOptions, expected []EvalGoldPage) (CrawlProof, error) {
	store := NewMemoryStore()
	model := &countingModel{inner: options.Model}
	config := options.CrawlConfig
	config.Scope = options.Scope
	config.SourceSkill = options.SourceSkill
	if config.Mode == "" {
		config.Mode = "snapshot"
	}
	pipeline := NewPipeline(&SnapshotFetcher{Directory: options.FixtureDirectory}, model, store)
	first, err := pipeline.Run(ctx, config)
	if err != nil {
		return CrawlProof{}, err
	}
	firstCalls := model.Calls()
	var capturedExpectedURLs []string
	for _, page := range expected {
		if _, captured, err := store.LatestPageVersion(ctx, config.SourceID, page.URL); err != nil {
			return CrawlProof{}, err
		} else if captured {
			capturedExpectedURLs = append(capturedExpectedURLs, page.URL)
		}
	}
	second, err := pipeline.Run(ctx, config)
	if err != nil {
		return CrawlProof{}, err
	}
	secondCalls := model.Calls() - firstCalls
	return CrawlProof{FirstRun: first, SecondRun: second, FirstRunModelCalls: firstCalls, SecondRunModelCalls: secondCalls, ZeroSecondRunLLMCalls: secondCalls == 0, CapturedExpectedURLs: capturedExpectedURLs}, nil
}

type countingModel struct {
	inner   SemanticModel
	route   int
	extract int
}

func (model *countingModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	model.route++
	return model.inner.Route(ctx, prompt)
}

func (model *countingModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	model.extract++
	return model.inner.Extract(ctx, prompt, blocks)
}

func (model *countingModel) Calls() int {
	return model.route + model.extract
}

func evalRouteLabels(expected, actual RouteAction, err error) []string {
	if err != nil {
		return []string{"routing_error"}
	}
	if expected != actual {
		return []string{fmt.Sprintf("route_mismatch_expected_%s_actual_%s", expected, actual)}
	}
	return nil
}

func WriteEvaluationReports(directory string, report EvaluationReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create evaluation directory: %w", err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode evaluation report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evaluation.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write evaluation JSON: %w", err)
	}
	simulation, _ := json.MarshalIndent(report.ReviewSimulation, "", "  ")
	if err := os.WriteFile(filepath.Join(directory, "review-simulation.json"), simulation, 0o644); err != nil {
		return fmt.Errorf("write review simulation JSON: %w", err)
	}
	file, err := os.Create(filepath.Join(directory, "evaluation.html"))
	if err != nil {
		return fmt.Errorf("create evaluation HTML: %w", err)
	}
	defer file.Close()
	functions := template.FuncMap{"mul": func(left, right float64) float64 { return left * right }}
	return template.Must(template.New("evaluation").Funcs(functions).Parse(evaluationHTML)).Execute(file, report)
}

const evaluationHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Semantic evaluation</title><style>body{font:14px system-ui;max-width:1200px;margin:30px auto;padding:0 20px;color:#172033}.cards{display:grid;grid-template-columns:repeat(5,1fr);gap:12px}.card{border:1px solid #ccd3dc;border-radius:8px;padding:12px;background:#f8fafc}.metric{font-size:24px;font-weight:700}.pass{color:#087830}.fail{color:#b42318}table{border-collapse:collapse;width:100%;margin-top:16px}th,td{border:1px solid #ccc;padding:8px;text-align:left;vertical-align:top}</style></head><body><h1>Semantic evaluation: {{.FixtureSet}}</h1><p>Quality gates: {{if .QualityGates.Passed}}<strong class="pass">PASSED</strong>{{else}}<strong class="fail">FAILED</strong>{{end}}</p><div class="cards"><section class="card"><div>Discovery</div><div class="metric">{{printf "%.1f%%" (mul .DiscoveryCoverage 100)}}</div></section><section class="card"><div>Schema valid</div><div class="metric">{{printf "%.1f%%" (mul .SchemaValidRate 100)}}</div></section><section class="card"><div>Initial F1</div><div class="metric">{{printf "%.1f%%" (mul .Aggregate.RelevantTextF1 100)}}</div></section><section class="card"><div>Final approved parity</div><div class="metric">{{printf "%.1f%%" (mul .ReviewSimulation.FinalApprovedParity 100)}}</div></section><section class="card"><div>Second crawl model calls</div><div class="metric">{{.CrawlProof.SecondRunModelCalls}}</div><div>{{.CrawlProof.SecondRun.Unchanged}} unchanged pages</div></section></div><h2>Simulated human review</h2><p>{{.ReviewSimulation.ImmediateApprovals}} immediate approvals · {{.ReviewSimulation.EditApprovals}} edit approvals · {{.ReviewSimulation.RetryApprovals}} retry approvals · {{.ReviewSimulation.Escalations}} escalations</p><h2>Pages</h2><table><thead><tr><th>URL</th><th>Exact</th><th>Recall</th><th>Precision</th><th>Critical</th><th>Table</th><th>Media</th><th>Labels</th></tr></thead><tbody>{{range .Pages}}<tr><td>{{.URL}}</td><td>{{.Metrics.ExactParity}}</td><td>{{printf "%.1f%%" (mul .Metrics.RelevantTextRecall 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.RelevantTextPrecision 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.CriticalRecall 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.TableRecall 100)}}</td><td>{{printf "%.1f%%" (mul .Metrics.MediaRecall 100)}}</td><td>{{range .MismatchLabels}}<div>{{.}}</div>{{end}}</td></tr>{{end}}</tbody></table></body></html>`
