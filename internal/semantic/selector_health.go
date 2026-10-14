package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"

	"github.com/PuerkitoBio/goquery"
)

type SelectorHealthStatus string

const (
	SelectorHealthValidGold            SelectorHealthStatus = "valid_gold"
	SelectorHealthPartialGold          SelectorHealthStatus = "partial_gold"
	SelectorHealthZeroGold             SelectorHealthStatus = "zero_gold"
	SelectorHealthRedirectOrURLChanged SelectorHealthStatus = "redirect_or_url_changed"
	SelectorHealthFetchFailed          SelectorHealthStatus = "fetch_failed"
)

type SelectorIdentifierStatus string

const (
	SelectorIdentifierMatched SelectorIdentifierStatus = "matched"
	SelectorIdentifierEmpty   SelectorIdentifierStatus = "empty"
	SelectorIdentifierMissing SelectorIdentifierStatus = "missing"
)

type SelectorHealthOptions struct {
	MappingPath     string
	URLContains     string
	Fetcher         Fetcher
	ReportDirectory string
	StartPage       int
	MaxPages        int
}

type SelectorIdentifierHealth struct {
	Name     string                   `json:"name"`
	Selector string                   `json:"selector"`
	Parent   string                   `json:"parent,omitempty"`
	Status   SelectorIdentifierStatus `json:"status"`
	Text     string                   `json:"text,omitempty"`
}

type SelectorPageHealth struct {
	URL         string                     `json:"url"`
	FinalURL    string                     `json:"final_url,omitempty"`
	StatusCode  int                        `json:"status_code"`
	Status      SelectorHealthStatus       `json:"status"`
	Error       string                     `json:"error,omitempty"`
	Identifiers []SelectorIdentifierHealth `json:"identifiers,omitempty"`
}

type SelectorHealthReport struct {
	TotalURLs        int                  `json:"total_urls"`
	TotalIdentifiers int                  `json:"total_identifiers"`
	MatchedSelectors int                  `json:"matched_selectors"`
	EmptySelectors   int                  `json:"empty_selectors"`
	MissingSelectors int                  `json:"missing_selectors"`
	ValidGoldPages   int                  `json:"valid_gold_pages"`
	PartialGoldPages int                  `json:"partial_gold_pages"`
	ZeroGoldPages    int                  `json:"zero_gold_pages"`
	RedirectedPages  int                  `json:"redirected_pages"`
	FetchFailedPages int                  `json:"fetch_failed_pages"`
	Pages            []SelectorPageHealth `json:"pages"`
}

func RunSelectorHealth(ctx context.Context, options SelectorHealthOptions) (SelectorHealthReport, error) {
	if err := ctx.Err(); err != nil {
		return SelectorHealthReport{}, err
	}
	if options.Fetcher == nil {
		return SelectorHealthReport{}, fmt.Errorf("selector health fetcher is required")
	}
	mapping, err := LoadGoldMappings(options.MappingPath, options.URLContains)
	if err != nil {
		return SelectorHealthReport{}, err
	}
	if options.StartPage > 1 {
		if options.StartPage > len(mapping.Pages) {
			mapping.Pages = nil
		} else {
			mapping.Pages = mapping.Pages[options.StartPage-1:]
		}
	}
	if options.MaxPages > 0 && len(mapping.Pages) > options.MaxPages {
		mapping.Pages = mapping.Pages[:options.MaxPages]
	}
	report := SelectorHealthReport{TotalURLs: len(mapping.Pages)}
	for _, page := range mapping.Pages {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		result, err := options.Fetcher.Fetch(ctx, page.URL)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return report, ctxErr
		}
		report.TotalIdentifiers += len(page.Identifiers)
		if err != nil {
			report.FetchFailedPages++
			report.Pages = append(report.Pages, SelectorPageHealth{URL: page.URL, Status: SelectorHealthFetchFailed, Error: err.Error()})
			continue
		}
		pageHealth := SelectorPageHealth{URL: page.URL, FinalURL: result.URL, StatusCode: result.StatusCode}
		if result.StatusCode != 0 && (result.StatusCode < 200 || result.StatusCode >= 300) {
			pageHealth.Status = SelectorHealthFetchFailed
			pageHealth.Error = fmt.Sprintf("HTTP %d", result.StatusCode)
			report.FetchFailedPages++
			report.Pages = append(report.Pages, pageHealth)
			continue
		}
		if result.URL != "" && result.URL != page.URL {
			pageHealth.Status = SelectorHealthRedirectOrURLChanged
			report.RedirectedPages++
			report.Pages = append(report.Pages, pageHealth)
			continue
		}
		identifiers, matched, empty, missing, err := selectorIdentifierHealth(page, []byte(result.HTML))
		if err != nil {
			report.FetchFailedPages++
			pageHealth.Status = SelectorHealthFetchFailed
			pageHealth.Error = err.Error()
			report.Pages = append(report.Pages, pageHealth)
			continue
		}
		pageHealth.Identifiers = identifiers
		report.MatchedSelectors += matched
		report.EmptySelectors += empty
		report.MissingSelectors += missing
		switch {
		case matched == len(page.Identifiers):
			pageHealth.Status = SelectorHealthValidGold
			report.ValidGoldPages++
		case matched > 0:
			pageHealth.Status = SelectorHealthPartialGold
			report.PartialGoldPages++
		default:
			pageHealth.Status = SelectorHealthZeroGold
			report.ZeroGoldPages++
		}
		report.Pages = append(report.Pages, pageHealth)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if options.ReportDirectory != "" {
		if err := WriteSelectorHealthReports(options.ReportDirectory, report); err != nil {
			return report, err
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}

func selectorIdentifierHealth(page GoldPage, html []byte) ([]SelectorIdentifierHealth, int, int, int, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("parse selector health HTML: %w", err)
	}
	var matched, empty, missing int
	identifiers := make([]SelectorIdentifierHealth, 0, len(page.Identifiers))
	for _, identifier := range page.Identifiers {
		selection := doc.Find(identifier.Container).First()
		if identifier.Parent != "" {
			selection = doc.Find(identifier.Parent).First().Find(identifier.Container).First()
		}
		item := SelectorIdentifierHealth{Name: identifier.Name, Selector: identifier.Container, Parent: identifier.Parent}
		if selection.Length() == 0 {
			item.Status = SelectorIdentifierMissing
			missing++
			identifiers = append(identifiers, item)
			continue
		}
		text := normalizeText(selection.Text())
		if text == "" && isMediaRole(identifier.Role) {
			text = mediaSelectionText(selection)
		}
		if text == "" {
			item.Status = SelectorIdentifierEmpty
			empty++
		} else {
			item.Status = SelectorIdentifierMatched
			item.Text = text
			matched++
		}
		identifiers = append(identifiers, item)
	}
	return identifiers, matched, empty, missing, nil
}

func WriteSelectorHealthReports(directory string, report SelectorHealthReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create selector health directory: %w", err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode selector health report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "selector-health.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write selector health JSON: %w", err)
	}
	file, err := os.Create(filepath.Join(directory, "selector-health.html"))
	if err != nil {
		return fmt.Errorf("create selector health HTML: %w", err)
	}
	defer file.Close()
	return template.Must(template.New("selector-health").Parse(selectorHealthHTML)).Execute(file, report)
}

const selectorHealthHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Selector health</title><style>body{font:14px system-ui;max-width:1200px;margin:30px auto;padding:0 20px;color:#172033}.cards{display:grid;grid-template-columns:repeat(5,1fr);gap:12px}.card{border:1px solid #ccd3dc;border-radius:8px;padding:12px;background:#f8fafc}.metric{font-size:24px;font-weight:700}table{border-collapse:collapse;width:100%;margin-top:16px}th,td{border:1px solid #ccc;padding:8px;text-align:left;vertical-align:top}code{background:#eef2f8;padding:2px 5px;border-radius:5px}</style></head><body><h1>Selector health</h1><div class="cards"><section class="card"><div>URLs</div><div class="metric">{{.TotalURLs}}</div></section><section class="card"><div>Identifiers</div><div class="metric">{{.TotalIdentifiers}}</div></section><section class="card"><div>Matched</div><div class="metric">{{.MatchedSelectors}}</div></section><section class="card"><div>Missing</div><div class="metric">{{.MissingSelectors}}</div></section><section class="card"><div>Zero-gold pages</div><div class="metric">{{.ZeroGoldPages}}</div></section></div><p>Valid gold pages: {{.ValidGoldPages}} · Partial gold pages: {{.PartialGoldPages}} · Redirected/URL changed: {{.RedirectedPages}} · Fetch failed: {{.FetchFailedPages}}</p><table><thead><tr><th>URL</th><th>Status</th><th>Selector misses</th></tr></thead><tbody>{{range .Pages}}<tr><td>{{.URL}}</td><td><code>{{.Status}}</code></td><td>{{range .Identifiers}}{{if ne .Status "matched"}}<div><code>{{.Selector}}</code> {{.Name}}: {{.Status}}</div>{{end}}{{end}}{{.Error}}</td></tr>{{end}}</tbody></table></body></html>`
