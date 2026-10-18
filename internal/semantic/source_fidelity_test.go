package semantic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSelectedSectionsRetainSourceOrder(t *testing.T) {
	blocks, _, err := BuildCandidateBlocks(strings.NewReader(`<main><h1>Procedure</h1><p>First turn power off.</p><p>Then replace the fuse.</p></main>`), "https://example.test/procedure")
	if err != nil {
		t.Fatal(err)
	}
	decisions := []BlockDecision{
		{BlockID: "b0003", Include: true, Role: RoleContent, Type: TypeNone},
		{BlockID: "b0002", Include: true, Role: RoleContent, Type: TypeCritical},
		{BlockID: "b0001", Include: false, Role: RoleHeader, Type: TypeNone},
	}
	if err := ValidateExtraction(ExtractionResponse{PageKind: "article", Blocks: decisions}, blocks); err != nil {
		t.Fatal(err)
	}
	sections := sectionsFromDecisions(blocks, decisions)
	if len(sections) != 2 || sections[0].BlockID != "b0002" || sections[1].BlockID != "b0003" || sections[0].Type != TypeCritical {
		t.Fatalf("selected passages must retain source order and decision metadata: %#v", sections)
	}
}

func TestPreformattedWhitespaceChangeInvalidatesExtraction(t *testing.T) {
	u := "https://example.test/code"
	original := "<main><pre><code>if allowed:\n    grant()\nnotify()</code></pre></main>"
	changed := "<main><pre><code>if allowed:\n    grant()\n    notify()</code></pre></main>"
	fetcher := mapFetcher{u: original}
	store := NewMemoryStore()
	pipeline := NewPipeline(fetcher, &recordingModel{}, store)
	config := CrawlConfig{SourceID: 1, SeedURL: u, AllowedHost: "example.test", MaxPages: 1}
	if _, err := pipeline.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	fetcher[u] = changed
	result, err := pipeline.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Unchanged != 0 || result.PendingReview != 1 || len(store.Candidates()) != 2 {
		t.Fatalf("changed indentation must produce a new review candidate: %+v", result)
	}
	result, err = pipeline.Run(context.Background(), config)
	if err != nil || result.Unchanged != 1 {
		t.Fatalf("identical preformatted content should still reuse extraction: %+v, %v", result, err)
	}
}

func TestHTMLBaseResolvesLinksAndAssets(t *testing.T) {
	for _, tc := range []struct {
		name, base, wantLink, wantAsset string
	}{
		{"relative", `<base href="../guide/">`, "https://example.test/guide/next", "https://example.test/guide/map.png"},
		{"first base wins", `<base href="/guide/"><base href="/ignored/">`, "https://example.test/guide/next", "https://example.test/guide/map.png"},
		{"external base stays outside crawl", `<base href="https://assets.example.test/guide/">`, "", "https://assets.example.test/guide/map.png"},
		{"invalid base falls back", `<base href="javascript:alert(1)">`, "https://example.test/topics/next", "https://example.test/topics/map.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := `<html><head>` + tc.base + `</head><body><main><p>Read <a href="next">next</a>.</p><img src="map.png" alt="Map"></main></body></html>`
			u := "https://example.test/topics/start"
			links, err := harvestLinks(page, u, "example.test")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLink == "" && len(links) != 0 || tc.wantLink != "" && (len(links) != 1 || links[0] != tc.wantLink) {
				t.Errorf("resolved crawl links = %v; want %q", links, tc.wantLink)
			}
			blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(page), u)
			if err != nil {
				t.Fatal(err)
			}
			if len(blocks) != 2 || !strings.Contains(blocks[1].HTML, `src="`+tc.wantAsset+`"`) || !strings.Contains(snapshot, `src="`+tc.wantAsset+`"`) {
				t.Fatalf("candidate and snapshot must resolve the source asset %q: %#v", tc.wantAsset, blocks)
			}
		})
	}
}

func TestHTTPFetcherDecodesHTMLCharacterEncoding(t *testing.T) {
	for _, tc := range []struct{ name, contentType, page, want string }{
		{"http charset", "text/html; charset=windows-1252", "<main><p>Avgift 20 \x80. G\xe4ller fr\xe5n m\xe5ndag.</p></main>", "Avgift 20 €. Gäller från måndag."},
		{"meta charset", "text/html", "<meta charset=iso-8859-1><main><p>G\xe4ller fr\xe5n m\xe5ndag.</p></main>", "Gäller från måndag."},
		{"utf8", "text/html; charset=utf-8", "<main><p>Avgift 20 €. Gäller från måndag.</p></main>", "Avgift 20 €. Gäller från måndag."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.page))
			}))
			defer server.Close()
			result, err := (&HTTPFetcher{}).Fetch(context.Background(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			blocks, _, err := BuildCandidateBlocks(strings.NewReader(result.HTML), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if len(blocks) != 1 || blocks[0].Text != tc.want {
				t.Fatalf("source text corrupted during decoding: %#v", blocks)
			}
		})
	}
}

func TestHTTPFetcherBoundsUTF8Expansion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1252")
		_, _ = w.Write([]byte("<p>" + strings.Repeat("\x80", 50) + "</p>"))
	}))
	defer server.Close()
	_, err := (&HTTPFetcher{MaxResponseBytes: 100}).Fetch(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("decoded UTF-8 must respect the page size limit: %v", err)
	}
}

func TestHTTPFetcherAcceptsEmptySuccess(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			result, err := (&HTTPFetcher{}).Fetch(context.Background(), server.URL)
			if err != nil || result.StatusCode != status || result.HTML != "" {
				t.Fatalf("empty successful response was rejected: %+v, %v", result, err)
			}
		})
	}
}

func TestExportRetainsNestedSemanticNotation(t *testing.T) {
	doc := ExportDocument{Status: CandidateApproved, Sections: []ExportSection{{BlockID: "b0001", HTML: `<p>Population <del>10<sup>6</sup></del> <ins>10<sup>7</sup></ins> people. Formula <s>H<sub>2</sub>O</s>.</p>`}}}
	for _, format := range []ExportFormat{ExportMarkdown, ExportText} {
		t.Run(string(format), func(t *testing.T) {
			rendered, err := RenderExportDocument(doc, format, ProfileComplete)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"[deleted: 10^(6)]", "[inserted: 10^(7)]", "[struck through: H_(2)O]"} {
				// Markdown escapes punctuation so it remains literal when rendered.
				text := strings.ReplaceAll(string(rendered.Content), `\`, "")
				if !strings.Contains(text, want) {
					t.Errorf("nested notation %q lost in %s", want, rendered.Content)
				}
			}
		})
	}
}
