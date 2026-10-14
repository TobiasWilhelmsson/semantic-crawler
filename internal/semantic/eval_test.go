package semantic

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRunEvaluationProducesPortableQualityGateReport(t *testing.T) {
	skill, err := LoadSourceSkill(filepath.Join("..", "..", "configs", "sources", "source-skills", "generic-health-sample.md"), 1)
	if err != nil {
		t.Fatal(err)
	}
	report, err := RunEvaluation(context.Background(), EvaluationOptions{
		FixtureDirectory: filepath.Join("..", "..", "tests", "fixtures", "generic-health-sample"),
		GoldPath:         filepath.Join("..", "..", "tests", "fixtures", "generic-health-sample", "gold.json"),
		ReportDirectory:  t.TempDir(),
		Scope:            "clinical airway care",
		SourceSkill:      skill,
		Model:            &FixtureModel{},
		CrawlConfig: CrawlConfig{
			SourceID:          2,
			SeedURL:           "https://guidelines.example.test/airway/",
			AllowedHost:       "guidelines.example.test",
			AllowedPathPrefix: "/airway/",
			MaxDepth:          2,
			MaxPages:          10,
			SkillVersion:      1,
			Mode:              "snapshot",
			Model:             "fixture",
			PromptVersion:     "test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.QualityGates.Passed {
		t.Fatalf("expected quality gates to pass: %#v", report.QualityGates)
	}
	if report.ExpectedURLs != 4 || report.DiscoveredURLs != 4 || report.SchemaValidRate != 1 {
		t.Fatalf("unexpected evaluation counts: %#v", report)
	}
	if report.SourceSkillHash == "" || report.SourceSkillVersion != 1 {
		t.Fatalf("source skill audit metadata missing: %#v", report)
	}
	if report.ReviewSimulation.FinalApprovedParity != 1 || !report.ReviewSimulation.ApprovedOnlyDeliveryWorks {
		t.Fatalf("review simulation did not prove final approved parity: %#v", report.ReviewSimulation)
	}
	if !report.CrawlProof.ZeroSecondRunLLMCalls || report.CrawlProof.SecondRunModelCalls != 0 || report.CrawlProof.SecondRun.Unchanged != 4 {
		t.Fatalf("crawl proof did not show unchanged hash skip: %#v", report.CrawlProof)
	}
}

func TestSimulateGoldReviewCreatesInstructionsForSafetyCriticalMisses(t *testing.T) {
	pages := []PageBenchmark{{
		URL: "https://example.test/a",
		Expected: []BenchmarkSection{{
			Role:      RoleInfoBox,
			Type:      TypeCritical,
			ParseMode: false,
			Text:      "Critical warning",
		}},
		Metrics: BenchmarkMetrics{
			RelevantTextRecall:    0.9,
			RelevantTextPrecision: 0.9,
			RelevantTextF1:        0.9,
			CriticalRecall:        0,
			TableRecall:           1,
			MediaRecall:           1,
		},
	}}
	report := SimulateGoldReview(pages)
	if report.RetryApprovals != 1 || report.Escalations != 1 || len(report.InstructionsCreated) != 1 || report.FinalApprovedParity != 1 {
		t.Fatalf("unexpected simulated review report: %#v", report)
	}
}

func TestSimulateGoldReviewDoesNotClaimParityWithoutGold(t *testing.T) {
	report := SimulateGoldReview([]PageBenchmark{{
		URL: "https://example.test/missing-gold",
		Metrics: BenchmarkMetrics{
			RelevantTextRecall:    0.4,
			RelevantTextPrecision: 0.4,
			CriticalRecall:        0,
			TableRecall:           1,
			MediaRecall:           1,
		},
	}})
	if report.FinalApprovedParity != 0 || report.RetryApprovals != 0 || report.Escalations != 1 {
		t.Fatalf("review simulation overclaimed repair without gold: %#v", report)
	}
}
