package semantic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectorHealthReportsMatchedMissingEmptyAndFetchFailures(t *testing.T) {
	directory := t.TempDir()
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[
			{"name":"title","role":"header","container":"h1","parse_mode":true},
			{"name":"empty","role":"content","container":".empty","parse_mode":true},
			{"name":"missing","role":"content","container":"#missing","parse_mode":true}
		]},
		{"url":"https://example.test/topic/b/","identifier":[
			{"name":"body","role":"content","container":"main","parse_mode":true}
		]}
	]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := mapFetcher{"https://example.test/topic/a/": `<html><body><main><h1>A</h1><p class="empty">   </p></main></body></html>`}

	report, err := RunSelectorHealth(context.Background(), SelectorHealthOptions{
		MappingPath:     filepath.Join(directory, "mapping.json"),
		URLContains:     "/topic/",
		Fetcher:         fetcher,
		ReportDirectory: filepath.Join(directory, "reports"),
	})
	if err != nil {
		t.Fatalf("selector health: %v", err)
	}
	if report.TotalURLs != 2 || report.TotalIdentifiers != 4 || report.MatchedSelectors != 1 || report.EmptySelectors != 1 || report.MissingSelectors != 1 || report.FetchFailedPages != 1 {
		t.Fatalf("unexpected selector counts: %#v", report)
	}
	if report.Pages[0].Status != SelectorHealthPartialGold || report.Pages[1].Status != SelectorHealthFetchFailed {
		t.Fatalf("unexpected page statuses: %#v", report.Pages)
	}
	if report.Pages[0].Identifiers[2].Selector != "#missing" || report.Pages[0].Identifiers[2].Status != SelectorIdentifierMissing {
		t.Fatalf("missing selector was not reported: %#v", report.Pages[0].Identifiers)
	}
	if _, err := os.Stat(filepath.Join(directory, "reports", "selector-health.json")); err != nil {
		t.Fatalf("selector health JSON missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "reports", "selector-health.html")); err != nil {
		t.Fatalf("selector health HTML missing: %v", err)
	}
}

func TestSelectorHealthClassifiesRedirectedURL(t *testing.T) {
	directory := t.TempDir()
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := redirectFetcher{result: FetchResult{URL: "https://example.test/topic/new-a/", StatusCode: 200, HTML: `<html><body><h1>A</h1></body></html>`}}

	report, err := RunSelectorHealth(context.Background(), SelectorHealthOptions{MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", Fetcher: fetcher})
	if err != nil {
		t.Fatalf("selector health: %v", err)
	}
	if report.RedirectedPages != 1 || report.Pages[0].Status != SelectorHealthRedirectOrURLChanged {
		t.Fatalf("redirect was not classified: %#v", report)
	}
}

func TestSelectorHealthClassifiesHTTPErrorAsFetchFailed(t *testing.T) {
	directory := t.TempDir()
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := redirectFetcher{result: FetchResult{URL: "https://example.test/topic/a/", StatusCode: 429, HTML: `<html><body>Too many requests</body></html>`}}

	report, err := RunSelectorHealth(context.Background(), SelectorHealthOptions{MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", Fetcher: fetcher})
	if err != nil {
		t.Fatalf("selector health: %v", err)
	}
	if report.FetchFailedPages != 1 || report.ZeroGoldPages != 0 || report.Pages[0].Status != SelectorHealthFetchFailed {
		t.Fatalf("HTTP error must not be treated as zero gold: %#v", report)
	}
}

func TestBenchmarkRunnerTreatsHTTPErrorAsMissingURL(t *testing.T) {
	directory := t.TempDir()
	mapping := `{"urls":[{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}]}`
	if err := os.WriteFile(filepath.Join(directory, "mapping.json"), []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := redirectFetcher{result: FetchResult{URL: "https://example.test/topic/a/", StatusCode: 429, HTML: `<html><body>Too many requests</body></html>`}}

	report, err := RunBenchmark(context.Background(), BenchmarkOptions{FixtureSet: "test", MappingPath: filepath.Join(directory, "mapping.json"), URLContains: "/topic/", ReportDirectory: filepath.Join(directory, "reports"), Model: &FixtureModel{}, Fetcher: fetcher})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if len(report.MissingURLs) != 1 || report.Pages[0].BaselineStatus != BaselineFetchFailed || report.Aggregate.GoldSections != 0 || report.ReviewerPages != 0 {
		t.Fatalf("HTTP error must be reported as missing/fetch failed: %#v", report)
	}
}

type redirectFetcher struct {
	result FetchResult
}

func (fetcher redirectFetcher) Fetch(context.Context, string) (FetchResult, error) {
	return fetcher.result, nil
}
