package semantic

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluationUsesActualDiscoveryCoverage(t *testing.T) {
	fixtures := filepath.Join("..", "..", "tests", "fixtures", "generic-health-sample")
	report, err := RunEvaluation(context.Background(), EvaluationOptions{
		FixtureDirectory: fixtures, GoldPath: filepath.Join(fixtures, "gold.json"), ReportDirectory: t.TempDir(), Model: &FixtureModel{},
		CrawlConfig: CrawlConfig{SourceID: 2, SeedURL: "https://guidelines.example.test/airway/", AllowedHost: "guidelines.example.test", AllowedPathPrefix: "/airway/", MaxDepth: 0, MaxPages: 1, Mode: "snapshot"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.DiscoveryCoverage == 1 || report.QualityGates.Passed {
		t.Fatalf("evaluation accepted incomplete crawl: expected=%d claimedDiscovered=%d actualDiscovered=%d coverage=%v passed=%t", report.ExpectedURLs, report.DiscoveredURLs, report.CrawlProof.FirstRun.Discovered, report.DiscoveryCoverage, report.QualityGates.Passed)
	}
}

func TestEvaluationDiscoveryMatchesExpectedURLs(t *testing.T) {
	fixtures := filepath.Join("..", "..", "tests", "fixtures", "generic-health-sample")
	gold, err := os.ReadFile(filepath.Join(fixtures, "gold.json"))
	if err != nil {
		t.Fatal(err)
	}
	gold = bytes.ReplaceAll(gold, []byte("https://guidelines.example.test/airway/preparation/"), []byte("https://guidelines.example.test/airway/not-linked/"))
	goldPath := filepath.Join(t.TempDir(), "gold.json")
	if err := os.WriteFile(goldPath, gold, 0644); err != nil {
		t.Fatal(err)
	}
	report, err := RunEvaluation(context.Background(), EvaluationOptions{
		FixtureDirectory: fixtures, GoldPath: goldPath, ReportDirectory: t.TempDir(), Model: &FixtureModel{},
		CrawlConfig: CrawlConfig{SourceID: 2, SeedURL: "https://guidelines.example.test/airway/", AllowedHost: "guidelines.example.test", AllowedPathPrefix: "/airway/", MaxDepth: 2, MaxPages: 10, Mode: "snapshot"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.CrawlProof.FirstRun.Discovered != 4 || report.DiscoveredURLs != 3 || report.DiscoveryCoverage != 0.75 || report.QualityGates.Passed {
		t.Fatalf("unexpected URLs replaced missing expected URL: actual=%d expectedCaptured=%d coverage=%v passed=%t", report.CrawlProof.FirstRun.Discovered, report.DiscoveredURLs, report.DiscoveryCoverage, report.QualityGates.Passed)
	}
}
