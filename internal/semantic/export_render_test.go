package semantic

import (
	"strings"
	"testing"
)

func TestExportRenderCompleteRetainsDetailsAndLegacy(t *testing.T) {
	s, p, c := exportExample()
	c.Sections = append(c.Sections, Section{BlockID: "media", Role: RoleMedia, Content: `<figure><img src="https://example.test/chart.png" alt="Fee chart"><figcaption>Fees per month</figcaption></figure>`}, Section{BlockID: "table", Role: RoleTable, Content: `<table><tr><th>Service</th><th>SEK/month</th></tr><tr><td>Permit</td><td>25</td></tr></table>`})
	d, _ := BuildExportDocument(s, p, c)
	for _, f := range []ExportFormat{ExportMarkdown, ExportText} {
		r, err := RenderExportDocument(d, f, ProfileComplete)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []string{"except students", "31 October", "Fee chart", "Fees per month", "SEK/month", "25"} {
			if !strings.Contains(string(r.Content), v) {
				t.Errorf("%s missing %s: %s", f, v, r.Content)
			}
		}
	}
	r, err := RenderExportDocument(d, ExportMarkdown, ProfileLegacy)
	if err != nil || string(r.Content) != "legacy" {
		t.Fatalf("legacy changed %s %v", r.Content, err)
	}
}
func TestExportRenderDraftAndUnsafeContent(t *testing.T) {
	s, p, c := exportExample()
	c.Status = CandidatePendingReview
	c.ApprovedAt = nil
	c.Sections = []Section{{Content: `<p>Keep this exception.</p><script>alert(1)</script><a href="javascript:alert(1)">Bad link</a><video src="https://example.test/video.mp4"></video>`}}
	d, _ := BuildExportDocument(s, p, c)
	r, err := RenderExportDocument(d, ExportMarkdown, ProfileComplete)
	if err != nil {
		t.Fatal(err)
	}
	text := string(r.Content)
	for _, bad := range []string{"<script", "javascript:", "<video", "alert(1)"} {
		if strings.Contains(text, bad) {
			t.Errorf("unsafe output: %s", text)
		}
	}
	for _, want := range []string{"UNREVIEWED", "Keep this exception", "https://example.test/video.mp4"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s: %s", want, text)
		}
	}
	if len(r.Warnings) == 0 {
		t.Fatal("missing conversion warning")
	}
}
func TestExtractionSchemaSeparatesSelectionFromRendering(t *testing.T) {
	schema := extractionJSONSchema()
	props := schema["properties"].(map[string]any)
	block := props["blocks"].(map[string]any)["items"].(map[string]any)
	if _, ok := block["properties"].(map[string]any)["parse_mode"]; ok {
		t.Fatal("model still controls rendering")
	}
}

func TestExportRenderNestedMediaRetainsSourceAndFallback(t *testing.T) {
	s, p, c := exportExample()
	c.Sections = []Section{{BlockID: "video", Content: `<video controls><source src="https://example.test/demo.mp4" type="video/mp4">Transcript: apply before 31 October.</video>`}}
	d, _ := BuildExportDocument(s, p, c)
	result, err := RenderExportDocument(d, ExportMarkdown, ProfileComplete)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://example.test/demo.mp4", "Transcript: apply before 31 October."} {
		if !strings.Contains(string(result.Content), want) {
			t.Errorf("lost %s: %s", want, result.Content)
		}
	}
}

func TestExportMarkdownPreservesTableRelationships(t *testing.T) {
	s, p, c := exportExample()
	c.Sections = []Section{{BlockID: "table", Role: RoleTable, Content: `<table><tr><th>Plan</th><th>Fee</th></tr><tr><td>Basic</td><td>10 EUR</td></tr><tr><td>Pro</td><td>20 EUR</td></tr></table>`}}
	d, _ := BuildExportDocument(s, p, c)
	r, err := RenderExportDocument(d, ExportMarkdown, ProfileComplete)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(r.Content), "\n")
	for _, pair := range [][2]string{{"Plan", "Fee"}, {"Basic", "10 EUR"}, {"Pro", "20 EUR"}} {
		found := false
		for _, line := range lines {
			if strings.Contains(line, "|") && strings.Contains(line, pair[0]) && strings.Contains(line, pair[1]) {
				found = true
			}
		}
		if !found {
			t.Errorf("lost table row %v: %s", pair, r.Content)
		}
	}
}
func TestExportRendererPreservesSubstantiveNotation(t *testing.T) {
	s, p, c := exportExample()
	c.Sections = []Section{{Content: `<p>Price <del>100</del> <ins>200</ins> EUR. Scale 10<sup>6</sup>, H<sub>2</sub>O.</p><dl><dt>Retry</dt><dd>Wait 750 ms</dd><dt>Limit</dt><dd>20 attempts</dd></dl>`}}
	d, _ := BuildExportDocument(s, p, c)
	for _, f := range []ExportFormat{ExportMarkdown, ExportText} {
		r, err := RenderExportDocument(d, f, ProfileComplete)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"deleted: 100", "inserted: 200", "^(6)", "_(2)"} {
			if !strings.Contains(string(r.Content), want) {
				t.Errorf("%s lost %s: %s", f, want, r.Content)
			}
		}
		if len(r.Warnings) == 0 {
			t.Fatal("notation conversion missing warning")
		}
		if strings.Contains(string(r.Content), "RetryWait") || strings.Contains(string(r.Content), "msLimit") {
			t.Fatal("definition-list boundaries lost")
		}
	}
}
