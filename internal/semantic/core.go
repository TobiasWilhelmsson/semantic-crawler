package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

var repeatedWhitespace = regexp.MustCompile(`\s+`)

const candidateSelector = ".c-breadcrumb, .c-articlefooter, h1, h2, h3, h4, h5, h6, p, ul, ol, aside, table, figure, video, pre, dl, blockquote, address, img, audio"

func CanonicalizeURL(baseURL, href, allowedHost string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	reference, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", fmt.Errorf("parse URL reference: %w", err)
	}
	resolved := base.ResolveReference(reference)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", resolved.Scheme)
	}
	if !strings.EqualFold(resolved.Hostname(), allowedHost) {
		return "", fmt.Errorf("external host %q is not allowed", resolved.Hostname())
	}
	resolved.Fragment = ""
	resolved.RawQuery = ""
	resolved.Host = strings.ToLower(resolved.Host)
	if resolved.Path == "" {
		resolved.Path = "/"
	}
	return resolved.String(), nil
}

func BuildCandidateBlocks(reader io.Reader, pageURL string) ([]CandidateBlock, string, error) {
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, "", fmt.Errorf("parse HTML: %w", err)
	}
	embeddedBlocks := embeddedPreloadedContentBlocks(doc)
	doc.Find("script, style, form, noscript, template, svg, [hidden], [aria-hidden='true']").Remove()
	doc.Find("nav").Each(func(_ int, selection *goquery.Selection) {
		if !selection.HasClass("c-breadcrumb") {
			selection.Remove()
		}
	})

	blocks := make([]CandidateBlock, 0)
	root := doc.Selection
	if main := doc.Find("main").First(); main.Length() > 0 {
		root = main
	}
	if breadcrumb := doc.Find(".c-breadcrumb").First(); breadcrumb.Length() > 0 && root.Find(".c-breadcrumb").Length() == 0 {
		appendCandidateBlock(&blocks, breadcrumb)
	}
	represented := make(map[*html.Node]bool)
	root.Find(candidateSelector).Each(func(_ int, selection *goquery.Selection) {
		// Only suppress descendants when their ancestor actually yielded a block.
		// Empty containers otherwise hide media that has no text content.
		for _, ancestor := range selection.Parents().Nodes {
			if represented[ancestor] {
				return
			}
		}
		before := len(blocks)
		appendCandidateBlock(&blocks, selection)
		if len(blocks) > before {
			represented[selection.Nodes[0]] = true
		}
	})
	appendEmbeddedCandidateBlocks(&blocks, root, embeddedBlocks)

	makeAssetURLsAbsolute(doc, pageURL)
	// Blocks are copied before the snapshot is rendered. Resolve their URLs as
	// well so review exports and delivered Markdown do not depend on a receiver's
	// base URL. This includes content recovered from embedded source data.
	for index := range blocks {
		fragment, err := goquery.NewDocumentFromReader(strings.NewReader(blocks[index].HTML))
		if err != nil {
			return nil, "", fmt.Errorf("parse candidate HTML: %w", err)
		}
		makeAssetURLsAbsolute(fragment, pageURL)
		blocks[index].HTML, err = fragment.Find("body").Html()
		if err != nil {
			return nil, "", fmt.Errorf("render candidate HTML: %w", err)
		}
	}
	snapshot, err := doc.Html()
	if err != nil {
		return nil, "", fmt.Errorf("render snapshot HTML: %w", err)
	}
	return blocks, snapshot, nil
}

type embeddedContentBlock struct {
	BlockTypeName string
	ContentHTML   string
}

func embeddedPreloadedContentBlocks(doc *goquery.Document) []embeddedContentBlock {
	var blocks []embeddedContentBlock
	doc.Find("script").Each(func(_ int, selection *goquery.Selection) {
		script := selection.Text()
		stateJSON, ok := extractPreloadedStateJSON(script)
		if !ok {
			return
		}
		var value any
		if err := json.Unmarshal([]byte(stateJSON), &value); err != nil {
			return
		}
		collectEmbeddedContentBlocks(value, &blocks)
	})
	return blocks
}

func extractPreloadedStateJSON(script string) (string, bool) {
	marker := "__PRELOADED_STATE__"
	markerIndex := strings.Index(script, marker)
	if markerIndex < 0 {
		return "", false
	}
	start := strings.Index(script[markerIndex:], "{")
	if start < 0 {
		return "", false
	}
	start += markerIndex
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(script); index++ {
		character := script[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return script[start : index+1], true
			}
		}
	}
	return "", false
}

func collectEmbeddedContentBlocks(value any, blocks *[]embeddedContentBlock) {
	switch typed := value.(type) {
	case map[string]any:
		blockType, _ := typed["BlockTypeName"].(string)
		content, _ := typed["ContentString"].(string)
		if content != "" && retainEmbeddedContentBlockType(blockType) {
			*blocks = append(*blocks, embeddedContentBlock{BlockTypeName: blockType, ContentHTML: content})
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectEmbeddedContentBlocks(typed[key], blocks)
		}
	case []any:
		for _, child := range typed {
			collectEmbeddedContentBlocks(child, blocks)
		}
	}
}

func retainEmbeddedContentBlockType(blockType string) bool {
	lower := strings.ToLower(blockType)
	return strings.Contains(lower, "highlight") || strings.Contains(lower, "chapter")
}

func appendEmbeddedCandidateBlocks(blocks *[]CandidateBlock, root *goquery.Selection, embeddedBlocks []embeddedContentBlock) {
	for _, embedded := range embeddedBlocks {
		text, linkCount := embeddedContentText(embedded.ContentHTML)
		if text == "" || embeddedContentAlreadyCovered(embedded, text, *blocks) {
			continue
		}
		id := fmt.Sprintf("b%04d", len(*blocks)+1)
		tag := embeddedContentTag(embedded)
		html := fmt.Sprintf(`<%s class="semantic-embedded-block" data-semantic-block="%s" data-embedded-block-type="%s">%s</%s>`, tag, id, embedded.BlockTypeName, embedded.ContentHTML, tag)
		if root != nil {
			root.AppendHtml(html)
		}
		*blocks = append(*blocks, CandidateBlock{
			ID:        id,
			Tag:       tag,
			Text:      text,
			HTML:      html,
			Locator:   fmt.Sprintf("[data-semantic-block=%q]", id),
			LinkCount: linkCount,
		})
	}
}

func embeddedContentTag(embedded embeddedContentBlock) string {
	lower := strings.ToLower(embedded.BlockTypeName)
	if strings.Contains(lower, "highlight") {
		return "aside"
	}
	return "section"
}

func embeddedContentText(contentHTML string) (string, int) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(contentHTML))
	if err != nil {
		return normalizeText(contentHTML), 0
	}
	return normalizeText(doc.Text()), doc.Find("a[href]").Length()
}

func embeddedContentAlreadyCovered(embedded embeddedContentBlock, text string, blocks []CandidateBlock) bool {
	if !strings.Contains(strings.ToLower(embedded.BlockTypeName), "highlight") {
		var combined strings.Builder
		for _, block := range blocks {
			if !candidateCanRepresentEmbeddedBlock(embedded, block) {
				continue
			}
			combined.WriteString(" ")
			combined.WriteString(block.Text)
		}
		return textOverlap(text, combined.String()) >= 0.95
	}
	for _, block := range blocks {
		if !candidateCanRepresentEmbeddedBlock(embedded, block) {
			continue
		}
		if textOverlap(text, block.Text) >= 0.95 || textOverlap(block.Text, text) >= 0.95 {
			return true
		}
	}
	return false
}

func candidateCanRepresentEmbeddedBlock(embedded embeddedContentBlock, block CandidateBlock) bool {
	if strings.Contains(strings.ToLower(embedded.BlockTypeName), "highlight") {
		return block.Tag == "aside"
	}
	return true
}

func appendCandidateBlock(blocks *[]CandidateBlock, selection *goquery.Selection) {
	text := normalizeText(selection.Text())
	if selection.Is("figure, video, img, audio") {
		text = mediaSelectionText(selection)
	}
	if text == "" && !selection.Is("figure, video, img, audio") {
		return
	}
	id := fmt.Sprintf("b%04d", len(*blocks)+1)
	selection.SetAttr("data-semantic-block", id)
	html, htmlErr := goquery.OuterHtml(selection)
	if htmlErr != nil {
		return
	}
	tag := goquery.NodeName(selection)
	*blocks = append(*blocks, CandidateBlock{
		ID:        id,
		Tag:       tag,
		Text:      text,
		HTML:      html,
		Locator:   fmt.Sprintf("[data-semantic-block=%q]", id),
		LinkCount: selection.Find("a[href]").Length(),
	})
}

func makeAssetURLsAbsolute(doc *goquery.Document, pageURL string) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return
	}
	for _, attribute := range []string{"href", "src", "poster"} {
		doc.Find("[" + attribute + "]").Each(func(_ int, selection *goquery.Selection) {
			value, exists := selection.Attr(attribute)
			if !exists {
				return
			}
			reference, parseErr := url.Parse(value)
			if parseErr == nil {
				selection.SetAttr(attribute, base.ResolveReference(reference).String())
			}
		})
	}
}

func ContentHash(blocks []CandidateBlock) string {
	hasher := sha256.New()
	for _, block := range blocks {
		_, _ = fmt.Fprintf(hasher, "%s\x00%s\x00", strings.ToLower(block.Tag), normalizeText(block.Text))
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(block.HTML))
		if err != nil {
			_, _ = fmt.Fprint(hasher, normalizeText(block.HTML))
			continue
		}
		for _, node := range doc.Nodes {
			hashContentNode(hasher, node)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// Text belongs to its element: flattened text alone cannot distinguish, for
// example, a value moving between otherwise identical table cells.
func hashContentNode(writer io.Writer, node *html.Node) {
	switch node.Type {
	case html.TextNode:
		if text := normalizeText(node.Data); text != "" {
			_, _ = fmt.Fprintf(writer, "text:%q\x00", text)
		}
	case html.ElementNode:
		_, _ = fmt.Fprintf(writer, "open:%s\x00", node.Data)
		attributes := make(map[string]string, len(node.Attr))
		for _, attribute := range node.Attr {
			attributes[attribute.Key] = attribute.Val
		}
		for _, name := range []string{"href", "src", "srcset", "poster", "alt", "title", "aria-label", "role", "lang", "colspan", "rowspan", "class", "start", "value", "reversed"} {
			if value, exists := attributes[name]; exists {
				_, _ = fmt.Fprintf(writer, "%s=%q\x00", name, normalizeText(value))
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		hashContentNode(writer, child)
	}
	if node.Type == html.ElementNode {
		_, _ = fmt.Fprintf(writer, "close:%s\x00", node.Data)
	}
}

func ValidateExtraction(response ExtractionResponse, candidates []CandidateBlock) error {
	known := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		known[candidate.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(response.Blocks))
	for _, decision := range response.Blocks {
		if _, exists := known[decision.BlockID]; !exists {
			return fmt.Errorf("unknown block ID %q", decision.BlockID)
		}
		if _, exists := seen[decision.BlockID]; exists {
			return fmt.Errorf("duplicate block ID %q", decision.BlockID)
		}
		seen[decision.BlockID] = struct{}{}
		if decision.Include && !validRole(decision.Role) {
			return fmt.Errorf("invalid role %q for block %s", decision.Role, decision.BlockID)
		}
		if !validType(decision.Type) {
			return fmt.Errorf("invalid type %q for block %s", decision.Type, decision.BlockID)
		}
	}
	return nil
}

func normalizeText(value string) string {
	return strings.TrimSpace(repeatedWhitespace.ReplaceAllString(strings.ReplaceAll(value, "\u00a0", " "), " "))
}

func validRole(role BlockRole) bool {
	switch role {
	case RoleHeader, RoleContent, RoleInfoBox, RoleMetadata, RoleNavigation, RoleTable, RoleMedia, RoleMediaGroup, RoleVideo:
		return true
	default:
		return false
	}
}

func validType(blockType BlockType) bool {
	switch blockType {
	case TypeNone, TypeCritical, TypeWarning, TypeInfo:
		return true
	default:
		return false
	}
}
