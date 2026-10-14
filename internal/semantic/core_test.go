package semantic

import (
	"strings"
	"testing"
)

func TestCanonicalizeURLNormalizesAndRejectsExternalHosts(t *testing.T) {
	base := "https://source.example.test/topics/airway/overview/"

	got, err := CanonicalizeURL(base, "../preparation/?utm_source=test#content", "source.example.test")
	if err != nil {
		t.Fatalf("canonicalize URL: %v", err)
	}
	if got != "https://source.example.test/topics/airway/preparation/" {
		t.Fatalf("unexpected canonical URL: %s", got)
	}

	if _, err := CanonicalizeURL(base, "https://example.com/article", "source.example.test"); err == nil {
		t.Fatal("expected external host to be rejected")
	}
}

func TestBuildCandidateBlocksPreservesSemanticMedicalContent(t *testing.T) {
	html := `<html><body>
		<nav><a href="/ignored">Menu</a></nav>
		<main>
			<h1>Sugning av luftvägar</h1>
			<section><h2>Förberedelser</h2><p>Kontrollera patientens identitet.</p></section>
			<aside class="c-highlight"><strong>Avbryt vid allvarlig desaturation.</strong></aside>
			<table><tr><th>Åtgärd</th><th>Intervall</th></tr><tr><td>Sugning</td><td>Vid behov</td></tr></table>
		</main>
		<script>doNotInclude()</script>
	</body></html>`

	blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	if len(blocks) != 5 {
		t.Fatalf("expected 5 semantic blocks, got %d: %#v", len(blocks), blocks)
	}
	if blocks[0].ID != "b0001" || blocks[0].Tag != "h1" {
		t.Fatalf("unexpected first block: %#v", blocks[0])
	}
	if !strings.Contains(blocks[3].Text, "allvarlig desaturation") || blocks[3].Tag != "aside" {
		t.Fatalf("critical warning was not preserved: %#v", blocks[3])
	}
	if !strings.Contains(blocks[4].HTML, "Vid behov") || blocks[4].Tag != "table" {
		t.Fatalf("table was not preserved: %#v", blocks[4])
	}
	if strings.Contains(snapshot, "doNotInclude") || strings.Contains(snapshot, "Menu") {
		t.Fatalf("snapshot contains excluded structural content: %s", snapshot)
	}
	if !strings.Contains(snapshot, `data-semantic-block="b0004"`) {
		t.Fatalf("snapshot does not contain review block markers: %s", snapshot)
	}
}

func TestBuildCandidateBlocksKeepsBaselineBreadcrumbAndMetadata(t *testing.T) {
	html := `<html><body>
		<nav><a href="/ignored">Global menu</a></nav>
		<nav class="c-breadcrumb">Du är här: StartVård och behandling</nav>
		<main>
			<h1>Article</h1>
			<p>Clinical text.</p>
			<footer class="c-articlefooter">Godkänd:2026-06-18Författare: Test</footer>
		</main>
	</body></html>`

	blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	var breadcrumb, metadata bool
	for _, block := range blocks {
		if strings.Contains(block.Text, "Du är här") {
			breadcrumb = true
		}
		if strings.Contains(block.Text, "Godkänd") {
			metadata = true
		}
	}
	if !breadcrumb || !metadata {
		t.Fatalf("breadcrumb and metadata should be available to extraction: %#v", blocks)
	}
	if strings.Contains(snapshot, "Global menu") {
		t.Fatalf("global navigation should still be removed: %s", snapshot)
	}
}

func TestBuildCandidateBlocksKeepsPreloadedHighlightBlocks(t *testing.T) {
	html := `<html><body><main>
		<h1>Bedömning enligt ABCDE</h1>
		<p>Clinical introduction.</p>
	</main>
	<script>
		window.__PRELOADED_STATE__ = {"Content":{"MainContent":[
			{"BlockTypeName":"HighlightBlock","ContentString":"<div class=\"infobox\"><p>Prioritera alltid din egen säkerhet och använd skyddsutrustning.</p></div>"},
			{"BlockTypeName":"ChapterBlock","ContentString":"<p>Already rendered chapter text.</p>"}
		]}};
	</script></body></html>`

	blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	var highlight CandidateBlock
	for _, block := range blocks {
		if strings.Contains(block.Text, "Prioritera alltid") {
			highlight = block
			break
		}
	}
	if highlight.ID == "" || highlight.Tag != "aside" || !strings.Contains(highlight.HTML, "infobox") {
		t.Fatalf("preloaded highlight block should be preserved as semantic aside: %#v", blocks)
	}
	if strings.Contains(snapshot, "__PRELOADED_STATE__") {
		t.Fatalf("snapshot should still remove scripts: %s", snapshot)
	}
	if !strings.Contains(snapshot, `data-semantic-block="`+highlight.ID+`"`) || !strings.Contains(snapshot, "Prioritera alltid") {
		t.Fatalf("snapshot should contain synthetic highlighted review block: %s", snapshot)
	}
}

func TestBuildCandidateBlocksKeepsPreloadedChapterBlocksWithoutVisibleParagraphs(t *testing.T) {
	html := `<html><body><main>
		<h1>Åtgärder enligt NEWS</h1>
		<div class="chapter-placeholder"></div>
	</main>
	<script>
		window.__PRELOADED_STATE__ = {"Content":{"MainContent":[
			{"BlockTypeName":"ChapterBlock","ContentString":"<div>Vid allvarliga kliniska situationer är det primära att få överblick och säkra vitala funktioner.</div><div>Läs mer i Sample Sources texter om Bedömning enligt ABCDE.</div><div>När poängen i de olika parametrarna är sammanräknade ska åtgärdsskalan följas.</div>"},
			{"BlockTypeName":"ImageBlock","ContentString":"<figure><img src=\"/globalassets/news.jpg\" alt=\"Åtgärdsskala NEWS\"><figcaption>Åtgärdsskalan</figcaption></figure>"}
		]}};
	</script></body></html>`

	blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	var chapter CandidateBlock
	for _, block := range blocks {
		if strings.Contains(block.Text, "Vid allvarliga kliniska situationer") {
			chapter = block
			break
		}
	}
	if chapter.ID == "" || chapter.Tag != "section" {
		t.Fatalf("preloaded chapter text should be available as a semantic content block: %#v", blocks)
	}
	if !strings.Contains(chapter.Text, "åtgärdsskalan följas") || !strings.Contains(chapter.Text, "Bedömning enligt ABCDE") {
		t.Fatalf("preloaded chapter should preserve all clinical intro text: %#v", chapter)
	}
	if !strings.Contains(snapshot, `data-semantic-block="`+chapter.ID+`"`) || !strings.Contains(snapshot, "Vid allvarliga kliniska situationer") {
		t.Fatalf("snapshot should contain synthetic chapter review block: %s", snapshot)
	}
}

func TestBuildCandidateBlocksKeepsPreloadedChapterWhenOnlyPartiallyCovered(t *testing.T) {
	html := `<html><body><main>
		<h1>Åtgärder enligt NEWS</h1>
		<p>När poängen i de olika parametrarna är sammanräknade ska åtgärdsskalan följas.</p>
	</main>
	<script>
		window.__PRELOADED_STATE__ = {"Content":{"MainContent":[
			{"BlockTypeName":"ChapterBlock","ContentString":"<div>Vid allvarliga kliniska situationer är det primära att få överblick och säkra vitala funktioner.</div><div>När poängen i de olika parametrarna är sammanräknade ska åtgärdsskalan följas.</div>"}
		]}};
	</script></body></html>`

	blocks, _, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	var chapter CandidateBlock
	for _, block := range blocks {
		if strings.Contains(block.Text, "Vid allvarliga kliniska situationer") {
			chapter = block
			break
		}
	}
	if chapter.ID == "" || chapter.Tag != "section" {
		t.Fatalf("partially covered preloaded chapter should not be discarded: %#v", blocks)
	}
}

func TestBuildCandidateBlocksKeepsPreloadedHighlightEvenWhenParagraphTextExists(t *testing.T) {
	html := `<html><body><main>
		<h1>Article</h1>
		<div class="infobox"><p>Skydda patienten mot nedkylning.</p></div>
	</main>
	<script>
		window.__PRELOADED_STATE__ = {"Content":{"MainContent":[
			{"BlockTypeName":"HighlightBlock","ContentString":"<div class=\"infobox\"><p>Skydda patienten mot nedkylning.</p></div>"}
		]}};
	</script></body></html>`

	blocks, _, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	var plainParagraphs, highlightedAsides int
	for _, block := range blocks {
		if strings.Contains(block.Text, "Skydda patienten") && block.Tag == "p" {
			plainParagraphs++
		}
		if strings.Contains(block.Text, "Skydda patienten") && block.Tag == "aside" {
			highlightedAsides++
		}
	}
	if plainParagraphs != 1 || highlightedAsides != 1 {
		t.Fatalf("highlight semantics should be preserved even when plain paragraph exists: %#v", blocks)
	}
}

func TestBuildCandidateBlocksUsesMediaMetadataWhenFigureHasNoVisibleText(t *testing.T) {
	html := `<html><body><main><figure><img src="/globalassets/haklyft.jpg" alt="Bild som visar haklyft"></figure></main></body></html>`

	blocks, _, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected one media block, got %#v", blocks)
	}
	if blocks[0].Tag != "figure" || !strings.Contains(blocks[0].Text, "Bild som visar haklyft") || !strings.Contains(blocks[0].Text, "/globalassets/haklyft.jpg") {
		t.Fatalf("figure block should expose image metadata for prompts and hashes: %#v", blocks[0])
	}
}

func TestBuildCandidateBlocksPreservesMediaMetadataWhenFigureHasVisibleCaption(t *testing.T) {
	html := `<html><body><main>
		<figure>
			<img src="/globalassets/inhalator.jpg" alt="Slut läpparna tätt runt pulverinhalatorns munstycke och andas in genom munnen">
			<figcaption>Exempel på pulverinhalator.</figcaption>
		</figure>
	</main></body></html>`

	blocks, _, err := BuildCandidateBlocks(strings.NewReader(html), "https://source.example.test/test/")
	if err != nil {
		t.Fatalf("build candidate blocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected one media block, got %#v", blocks)
	}
	for _, expected := range []string{"Slut läpparna tätt", "Exempel på pulverinhalator", "/globalassets/inhalator.jpg"} {
		if !strings.Contains(blocks[0].Text, expected) {
			t.Fatalf("figure block should preserve visible caption and media metadata %q: %#v", expected, blocks[0])
		}
	}
}

func TestContentHashIgnoresWhitespaceButDetectsMedicalChanges(t *testing.T) {
	first := []CandidateBlock{{Tag: "p", Text: "Kontrollera   patienten."}}
	second := []CandidateBlock{{Tag: "p", Text: " Kontrollera patienten. "}}
	changed := []CandidateBlock{{Tag: "p", Text: "Kontrollera patienten omedelbart."}}

	if ContentHash(first) != ContentHash(second) {
		t.Fatal("whitespace-only changes must not change the content hash")
	}
	if ContentHash(first) == ContentHash(changed) {
		t.Fatal("medical text changes must change the content hash")
	}
}

func TestValidateExtractionRejectsUnknownAndDuplicateBlockIDs(t *testing.T) {
	blocks := []CandidateBlock{{ID: "b0001"}, {ID: "b0002"}}

	valid := ExtractionResponse{Blocks: []BlockDecision{{BlockID: "b0001", Include: true, Role: RoleContent, Type: TypeNone, ParseMode: true}}}
	if err := ValidateExtraction(valid, blocks); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}

	unknown := ExtractionResponse{Blocks: []BlockDecision{{BlockID: "b9999", Include: true, Role: RoleContent, Type: TypeNone}}}
	if err := ValidateExtraction(unknown, blocks); err == nil {
		t.Fatal("expected unknown block ID to be rejected")
	}

	duplicate := ExtractionResponse{Blocks: []BlockDecision{
		{BlockID: "b0001", Include: true, Role: RoleContent, Type: TypeNone},
		{BlockID: "b0001", Include: false, Role: RoleContent, Type: TypeNone},
	}}
	if err := ValidateExtraction(duplicate, blocks); err == nil {
		t.Fatal("expected duplicate block ID to be rejected")
	}
}

func TestCandidateBlocksRetainNonmedicalSemanticElements(t *testing.T) {
	source := `<main><pre><code>tool --dry-run</code></pre><dl><dt>Fee</dt><dd>25 SEK/year</dd></dl><blockquote><p>Except temporary applicants.</p></blockquote><address>Office 12</address><img src="/map.png" alt="Office map"><audio src="/guide.mp3"></audio></main>`
	blocks, _, err := BuildCandidateBlocks(strings.NewReader(source), "https://example.test/page")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 6 {
		t.Fatalf("expected six separate blocks without duplicated children, got %#v", blocks)
	}
	for i, tag := range []string{"pre", "dl", "blockquote", "address", "img", "audio"} {
		if blocks[i].Tag != tag {
			t.Errorf("block %d: %s", i, blocks[i].Tag)
		}
	}
}
