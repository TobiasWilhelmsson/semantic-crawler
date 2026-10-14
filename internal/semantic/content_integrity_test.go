package semantic

import (
	"strings"
	"testing"
)

func TestContentHashDetectsMovedTableValues(t *testing.T) {
	oldPage := `<main><table><tr><th>Morning</th><th>Evening</th></tr><tr><td>5 mg</td><td></td></tr></table></main>`
	newPage := `<main><table><tr><th>Morning</th><th>Evening</th></tr><tr><td></td><td>5 mg</td></tr></table></main>`
	before, _, err := BuildCandidateBlocks(strings.NewReader(oldPage), "https://example.test/schedule/")
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := BuildCandidateBlocks(strings.NewReader(newPage), "https://example.test/schedule/")
	if err != nil {
		t.Fatal(err)
	}
	if ContentHash(before) == ContentHash(after) {
		t.Fatal("moving a value between labeled table cells must invalidate cached extraction")
	}
}

func TestCandidateBlocksPreserveNestedListContentOnce(t *testing.T) {
	page := `<main><ol start="3"><li><p>First unique step.</p><ul><li>Nested unique detail.</li></ul></li></ol></main>`
	blocks, _, err := BuildCandidateBlocks(strings.NewReader(page), "https://example.test/steps/")
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	for _, block := range blocks {
		content.WriteString(block.Text + " ")
	}
	for _, text := range []string{"First unique step.", "Nested unique detail."} {
		if got := strings.Count(content.String(), text); got != 1 {
			t.Fatalf("source content %q occurs %d times in overlapping blocks", text, got)
		}
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0].HTML, `start="3"`) || !strings.Contains(blocks[0].HTML, "<ul>") {
		t.Fatalf("list structure was not preserved in one source block: %#v", blocks)
	}
}

func TestEmbeddedContentHasDeterministicOrderAndHash(t *testing.T) {
	page := `<main></main><script>window.__PRELOADED_STATE__={"zeta":{"BlockTypeName":"ChapterBlock","ContentString":"<p>Zebra policy applies after sunset.</p>"},"alpha":{"BlockTypeName":"ChapterBlock","ContentString":"<p>Antelope preparation begins before dawn.</p>"}};</script>`
	first, firstSnapshot, err := BuildCandidateBlocks(strings.NewReader(page), "https://example.test/chapters/")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("expected both distinct embedded chapters: %#v", first)
	}
	for index := 0; index < 100; index++ {
		blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(page), "https://example.test/chapters/")
		if err != nil {
			t.Fatal(err)
		}
		if ContentHash(blocks) != ContentHash(first) || snapshot != firstSnapshot {
			t.Fatal("identical source produced different block IDs/order, snapshots or hashes")
		}
	}
}

func TestContentHashDetectsSemanticFormattingAndListNumbering(t *testing.T) {
	for _, test := range []struct{ name, before, after string }{
		{"warning class", `<aside class="info">Keep source text.</aside>`, `<aside class="warning">Keep source text.</aside>`},
		{"ordered list start", `<ol start="1"><li>Keep source text.</li></ol>`, `<ol start="3"><li>Keep source text.</li></ol>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldBlocks, _, err := BuildCandidateBlocks(strings.NewReader("<main>"+test.before+"</main>"), "https://example.test/page/")
			if err != nil {
				t.Fatal(err)
			}
			newBlocks, _, err := BuildCandidateBlocks(strings.NewReader("<main>"+test.after+"</main>"), "https://example.test/page/")
			if err != nil {
				t.Fatal(err)
			}
			if ContentHash(oldBlocks) == ContentHash(newBlocks) {
				t.Fatal("source semantics changed without invalidating extraction")
			}
		})
	}
}

func FuzzCandidateBlocksAreStable(f *testing.F) {
	for _, seed := range []string{
		`<main><h1>Title</h1><p>Text <a href="../next">link</a></p></main>`,
		`<main><table><tr><td>First</td><td></td></tr></table></main>`,
		`<main><ol><li><p>Step</p><ul><li>Detail</li></ul></li></ol></main>`,
		`<script>window.__PRELOADED_STATE__={"Content":{"MainContent":[{"BlockTypeName":"ChapterBlock","ContentString":"<p>Embedded</p>"}]}};</script>`,
		`<main><figure><img src="/image.jpg" alt="Description"></figure></main>`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, page string) {
		if len(page) > 64*1024 {
			t.Skip()
		}
		blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(page), "https://example.test/root/")
		if err != nil {
			return
		}
		repeated, repeatedSnapshot, err := BuildCandidateBlocks(strings.NewReader(page), "https://example.test/root/")
		if err != nil || ContentHash(blocks) != ContentHash(repeated) || snapshot != repeatedSnapshot {
			t.Fatalf("same input did not produce stable output: %v", err)
		}
		seen := make(map[string]bool)
		for _, block := range blocks {
			if block.ID == "" || seen[block.ID] {
				t.Fatalf("block identity is empty or duplicated: %q", block.ID)
			}
			seen[block.ID] = true
		}
	})
}

// A candidate is delivered independently of its snapshot's document URL.
func TestCandidateAssetsResolveAgainstSourceURL(t *testing.T) {
	html := `<main><p>Read <a href="../guide?lang=en#steps">the guide</a>.</p><figure><img src="images/chart.png" alt="Chart"></figure><video src="../clip.mp4" poster="poster.jpg"></video></main><script>window.__PRELOADED_STATE__ = {"BlockTypeName":"Chapter","ContentString":"<p>Embedded <a href='next'>instructions</a></p>"};</script>`
	blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(html), "https://example.test/docs/page/")
	if err != nil {
		t.Fatal(err)
	}
	combined := ""
	for _, b := range blocks {
		combined += b.HTML
	}
	for _, want := range []string{"https://example.test/docs/guide?lang=en#steps", "https://example.test/docs/page/images/chart.png", "https://example.test/docs/clip.mp4", "https://example.test/docs/page/poster.jpg", "https://example.test/docs/page/next"} {
		if !strings.Contains(combined, want) || !strings.Contains(snapshot, want) {
			t.Errorf("candidate and snapshot must retain absolute resource %s; blocks=%s", want, combined)
		}
	}
}
