package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"semantic-crawler/internal/semantic"
)

const websiteScope = "Keep the main page content, headings, lists, warnings, tables, links, images, and instructional media. Exclude navigation, cookie notices, advertisements, share controls, and other boilerplate."
const websiteInstructions = "# Website content\n\n## Scope\n" + websiteScope + "\n\n## Review\nPreserve source content without rewriting it. Flag uncertain selections for human review.\n"

func websiteURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	explicit := strings.Contains(raw, "://")
	if !explicit {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, fmt.Errorf("provide a website hostname or HTTP(S) URL without credentials, a query, or a fragment")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("website URL must use HTTP or HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if net.ParseIP(host) == nil {
		if len(host) > 253 || (!explicit && !strings.Contains(host, ".")) {
			return nil, fmt.Errorf("expected a website hostname such as example.com; use make help for commands")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, fmt.Errorf("website hostname is invalid")
			}
			for _, char := range label {
				if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
					return nil, fmt.Errorf("website hostname must use ASCII letters, digits, or hyphens")
				}
			}
		}
	}
	port := parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, fmt.Errorf("website port must be between 1 and 65535")
		}
		parsed.Host = net.JoinHostPort(host, port)
	} else {
		if strings.HasSuffix(parsed.Host, ":") {
			return nil, fmt.Errorf("website port is empty")
		}
		parsed.Host = host
		if strings.Contains(host, ":") {
			parsed.Host = "[" + host + "]"
		}
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed, nil
}

// Source creation reuses an exact normalized seed and never replaces an
// existing source's instructions, limits, identity, or scope.
func prepareWebsiteSource(root, raw string, pages, depth int) (semantic.SourceEntry, error) {
	if pages < 1 || pages > 1000 || depth < 0 || depth > 10 {
		return semantic.SourceEntry{}, fmt.Errorf("website page limit must be 1–1,000 and link depth 0–10")
	}
	target, err := websiteURL(raw)
	if err != nil {
		return semantic.SourceEntry{}, err
	}
	registry := semantic.NewSourceRegistry(root)
	statuses, err := registry.ListStatus()
	if err != nil {
		return semantic.SourceEntry{}, err
	}
	var matches []semantic.SourceEntry
	for _, status := range statuses {
		if status.Source == nil {
			continue
		}
		seed, err := websiteURL(status.Source.Config.SeedURL)
		if err == nil && seed.String() == target.String() {
			matches = append(matches, *status.Source)
		}
	}
	if len(matches) > 1 {
		return semantic.SourceEntry{}, fmt.Errorf("multiple sources use this URL; choose one with make start SOURCE=source-name")
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	label := strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
			return char
		}
		return '-'
	}, target.Hostname())
	label = strings.Join(strings.FieldsFunc(label, func(char rune) bool { return char == '-' }), "-")
	if len(label) > 48 {
		label = label[:48]
	}
	label = strings.Trim(label, "-")
	digest := sha256.Sum256([]byte(target.String()))
	slug := fmt.Sprintf("site-%s-%x", label, digest[:6])
	source, err := registry.Create(semantic.SourceWizardInput{Slug: slug, Name: target.Host, SeedURL: target.String(), AllowedHost: target.Hostname(), AllowedPathPrefix: target.Path, Scope: websiteScope, MaxPages: pages, MaxDepth: depth, SkillMarkdown: websiteInstructions})
	if err != nil {
		return semantic.SourceEntry{}, err
	}
	// The legacy wizard treats zero as an omitted default. Website scans allow
	// an explicit seed-only depth, so retain that value in newly created config.
	if depth == 0 {
		source.Config.MaxDepth = 0
		data, err := semantic.MarshalSourceConfig(source.Config)
		if err != nil {
			return semantic.SourceEntry{}, err
		}
		if err := os.WriteFile(source.ConfigPath, data, 0644); err != nil {
			return semantic.SourceEntry{}, fmt.Errorf("save seed-only source settings: %w", err)
		}
	}
	return source, nil
}
