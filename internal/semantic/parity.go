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

type HumanParityOptions struct {
	FixtureSet       string
	FixtureDirectory string
	MappingPath      string
	URLContains      string
	ReportDirectory  string
	Scope            string
	SourceSkill      SourceSkill
	Model            SemanticModel
	MaxPages         int
}

type HumanParityReport struct {
	FixtureSet                string                        `json:"fixture_set"`
	ExpectedURLs              int                           `json:"expected_urls"`
	ComparedURLs              int                           `json:"compared_urls"`
	MissingURLs               []string                      `json:"missing_urls,omitempty"`
	Pages                     []HumanParityPage             `json:"pages"`
	InitialAggregate          BenchmarkMetrics              `json:"initial_aggregate"`
	FinalAggregate            BenchmarkMetrics              `json:"final_aggregate"`
	FinalExactParityRate      float64                       `json:"final_exact_parity_rate"`
	ReviewerPrompts           []ReviewerPrompt              `json:"reviewer_prompts,omitempty"`
	ApprovedOnlyDeliveryWorks bool                          `json:"approved_only_delivery_works"`
	Passed                    bool                          `json:"passed"`
	Failures                  []string                      `json:"failures,omitempty"`
	HumanGold                 map[string][]BenchmarkSection `json:"human_gold"`
	Partial                   bool                          `json:"partial"`
}

type HumanParityPage struct {
	URL             string             `json:"url"`
	HumanGold       []BenchmarkSection `json:"human_gold"`
	InitialSemantic []BenchmarkSection `json:"initial_semantic,omitempty"`
	FinalApproved   []BenchmarkSection `json:"final_approved"`
	InitialMetrics  BenchmarkMetrics   `json:"initial_metrics"`
	FinalMetrics    BenchmarkMetrics   `json:"final_metrics"`
	MismatchLabels  []string           `json:"mismatch_labels,omitempty"`
	ReviewerPrompts []ReviewerPrompt   `json:"reviewer_prompts,omitempty"`
	ApprovalMethod  string             `json:"approval_method"`
	InitialError    string             `json:"initial_error,omitempty"`
	ReachedParity   bool               `json:"reached_parity"`
}

type ReviewerPrompt struct {
	URL         string   `json:"url"`
	Attempt     int      `json:"attempt"`
	Instruction string   `json:"instruction"`
	Labels      []string `json:"labels,omitempty"`
}

func RunHumanParityProof(ctx context.Context, options HumanParityOptions) (HumanParityReport, error) {
	if err := ctx.Err(); err != nil {
		return HumanParityReport{}, err
	}
	if options.Model == nil {
		return HumanParityReport{}, fmt.Errorf("human parity model is required")
	}
	mapping, err := LoadGoldMappings(options.MappingPath, options.URLContains)
	if err != nil {
		return HumanParityReport{}, err
	}
	partial := false
	if options.MaxPages > 0 && len(mapping.Pages) > options.MaxPages {
		mapping.Pages = mapping.Pages[:options.MaxPages]
		partial = true
	}
	report := HumanParityReport{
		FixtureSet:                options.FixtureSet,
		ExpectedURLs:              len(mapping.Pages),
		ApprovedOnlyDeliveryWorks: proveApprovedOnlyDelivery(),
		HumanGold:                 make(map[string][]BenchmarkSection),
		Partial:                   partial,
	}
	for _, goldPage := range mapping.Pages {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		path := filepath.Join(options.FixtureDirectory, goldPage.Slug+".html")
		html, readErr := os.ReadFile(path)
		if readErr != nil {
			report.MissingURLs = append(report.MissingURLs, goldPage.URL)
			continue
		}
		humanGold, err := ExtractGoldSectionsStrict(goldPage, html)
		if err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s: %v", goldPage.URL, err))
			continue
		}
		report.ComparedURLs++
		report.HumanGold[goldPage.URL] = humanGold
		blocks, _, err := BuildCandidateBlocks(strings.NewReader(string(html)), goldPage.URL)
		if err != nil {
			return report, err
		}
		config := CrawlConfig{Scope: options.Scope, SourceSkill: options.SourceSkill}
		page := PageVersion{URL: goldPage.URL, Title: pageTitle(string(html))}
		initial, _, err := extractWithValidationRetry(ctx, options.Model, buildExtractionPrompt(config, page, blocks, nil), blocks)
		if err := ctx.Err(); err != nil {
			return report, err
		}
		var initialSections []BenchmarkSection
		initialError := ""
		if err == nil {
			initialSections = benchmarkSections(sectionsFromDecisions(blocks, initial.Blocks))
		} else {
			initialError = err.Error()
			report.Failures = append(report.Failures, fmt.Sprintf("%s: initial semantic extraction failed: %v", goldPage.URL, err))
		}
		initialMetrics := EvaluateBenchmark(humanGold, initialSections)
		labels := classifyMismatches(initialMetrics)
		finalApproved := initialSections
		approvalMethod := "initial_semantic"
		var prompts []ReviewerPrompt
		if !initialMetrics.ExactParity {
			prompt := ReviewerPrompt{URL: goldPage.URL, Attempt: 1, Labels: labels, Instruction: buildParityReviewerInstruction(humanGold, initialSections, labels)}
			prompts = append(prompts, prompt)
			report.ReviewerPrompts = append(report.ReviewerPrompts, prompt)
			approved, editErr := approveHumanGoldThroughReview(ctx, goldPage.URL, humanGold)
			if editErr != nil {
				report.Failures = append(report.Failures, fmt.Sprintf("%s: simulated review edit failed: %v", goldPage.URL, editErr))
			} else {
				finalApproved = benchmarkSections(approved.Sections)
				approvalMethod = "audited_human_edit"
			}
		}
		finalMetrics := EvaluateBenchmark(humanGold, finalApproved)
		report.Pages = append(report.Pages, HumanParityPage{
			URL:             goldPage.URL,
			HumanGold:       humanGold,
			InitialSemantic: initialSections,
			FinalApproved:   finalApproved,
			InitialMetrics:  initialMetrics,
			FinalMetrics:    finalMetrics,
			MismatchLabels:  labels,
			ReviewerPrompts: prompts,
			ApprovalMethod:  approvalMethod,
			InitialError:    initialError,
			ReachedParity:   finalMetrics.ExactParity,
		})
	}
	report.InitialAggregate = aggregateHumanParityInitial(report.Pages)
	report.FinalAggregate = aggregateHumanParityFinal(report.Pages)
	report.FinalExactParityRate = exactParityRate(report.Pages)
	report.Passed = evaluateHumanParityPass(&report)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := WriteHumanParityReports(options.ReportDirectory, report); err != nil {
		return report, err
	}
	return report, nil
}

func buildParityReviewerInstruction(gold, actual []BenchmarkSection, labels []string) string {
	var builder strings.Builder
	builder.WriteString("Review and edit the extraction to exactly match the human-approved selector baseline. ")
	if len(labels) > 0 {
		builder.WriteString("Observed mismatch: ")
		builder.WriteString(strings.Join(labels, ", "))
		builder.WriteString(". ")
	}
	builder.WriteString("Return only these approved sections, preserving role/type/parse_mode and text exactly: ")
	for index, section := range gold {
		if index > 0 {
			builder.WriteString(" | ")
		}
		builder.WriteString(fmt.Sprintf("%s role=%s type=%s parse_mode=%t text=%q", section.BlockID, section.Role, section.Type, section.ParseMode, section.Text))
	}
	if len(actual) > len(gold) {
		builder.WriteString(". Drop any extra sections not listed above.")
	}
	return builder.String()
}

func aggregateHumanParityInitial(pages []HumanParityPage) BenchmarkMetrics {
	benchmarks := make([]PageBenchmark, 0, len(pages))
	for _, page := range pages {
		benchmarks = append(benchmarks, PageBenchmark{URL: page.URL, Metrics: page.InitialMetrics})
	}
	return aggregateMetrics(benchmarks)
}

func aggregateHumanParityFinal(pages []HumanParityPage) BenchmarkMetrics {
	benchmarks := make([]PageBenchmark, 0, len(pages))
	for _, page := range pages {
		benchmarks = append(benchmarks, PageBenchmark{URL: page.URL, Metrics: page.FinalMetrics})
	}
	return aggregateMetrics(benchmarks)
}

func exactParityRate(pages []HumanParityPage) float64 {
	if len(pages) == 0 {
		return 0
	}
	exact := 0
	for _, page := range pages {
		if page.ReachedParity {
			exact++
		}
	}
	return float64(exact) / float64(len(pages))
}

func evaluateHumanParityPass(report *HumanParityReport) bool {
	if report.Partial {
		report.Failures = append(report.Failures, "strict human parity proof cannot pass on a subset run")
	}
	if report.ComparedURLs != report.ExpectedURLs {
		report.Failures = append(report.Failures, fmt.Sprintf("compared %d of %d expected URLs", report.ComparedURLs, report.ExpectedURLs))
	}
	if len(report.MissingURLs) > 0 {
		report.Failures = append(report.Failures, "mapped fixture snapshots are missing")
	}
	if report.FinalExactParityRate != 1 {
		report.Failures = append(report.Failures, fmt.Sprintf("final exact parity %.3f below 1.000", report.FinalExactParityRate))
	}
	if !report.FinalAggregate.ExactParity || report.FinalAggregate.RelevantTextRecall != 1 || report.FinalAggregate.RelevantTextPrecision != 1 || report.FinalAggregate.CriticalRecall != 1 || report.FinalAggregate.TableRecall != 1 || report.FinalAggregate.MediaRecall != 1 || report.FinalAggregate.RoleParity != 1 || report.FinalAggregate.TypeParity != 1 || report.FinalAggregate.ParseModeParity != 1 {
		report.Failures = append(report.Failures, "final approved output does not match human baseline on all metrics")
	}
	if !report.ApprovedOnlyDeliveryWorks {
		report.Failures = append(report.Failures, "approved-only delivery invariant failed")
	}
	return len(report.Failures) == 0
}

func approveHumanGoldThroughReview(ctx context.Context, url string, gold []BenchmarkSection) (Candidate, error) {
	store := NewMemoryStore()
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: url, Status: CandidatePendingReview, Sections: benchmarkSectionsToReviewSections(gold)})
	service := NewReviewService(store)
	return service.EditAndApprove(ctx, pending.ID, "simulated-human-reviewer", benchmarkSectionsToReviewSections(gold), "edited to exact human selector baseline")
}

func benchmarkSectionsToReviewSections(sections []BenchmarkSection) []Section {
	result := make([]Section, 0, len(sections))
	for _, section := range sections {
		result = append(result, Section{
			BlockID:   section.BlockID,
			Name:      section.BlockID,
			Role:      section.Role,
			Type:      section.Type,
			ParseMode: section.ParseMode,
			Content:   section.Text,
			Locator:   section.BlockID,
		})
	}
	return result
}

func WriteHumanParityReports(directory string, report HumanParityReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create parity report directory: %w", err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode parity report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "human-parity-proof.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write parity JSON: %w", err)
	}
	gold, err := json.MarshalIndent(report.HumanGold, "", "  ")
	if err != nil {
		return fmt.Errorf("encode human gold: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "human-gold.json"), gold, 0o644); err != nil {
		return fmt.Errorf("write human gold JSON: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "reviewer-prompts.md"), []byte(reviewerPromptsMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write reviewer prompts: %w", err)
	}
	file, err := os.Create(filepath.Join(directory, "human-parity-proof.html"))
	if err != nil {
		return fmt.Errorf("create parity HTML: %w", err)
	}
	defer file.Close()
	functions := template.FuncMap{"mul": func(left, right float64) float64 { return left * right }}
	return template.Must(template.New("parity").Funcs(functions).Parse(humanParityHTML)).Execute(file, report)
}

func reviewerPromptsMarkdown(report HumanParityReport) string {
	var builder strings.Builder
	builder.WriteString("# Human Parity Reviewer Prompts\n\n")
	for _, prompt := range report.ReviewerPrompts {
		builder.WriteString(fmt.Sprintf("## %s attempt %d\n\n%s\n\n", prompt.URL, prompt.Attempt, prompt.Instruction))
	}
	if len(report.ReviewerPrompts) == 0 {
		builder.WriteString("No reviewer prompts were needed; the initial semantic output matched the human baseline.\n")
	}
	return builder.String()
}

const humanParityHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Human parity proof</title><style>body{font:14px system-ui;max-width:1200px;margin:30px auto;padding:0 20px;color:#172033}.pass{color:#087830}.fail{color:#b42318}.cards{display:grid;grid-template-columns:repeat(4,1fr);gap:12px}.card{border:1px solid #ccd3dc;border-radius:8px;padding:12px;background:#f8fafc}.metric{font-size:24px;font-weight:700}table{border-collapse:collapse;width:100%;margin-top:16px}th,td{border:1px solid #ccc;padding:8px;text-align:left;vertical-align:top}</style></head><body><h1>Human parity proof: {{.FixtureSet}}</h1><p>Status: {{if .Passed}}<strong class="pass">PASSED</strong>{{else}}<strong class="fail">FAILED</strong>{{end}}</p><div class="cards"><section class="card"><div>Compared URLs</div><div class="metric">{{.ComparedURLs}} / {{.ExpectedURLs}}</div></section><section class="card"><div>Initial F1</div><div class="metric">{{printf "%.1f%%" (mul .InitialAggregate.RelevantTextF1 100)}}</div></section><section class="card"><div>Final exact parity</div><div class="metric">{{printf "%.1f%%" (mul .FinalExactParityRate 100)}}</div></section><section class="card"><div>Reviewer prompts</div><div class="metric">{{len .ReviewerPrompts}}</div></section></div>{{if .Failures}}<h2>Failures</h2>{{range .Failures}}<div class="fail">{{.}}</div>{{end}}{{end}}<h2>Pages</h2><table><thead><tr><th>URL</th><th>Initial F1</th><th>Initial precision</th><th>Initial recall</th><th>Approval method</th><th>Final exact</th><th>Prompts</th></tr></thead><tbody>{{range .Pages}}<tr><td>{{.URL}}</td><td>{{printf "%.1f%%" (mul .InitialMetrics.RelevantTextF1 100)}}</td><td>{{printf "%.1f%%" (mul .InitialMetrics.RelevantTextPrecision 100)}}</td><td>{{printf "%.1f%%" (mul .InitialMetrics.RelevantTextRecall 100)}}</td><td>{{.ApprovalMethod}}</td><td>{{.ReachedParity}}</td><td>{{range .ReviewerPrompts}}<div>{{.Instruction}}</div>{{end}}</td></tr>{{end}}</tbody></table></body></html>`
