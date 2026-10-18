package semantic

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/PuerkitoBio/goquery"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type ExportFormat string

const (
	ExportJSON     ExportFormat = "json"
	ExportJSONL    ExportFormat = "jsonl"
	ExportMarkdown ExportFormat = "markdown"
	ExportText     ExportFormat = "text"
)

type ContentProfile string

const (
	ProfileComplete ContentProfile = "complete"
	ProfileLegacy   ContentProfile = "legacy"
)

type RenderedDocument struct {
	Content  []byte
	Warnings []string
}

func RenderExportDocument(document ExportDocument, format ExportFormat, profile ContentProfile) (RenderedDocument, error) {
	if format != ExportMarkdown && format != ExportText {
		return RenderedDocument{}, fmt.Errorf("render format must be markdown or text")
	}
	if profile != ProfileComplete && profile != ProfileLegacy {
		return RenderedDocument{}, fmt.Errorf("unknown content profile %q", profile)
	}
	result := RenderedDocument{Warnings: []string{}}
	var out strings.Builder
	if document.Status != CandidateApproved {
		fmt.Fprintf(&out, "UNREVIEWED: %s\n\n", document.Status)
	}
	if profile == ProfileLegacy && format == ExportMarkdown {
		out.WriteString(document.legacyMarkdown)
		result.Content = []byte(out.String())
		result.Warnings = append(result.Warnings, "Legacy Markdown follows saved inclusion flags and may omit selected content.")
		return result, nil
	}
	if profile == ProfileLegacy {
		result.Warnings = append(result.Warnings, "Legacy text follows saved section inclusion flags.")
	}
	policy := bluemonday.UGCPolicy()
	policy.AllowAttrs("start").Matching(regexp.MustCompile(`^[0-9]{1,9}$`)).OnElements("ol")
	markdownConverter := converter.NewConverter(converter.WithPlugins(base.NewBasePlugin(), commonmark.NewCommonmarkPlugin(), table.NewTablePlugin(table.WithSpanCellBehavior(table.SpanBehaviorMirror), table.WithNewlineBehavior(table.NewlineBehaviorPreserve))))
	for _, section := range document.Sections {
		if profile == ProfileLegacy && !section.parseMode {
			continue
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(section.HTML))
		if err != nil {
			return result, fmt.Errorf("parse section %s: %w", section.BlockID, err)
		}
		if doc.Find("script,style,iframe,object,embed,form,input,button").Length() > 0 {
			result.Warnings = append(result.Warnings, "Active or unsupported elements removed from section "+section.BlockID)
		}
		doc.Find("script,style,iframe,object,embed,form,input,button").Remove()
		doc.Find("img,source").Each(func(_ int, s *goquery.Selection) {
			href, _ := s.Attr("src")
			label, _ := s.Attr("alt")
			if label == "" {
				label = goquery.NodeName(s)
			}
			n := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: href}}}
			n.AppendChild(&html.Node{Type: html.TextNode, Data: label})
			s.ReplaceWithNodes(n)
			result.Warnings = append(result.Warnings, "Media represented as a link in section "+section.BlockID)
		})
		doc.Find("video,audio").Each(func(_ int, s *goquery.Selection) {
			wrapper := &html.Node{Type: html.ElementNode, Data: "div"}
			if href, ok := s.Attr("src"); ok && href != "" {
				a := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: href}}}
				a.AppendChild(&html.Node{Type: html.TextNode, Data: goquery.NodeName(s)})
				wrapper.AppendChild(a)
			}
			for _, node := range s.Nodes {
				for node.FirstChild != nil {
					child := node.FirstChild
					node.RemoveChild(child)
					wrapper.AppendChild(child)
				}
			}
			s.ReplaceWithNodes(wrapper)
			result.Warnings = append(result.Warnings, "Media represented as links and fallback text in section "+section.BlockID)
		})
		// Sanitizing MathML by stripping tags can turn 10 to the sixth power
		// into the unrelated number 106. Never present that as source text.
		doc.Find("math").Each(func(_ int, element *goquery.Selection) {
			text := "[MathML formula: consult selected HTML in the structured export]"
			if alternative := strings.TrimSpace(element.AttrOr("alttext", "")); alternative != "" {
				text = "[MathML alternative text: " + alternative + "]"
			}
			element.ReplaceWithNodes(&html.Node{Type: html.TextNode, Data: text})
			result.Warnings = append(result.Warnings, "MathML cannot be rendered faithfully; original formula is retained in selected HTML for section "+section.BlockID)
		})
		if preserveExportListNumbers(doc, format) {
			result.Warnings = append(result.Warnings, "Ordered list numbering represented as explicit decimal labels in section "+section.BlockID)
		}
		// Convert descendants before their containers, which otherwise flatten
		// nested notation (for example, a deleted 10^6 becomes the number 106).
		inline := doc.Find("del,ins,s,strike,sup,sub")
		for index := inline.Length() - 1; index >= 0; index-- {
			element := inline.Eq(index)
			value := element.Text()
			var notation string
			switch goquery.NodeName(element) {
			case "del":
				notation = "[deleted: " + value + "]"
			case "ins":
				notation = "[inserted: " + value + "]"
			case "s", "strike":
				notation = "[struck through: " + value + "]"
			case "sup":
				notation = "^(" + value + ")"
			case "sub":
				notation = "_(" + value + ")"
			}
			element.ReplaceWithNodes(&html.Node{Type: html.TextNode, Data: notation})
			result.Warnings = append(result.Warnings, "Semantic inline markup represented with explicit text notation in section "+section.BlockID)
		}
		doc.Find("dt,dd").Each(func(_ int, element *goquery.Selection) {
			if goquery.NodeName(element) == "dt" {
				element.AppendHtml(":")
			}
			for _, node := range element.Nodes {
				node.Data = "p"
			}
		})
		if doc.Find("table [rowspan],table [colspan],table table").Length() > 0 {
			result.Warnings = append(result.Warnings, "Complex table layout is flattened; selected HTML in JSON retains the original cell relationships in section "+section.BlockID)
		}
		raw, err := doc.Find("body").Html()
		if err != nil {
			return result, err
		}
		safe := policy.Sanitize(raw)
		if strings.Contains(strings.ToLower(raw), "javascript:") {
			result.Warnings = append(result.Warnings, "Unsafe link removed from section "+section.BlockID)
		}
		var content string
		if format == ExportMarkdown {
			content, err = markdownConverter.ConvertString(safe)
		} else {
			var plain *goquery.Document
			plain, err = goquery.NewDocumentFromReader(strings.NewReader(safe))
			if err == nil {
				plain.Find("a").Each(func(_ int, s *goquery.Selection) {
					if href, ok := s.Attr("href"); ok {
						s.AppendHtml(" (" + html.EscapeString(href) + ")")
					}
				})
				plain.Find("th,td").AppendHtml("\t")
				plain.Find("p,div,aside,li,tr,h1,h2,h3,h4,h5,h6,figcaption,br").AppendHtml("\n")
				content = plain.Find("body").Text()
			}
		}
		if err != nil {
			return result, fmt.Errorf("render section %s: %w", section.BlockID, err)
		}
		if strings.TrimSpace(content) == "" && strings.TrimSpace(section.HTML) != "" {
			result.Warnings = append(result.Warnings, "No representable text in section "+section.BlockID)
		}
		out.WriteString(strings.TrimSpace(content))
		out.WriteString("\n\n")
	}
	if format == ExportText {
		result.Warnings = append(result.Warnings, "Plain text loses rich formatting; consult structured JSON for section relationships.")
	}
	result.Content = []byte(strings.TrimSpace(out.String()) + "\n")
	return result, nil
}

// CommonMark can express a positive starting ordinal, but not reversed lists or
// per-item resets. Use explicit labels for these and for plain-text exports.
func preserveExportListNumbers(doc *goquery.Document, format ExportFormat) bool {
	converted := false
	doc.Find("ol").Each(func(_ int, list *goquery.Selection) {
		items := list.ChildrenFiltered("li")
		_, reversed := list.Attr("reversed")
		start := int64(1)
		if reversed {
			start = int64(items.Length())
		}
		parseOrdinal := func(raw string, fallback int64) int64 {
			value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
			if err != nil {
				return fallback
			}
			return value
		}
		start = parseOrdinal(list.AttrOr("start", ""), start)
		nondecimal := list.AttrOr("type", "1") != "1"
		if format == ExportMarkdown && !reversed && !nondecimal && items.Filter("[value]").Length() == 0 && start >= 0 && start <= 999999999 {
			list.SetAttr("start", strconv.FormatInt(start, 10))
			return
		}
		converted = true
		step := int64(1)
		if reversed {
			step = -1
		}
		items.Each(func(_ int, item *goquery.Selection) {
			start = parseOrdinal(item.AttrOr("value", ""), start)
			item.PrependNodes(&html.Node{Type: html.TextNode, Data: fmt.Sprintf("[%d] ", start)})
			start += step
		})
		for _, node := range list.Nodes {
			node.Data = "ul"
			node.DataAtom = atom.Ul
		}
	})
	return converted
}
