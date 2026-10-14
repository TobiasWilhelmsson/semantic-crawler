package semantic

import (
	"strings"
	"testing"
)

func TestCandidateBlocksKeepMediaInEmptyContainers(t *testing.T) {
	for _, media := range []string{`<video src="/fees.mp4"></video>`, `<img src="/fees.png" alt="Fee chart">`, `<audio src="/fees.mp3"></audio>`} {
		for _, wrapper := range []string{"p", "aside", "blockquote"} {
			for _, text := range []string{"", "Fee details:"} {
				source := "<main><" + wrapper + ">" + text + media + "</" + wrapper + "></main>"
				blocks, _, err := BuildCandidateBlocks(strings.NewReader(source), "https://example.test")
				if err != nil {
					t.Fatal(err)
				}
				if len(blocks) != 1 || !strings.Contains(blocks[0].HTML, "https://example.test/fees.") {
					t.Errorf("lost or duplicated media for %s: %#v", source, blocks)
				}
			}
		}
	}
}

func TestExportPreservesOrderedListLabels(t *testing.T) {
	for _, tc := range []struct {
		name, html string
		labels     []string
	}{
		{"start", `<ol start="5"><li>Pay 20 EUR</li><li>Submit receipt</li></ol>`, []string{"5", "6"}},
		{"value", `<ol><li value="5">Pay 20 EUR</li><li value="9">Submit receipt</li></ol>`, []string{"5", "9"}},
		{"reversed", `<ol reversed><li>Pay 20 EUR</li><li>Submit receipt</li></ol>`, []string{"2", "1"}},
	} {
		for _, format := range []ExportFormat{ExportMarkdown, ExportText} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				d := ExportDocument{Status: CandidateApproved, Sections: []ExportSection{{HTML: tc.html}}}
				r, err := RenderExportDocument(d, format, ProfileComplete)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(string(r.Content), "\n")
				for i, phrase := range []string{"Pay 20 EUR", "Submit receipt"} {
					found := false
					for _, line := range lines {
						if strings.Contains(line, phrase) && (strings.HasPrefix(strings.TrimSpace(line), tc.labels[i]+". ") || strings.Contains(line, "["+tc.labels[i]+"]")) {
							found = true
						}
					}
					if !found {
						t.Errorf("lost source label %s for %s: %s", tc.labels[i], phrase, r.Content)
					}
				}
			})
		}
	}
}

func TestExportWarnsInsteadOfFlatteningMathML(t *testing.T) {
	raw := `<p>Amount <math><mn>20</mn><mo>×</mo><msup><mn>10</mn><mn>6</mn></msup></math> EUR</p>`
	for _, format := range []ExportFormat{ExportMarkdown, ExportText} {
		d := ExportDocument{Status: CandidateApproved, Sections: []ExportSection{{BlockID: "formula", HTML: raw}}}
		r, err := RenderExportDocument(d, format, ProfileComplete)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(r.Content), "20×106") || !strings.Contains(string(r.Content), "MathML") {
			t.Errorf("formula misrepresented: %s", r.Content)
		}
		if !strings.Contains(strings.Join(r.Warnings, " "), "MathML") {
			t.Errorf("missing formula warning: %#v", r.Warnings)
		}
		if d.Sections[0].HTML != raw {
			t.Fatal("changed structured source HTML")
		}
	}
}
