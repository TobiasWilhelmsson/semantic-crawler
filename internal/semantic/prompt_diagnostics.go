package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PromptDiagnosticsOptions struct {
	FixtureDirectory string
	MappingPath      string
	URLContains      string
	Scope            string
	SourceSkill      SourceSkill
	Fetcher          Fetcher
	LiveFetch        bool
	StartPage        int
	MaxPages         int
	ReportDirectory  string
}

type PromptDiagnosticsReport struct {
	ExpectedURLs      int                     `json:"expected_urls"`
	DiscoveredURLs    int                     `json:"discovered_urls"`
	Pages             []PromptDiagnosticsPage `json:"pages"`
	TotalBlocks       int                     `json:"total_blocks"`
	TotalPromptChars  int                     `json:"total_prompt_chars"`
	MaxPromptChars    int                     `json:"max_prompt_chars"`
	MaxBlockCount     int                     `json:"max_block_count"`
	MaxSourceBlockLen int                     `json:"max_source_block_chars"`
}

type PromptDiagnosticsPage struct {
	Index               int            `json:"index"`
	URL                 string         `json:"url"`
	BlockCount          int            `json:"block_count"`
	PromptChars         int            `json:"prompt_chars"`
	SourceBlockChars    int            `json:"source_block_chars"`
	TotalBlockTextChars int            `json:"total_block_text_chars"`
	LargestBlockChars   int            `json:"largest_block_chars"`
	LinkHeavyBlocks     int            `json:"link_heavy_blocks"`
	TagCounts           map[string]int `json:"tag_counts"`
	FetchError          string         `json:"fetch_error,omitempty"`
}

func RunPromptDiagnostics(ctx context.Context, options PromptDiagnosticsOptions) (PromptDiagnosticsReport, error) {
	if err := ctx.Err(); err != nil {
		return PromptDiagnosticsReport{}, err
	}
	mapping, err := LoadGoldMappings(options.MappingPath, options.URLContains)
	if err != nil {
		return PromptDiagnosticsReport{}, err
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
	report := PromptDiagnosticsReport{ExpectedURLs: len(mapping.Pages)}
	for index, goldPage := range mapping.Pages {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		page := PromptDiagnosticsPage{Index: index + 1, URL: goldPage.URL, TagCounts: map[string]int{}}
		html, readErr := readBenchmarkHTML(ctx, BenchmarkOptions{
			FixtureDirectory: options.FixtureDirectory,
			Fetcher:          options.Fetcher,
			LiveFetch:        options.LiveFetch,
		}, goldPage)
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if readErr != nil {
			page.FetchError = readErr.Error()
			report.Pages = append(report.Pages, page)
			continue
		}
		report.DiscoveredURLs++
		blocks, _, buildErr := BuildCandidateBlocks(strings.NewReader(string(html)), goldPage.URL)
		if buildErr != nil {
			return PromptDiagnosticsReport{}, fmt.Errorf("build candidate blocks for %s: %w", goldPage.URL, buildErr)
		}
		prompt := buildExtractionPrompt(CrawlConfig{Scope: options.Scope, SourceSkill: options.SourceSkill}, PageVersion{URL: goldPage.URL, Title: pageTitle(string(html))}, blocks, nil)
		page.BlockCount = len(blocks)
		page.PromptChars = len(prompt)
		page.SourceBlockChars = len(blocksText(blocks, 0))
		for _, block := range blocks {
			textLen := len(block.Text)
			page.TotalBlockTextChars += textLen
			if textLen > page.LargestBlockChars {
				page.LargestBlockChars = textLen
			}
			if block.LinkCount >= 3 {
				page.LinkHeavyBlocks++
			}
			page.TagCounts[block.Tag]++
		}
		report.TotalBlocks += page.BlockCount
		report.TotalPromptChars += page.PromptChars
		if page.PromptChars > report.MaxPromptChars {
			report.MaxPromptChars = page.PromptChars
		}
		if page.BlockCount > report.MaxBlockCount {
			report.MaxBlockCount = page.BlockCount
		}
		if page.SourceBlockChars > report.MaxSourceBlockLen {
			report.MaxSourceBlockLen = page.SourceBlockChars
		}
		report.Pages = append(report.Pages, page)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if options.ReportDirectory != "" {
		if err := WritePromptDiagnosticsReport(options.ReportDirectory, report); err != nil {
			return report, err
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}

func WritePromptDiagnosticsReport(directory string, report PromptDiagnosticsReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create prompt diagnostics directory: %w", err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode prompt diagnostics report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "prompt-diagnostics.json"), content, 0o644); err != nil {
		return fmt.Errorf("write prompt diagnostics report: %w", err)
	}
	return nil
}
