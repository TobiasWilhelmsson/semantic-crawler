package semantic

import (
	"fmt"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

func formatMarkdownSections(sections []Section) (string, error) {
	var result strings.Builder
	for _, section := range sections {
		if !section.ParseMode || strings.TrimSpace(section.Content) == "" {
			continue
		}
		markdown, err := htmltomarkdown.ConvertString(section.Content)
		if err != nil {
			return "", fmt.Errorf("convert section %q to Markdown: %w", section.Name, err)
		}
		markdown = strings.TrimSpace(markdown)
		if markdown == "" {
			continue
		}
		switch section.Role {
		case RoleTable:
			fmt.Fprintf(&result, "<!-- table-start: %s -->\n%s\n<!-- table-end: %s -->\n\n", section.Name, markdown, section.Name)
		case RoleInfoBox:
			result.WriteString("> [!IMPORTANT]\n")
			for _, line := range strings.Split(markdown, "\n") {
				if strings.TrimSpace(line) == "" {
					result.WriteString(">\n")
				} else {
					result.WriteString("> " + line + "\n")
				}
			}
			result.WriteString("\n")
		case RoleMetadata:
			result.WriteString("\n---\n\n" + markdown + "\n")
		default:
			result.WriteString(markdown + "\n\n")
		}
	}
	return strings.TrimSpace(result.String()) + "\n", nil
}
