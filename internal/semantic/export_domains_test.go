package semantic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportPreservesIndependentCrossDomainDetails(t *testing.T) {
	cases := []struct {
		file    string
		details []string
	}{
		{"public-service", []string{"31 October 2026", "except emancipated applicants", "not permission to begin work", "Fee (SEK/year)", "250", "Student", "permits@example.test"}},
		{"technical", []string{"except 2.5.0", "750 ms, not 750 seconds", "--dry-run", "Do not delete", "seven years"}},
		{"commercial", []string{"EUR 19.50", "excluding VAT", "30 days' notice", "25,000 SEK", "excludes temporary partners", "1 January 2027", "https://example.test/images/tiers.svg"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("../../tests/fixtures/domain-neutral", tc.file+".html"))
			if err != nil {
				t.Fatal(err)
			}
			u := "https://example.test/" + tc.file
			store := NewMemoryStore()
			config := CrawlConfig{SourceID: 1, SeedURL: u, AllowedHost: "example.test", Scope: "All substantive content including notices and exceptions", MaxPages: 1}
			summary, err := NewPipeline(mapFetcher{u: string(body)}, &FixtureModel{}, store).Run(context.Background(), config)
			if err != nil || summary.PendingReview != 1 {
				t.Fatalf("scan %#v %v", summary, err)
			}
			candidate := store.Candidates()[0]
			page, err := store.PageVersionByID(context.Background(), candidate.PageVersionID)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := BuildExportDocument(SourceEntry{Slug: tc.file, Config: SourceConfig{SourceID: 1}}, page, candidate)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []ExportFormat{ExportMarkdown, ExportText} {
				rendered, err := RenderExportDocument(doc, format, ProfileComplete)
				if err != nil {
					t.Fatal(err)
				}
				for _, detail := range tc.details {
					if !strings.Contains(string(rendered.Content), detail) {
						t.Errorf("%s lost %q: %s", format, detail, rendered.Content)
					}
				}
			}
		})
	}
}
