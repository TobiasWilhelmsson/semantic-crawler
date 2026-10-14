package semantic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type GoldIdentifier struct {
	Name           string    `json:"name"`
	Role           BlockRole `json:"role"`
	Type           BlockType `json:"type"`
	Container      string    `json:"container"`
	Parent         string    `json:"parent"`
	ParseMode      bool      `json:"parse_mode"`
	IgnoreTexts    []string  `json:"ignore_texts"`
	IgnorePatterns []struct {
		Pattern     string `json:"pattern"`
		Replacement string `json:"replacement"`
	} `json:"ignore_patterns"`
}

type GoldIgnoreRule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

type GoldPage struct {
	URL         string           `json:"url"`
	Slug        string           `json:"-"`
	Identifiers []GoldIdentifier `json:"identifier"`
	IgnoreRules []GoldIgnoreRule `json:"-"`
}

type GoldMapping struct {
	Pages       []GoldPage
	IgnoreRules []GoldIgnoreRule
}

type GoldOverride struct {
	URL              string    `json:"url"`
	IdentifierName   string    `json:"identifier_name"`
	OriginalSelector string    `json:"original_selector"`
	Reason           string    `json:"reason"`
	AuditedText      string    `json:"audited_text"`
	Role             BlockRole `json:"role"`
	Type             BlockType `json:"type"`
	ParseMode        bool      `json:"parse_mode"`
	AuditorNote      string    `json:"auditor_note"`
	AuditedAt        string    `json:"audited_at"`
}

type GoldOverrides struct {
	Overrides []GoldOverride `json:"overrides"`
}

func LoadGoldMappings(path, URLContains string) (GoldMapping, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GoldMapping{}, fmt.Errorf("read gold mapping: %w", err)
	}
	var source struct {
		Ignore []GoldIgnoreRule `json:"ignore"`
		URLs   []GoldPage       `json:"urls"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return GoldMapping{}, fmt.Errorf("decode gold mapping: %w", err)
	}
	result := GoldMapping{IgnoreRules: source.Ignore}
	for _, page := range source.URLs {
		if !strings.Contains(page.URL, URLContains) {
			continue
		}
		page.Slug = strings.TrimSuffix(page.URL, "/")
		page.Slug = page.Slug[strings.LastIndex(page.Slug, "/")+1:]
		page.IgnoreRules = source.Ignore
		result.Pages = append(result.Pages, page)
	}
	return result, nil
}

func ExtractGoldSections(page GoldPage, html []byte) ([]BenchmarkSection, error) {
	return extractGoldSections(page, html, false)
}

func ExtractGoldSectionsStrict(page GoldPage, html []byte) ([]BenchmarkSection, error) {
	return extractGoldSections(page, html, true)
}

func LoadGoldOverrides(path string) (GoldOverrides, error) {
	if path == "" {
		return GoldOverrides{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GoldOverrides{}, fmt.Errorf("read gold overrides: %w", err)
	}
	var overrides GoldOverrides
	if err := json.Unmarshal(data, &overrides); err != nil {
		return GoldOverrides{}, fmt.Errorf("decode gold overrides: %w", err)
	}
	return overrides, nil
}

func (overrides GoldOverrides) SectionsForURL(url string) []BenchmarkSection {
	var sections []BenchmarkSection
	for _, override := range overrides.Overrides {
		if override.URL != url {
			continue
		}
		blockType := override.Type
		if blockType == "" {
			blockType = TypeNone
		}
		sections = append(sections, BenchmarkSection{
			BlockID:   override.IdentifierName,
			Role:      override.Role,
			Type:      blockType,
			ParseMode: override.ParseMode,
			Text:      normalizeText(override.AuditedText),
		})
	}
	return sections
}

func extractGoldSections(page GoldPage, html []byte, strict bool) ([]BenchmarkSection, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse gold snapshot: %w", err)
	}
	sections := make([]BenchmarkSection, 0, len(page.Identifiers))
	for _, identifier := range page.Identifiers {
		selection := doc.Find(identifier.Container).First()
		if identifier.Parent != "" {
			selection = doc.Find(identifier.Parent).First().Find(identifier.Container).First()
		}
		if selection.Length() == 0 {
			if strict {
				return nil, fmt.Errorf("gold selector %q for %s matched no elements", identifier.Container, identifier.Name)
			}
			continue
		}
		selection = selection.Clone()
		if len(identifier.IgnoreTexts) > 0 {
			selection.Find("p").Each(func(_ int, paragraph *goquery.Selection) {
				text := normalizeText(paragraph.Text())
				for _, ignored := range identifier.IgnoreTexts {
					if text == normalizeText(ignored) {
						paragraph.Remove()
						break
					}
				}
			})
		}
		role := identifier.Role
		if role == "" && identifier.Name == "breadcrumb" {
			role = RoleNavigation
		}
		blockType := identifier.Type
		if blockType == "" {
			blockType = TypeNone
		}
		text := normalizeText(selection.Text())
		if text == "" && isMediaRole(role) {
			text = mediaSelectionText(selection)
		}
		text, err = applyGoldIgnoreRules(text, page.IgnoreRules, strict)
		if err != nil {
			return nil, fmt.Errorf("apply gold ignore rules for %s: %w", identifier.Name, err)
		}
		text, err = applyIdentifierIgnorePatterns(text, identifier.IgnorePatterns, strict)
		if err != nil {
			return nil, fmt.Errorf("apply identifier ignore rules for %s: %w", identifier.Name, err)
		}
		if text == "" {
			if strict {
				return nil, fmt.Errorf("gold selector %q for %s produced empty text", identifier.Container, identifier.Name)
			}
			continue
		}
		sections = append(sections, BenchmarkSection{BlockID: identifier.Name, Role: role, Type: blockType, ParseMode: identifier.ParseMode, Text: text})
	}
	if strict && len(sections) == 0 && len(page.Identifiers) > 0 {
		return nil, fmt.Errorf("gold page %s produced zero sections from %d identifiers", page.URL, len(page.Identifiers))
	}
	return sections, nil
}

func applyGoldIgnoreRules(text string, rules []GoldIgnoreRule, strict bool) (string, error) {
	for _, rule := range rules {
		compiled, compileErr := regexp.Compile(rule.Pattern)
		if compileErr == nil {
			text = compiled.ReplaceAllString(text, rule.Replacement)
		} else if strict {
			return "", compileErr
		}
	}
	return normalizeText(text), nil
}

func applyIdentifierIgnorePatterns(text string, rules []struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}, strict bool) (string, error) {
	for _, rule := range rules {
		compiled, compileErr := regexp.Compile(rule.Pattern)
		if compileErr == nil {
			text = compiled.ReplaceAllString(text, rule.Replacement)
		} else if strict {
			return "", compileErr
		}
	}
	return normalizeText(text), nil
}

func isMediaRole(role BlockRole) bool {
	return role == RoleMedia || role == RoleMediaGroup || role == RoleVideo
}

func mediaSelectionText(selection *goquery.Selection) string {
	var parts []string
	addAttr := func(attr string) {
		if value, ok := selection.Attr(attr); ok && strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	addAttr("alt")
	addAttr("title")
	addAttr("src")
	addAttr("poster")
	selection.Find("img, video, source").Each(func(_ int, child *goquery.Selection) {
		for _, attr := range []string{"alt", "title", "src", "poster"} {
			if value, ok := child.Attr(attr); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, strings.TrimSpace(value))
			}
		}
	})
	selection.Find("figcaption").Each(func(_ int, caption *goquery.Selection) {
		if text := normalizeText(caption.Text()); text != "" {
			parts = append(parts, text)
		}
	})
	if len(parts) == 0 {
		if html, err := goquery.OuterHtml(selection); err == nil {
			parts = append(parts, html)
		}
	}
	return normalizeText(strings.Join(parts, " "))
}
