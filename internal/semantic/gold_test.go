package semantic

import (
	"os"
	"testing"
)

func TestLoadGoldMappingsExtractsHeldOutSectionsFromSnapshot(t *testing.T) {
	config, err := LoadGoldMappings("../../tests/fixtures/generic-health-sample/baseline.json", "/airway/")
	if err != nil {
		t.Fatalf("load mappings: %v", err)
	}
	if len(config.Pages) != 4 {
		t.Fatalf("expected four synthetic pages, got %d", len(config.Pages))
	}
	html, err := os.ReadFile("../../tests/fixtures/generic-health-sample/preparation.html")
	if err != nil {
		t.Fatal(err)
	}
	var page GoldPage
	for _, candidate := range config.Pages {
		if candidate.Slug == "preparation" {
			page = candidate
			break
		}
	}
	sections, err := ExtractGoldSections(page, html)
	if err != nil {
		t.Fatalf("extract gold sections: %v", err)
	}
	if len(sections) != 6 {
		t.Fatalf("expected six mapped synthetic sections, got %d sections", len(sections))
	}
	foundCritical, foundTable := false, false
	for _, section := range sections {
		foundCritical = foundCritical || section.Type == TypeCritical
		foundTable = foundTable || section.Role == RoleTable
	}
	if !foundCritical || !foundTable {
		t.Fatalf("gold extraction lost critical/table classifications: %#v", sections)
	}
}

func TestGoldExtractionAppliesTopLevelIgnoreRules(t *testing.T) {
	directory := t.TempDir()
	mappingPath := directory + "/mapping.json"
	mapping := `{
		"ignore": [{"pattern": "\\[\\d+\\]"}, {"pattern": "\\s*\\([Bb]ild\\s*\\d+\\)"}],
		"urls": [{
			"url": "https://example.test/topic/a/",
			"identifier": [{"name": "body", "role": "content", "container": "main", "parse_mode": true}]
		}]
	}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := LoadGoldMappings(mappingPath, "/topic/")
	if err != nil {
		t.Fatalf("load mappings: %v", err)
	}
	sections, err := ExtractGoldSections(config.Pages[0], []byte(`<html><body><main>Keep this [1] sentence (Bild 3).</main></body></html>`))
	if err != nil {
		t.Fatalf("extract gold sections: %v", err)
	}
	if len(sections) != 1 || sections[0].Text != "Keep this sentence." {
		t.Fatalf("global ignore rules were not applied: %#v", sections)
	}
}

func TestGoldExtractionPreservesTextlessMediaSelectors(t *testing.T) {
	directory := t.TempDir()
	mappingPath := directory + "/mapping.json"
	mapping := `{
		"urls": [{
			"url": "https://example.test/topic/a/",
			"identifier": [{"name": "hero", "role": "media", "container": "img.hero", "parse_mode": false}]
		}]
	}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := LoadGoldMappings(mappingPath, "/topic/")
	if err != nil {
		t.Fatalf("load mappings: %v", err)
	}
	sections, err := ExtractGoldSections(config.Pages[0], []byte(`<html><body><main><img class="hero" alt="Airway suction illustration" src="/images/suction.png"></main></body></html>`))
	if err != nil {
		t.Fatalf("extract gold sections: %v", err)
	}
	if len(sections) != 1 || sections[0].Role != RoleMedia || sections[0].Text != "Airway suction illustration /images/suction.png" {
		t.Fatalf("media gold selector was not preserved: %#v", sections)
	}
}
