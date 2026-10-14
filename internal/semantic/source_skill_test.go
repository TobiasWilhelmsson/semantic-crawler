package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSourceSkillComputesStableHashAndPromptUsesIt(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "skill.md")
	content := "# Source Skill\n\n## Always Keep\n- Keep warning boxes."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	skill, err := LoadSourceSkill(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Version != 3 || skill.Hash == "" || !strings.Contains(skill.Content, "Keep warning boxes") {
		t.Fatalf("unexpected source skill: %#v", skill)
	}
	prompt := buildExtractionPrompt(CrawlConfig{Scope: "clinical", SourceSkill: skill}, PageVersion{URL: "https://example.test/a"}, []CandidateBlock{{ID: "b0001", Tag: "aside", Text: "Warning"}}, nil)
	if !strings.Contains(prompt, "Source skill guide:") || !strings.Contains(prompt, "Keep warning boxes") || strings.Contains(prompt, "<head>") {
		t.Fatalf("prompt did not include optimized source skill/body blocks: %s", prompt)
	}
	if !strings.Contains(prompt, "role_hint=info_box") || !strings.Contains(prompt, "type_hint=info") {
		t.Fatalf("prompt should expose semantic hints for source blocks: %s", prompt)
	}
}

func TestExtractionPromptIncludesSemanticHintsForTablesAndMedia(t *testing.T) {
	prompt := buildExtractionPrompt(CrawlConfig{Scope: "clinical"}, PageVersion{URL: "https://example.test/a"}, []CandidateBlock{
		{ID: "b0001", Tag: "table", Text: "Dos 5 mg"},
		{ID: "b0002", Tag: "figure", Text: "Clinical image caption"},
	}, nil)
	for _, expected := range []string{"b0001 <table> role_hint=table", "b0002 <figure> role_hint=media"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
}
