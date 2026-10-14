package semantic

import (
	"strings"
	"testing"
)

func TestStandaloneMarkdownPreservesSectionFormatting(t *testing.T) {
	cases := []struct {
		name    string
		section Section
		want    string
	}{
		{"content", Section{Role: RoleContent, ParseMode: true, Content: "<p>Body text</p>"}, "Body text\n"},
		{"table", Section{Name: "checks", Role: RoleTable, ParseMode: true, Content: "<p>Check values</p>"}, "<!-- table-start: checks -->\nCheck values\n<!-- table-end: checks -->\n"},
		{"warning", Section{Role: RoleInfoBox, ParseMode: true, Content: "<p>Critical warning</p>"}, "> [!IMPORTANT]\n> Critical warning\n"},
		{"metadata", Section{Role: RoleMetadata, ParseMode: true, Content: "<p>Updated today</p>"}, "---\n\nUpdated today\n"},
		{"empty", Section{Role: RoleContent, ParseMode: true, Content: "   "}, "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatMarkdownSections([]Section{tc.section})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStandaloneMarkdownSkipsDisabledSections(t *testing.T) {
	got, err := formatMarkdownSections([]Section{
		{Role: RoleContent, ParseMode: false, Content: "<p>Excluded</p>"},
		{Role: RoleContent, ParseMode: true, Content: "<p>Included</p>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Excluded") || got != "Included\n" {
		t.Fatalf("parse mode was not respected: %q", got)
	}
}
