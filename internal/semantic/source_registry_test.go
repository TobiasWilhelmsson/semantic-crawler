package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceRegistryListsAndResolvesConfigs(t *testing.T) {
	root := t.TempDir()
	writeTestSourceConfig(t, root, "sample", SourceConfig{
		SourceID:          1,
		Name:              "Sample Source",
		SeedURL:           "https://source.example.test/start/",
		AllowedHost:       "source.example.test",
		AllowedPathPrefix: "/start/",
		Scope:             "Include clinical content.",
		MaxDepth:          2,
		MaxPages:          20,
		SkillVersion:      1,
		SourceSkillPath:   filepath.Join(root, "source-skills", "sample.md"),
	})

	registry := NewSourceRegistry(root)
	sources, err := registry.List()
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(sources) != 1 || sources[0].Slug != "sample" || sources[0].Config.Name != "Sample Source" {
		t.Fatalf("unexpected sources: %#v", sources)
	}

	source, err := registry.Get("sample")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.ConfigPath != filepath.Join(root, "sample.json") {
		t.Fatalf("unexpected config path: %q", source.ConfigPath)
	}
	if DefaultSemanticDBPath(source.Slug) != "data/semantic-sample.db" {
		t.Fatalf("unexpected default db path")
	}
}

func TestSourceRegistryCreateFromWizardDefaultsAndValidation(t *testing.T) {
	root := t.TempDir()
	registry := NewSourceRegistry(root)

	created, err := registry.Create(SourceWizardInput{
		Slug:          "my-source",
		Name:          "My Source",
		SeedURL:       "https://example.test/clinical/start/",
		Scope:         "Include clinical guidance and exclude boilerplate.",
		MaxDepth:      0,
		MaxPages:      0,
		SkillMarkdown: "# My Source\n\n## Scope\n\nKeep clinical text.\n",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if created.Config.AllowedHost != "example.test" || created.Config.AllowedPathPrefix != "/clinical/start/" {
		t.Fatalf("unexpected URL defaults: %#v", created.Config)
	}
	if created.Config.SourceID != 1 {
		t.Fatalf("expected first wizard source to receive source_id 1, got %d", created.Config.SourceID)
	}
	if created.Config.MaxDepth != 2 || created.Config.MaxPages != 20 || created.Config.SkillVersion != 1 {
		t.Fatalf("unexpected crawl defaults: %#v", created.Config)
	}
	if _, err := os.Stat(created.ConfigPath); err != nil {
		t.Fatalf("config was not written: %v", err)
	}
	skill, err := os.ReadFile(created.Config.SourceSkillPath)
	if err != nil || !strings.Contains(string(skill), "Keep clinical text.") {
		t.Fatalf("skill was not written: %v %s", err, string(skill))
	}

	if _, err := registry.Create(SourceWizardInput{Slug: "../bad", Name: "Bad", SeedURL: "https://example.test/", Scope: "Scope", SkillMarkdown: "# Skill"}); err == nil {
		t.Fatalf("expected invalid slug to fail")
	}
	if _, err := registry.Create(SourceWizardInput{Slug: "my-source", Name: "Duplicate", SeedURL: "https://example.test/", Scope: "Scope", SkillMarkdown: "# Skill"}); err == nil {
		t.Fatalf("expected duplicate source to fail without overwrite")
	}
}

func TestSourceRegistryCreateDefaultsPortURLToHostnameScope(t *testing.T) {
	created, err := NewSourceRegistry(t.TempDir()).Create(SourceWizardInput{
		Slug: "local-source", Name: "Local source", SeedURL: "http://127.0.0.1:12345/guide/", Scope: "Guide", SkillMarkdown: "# Scope",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Config.AllowedHost != "127.0.0.1" {
		t.Fatalf("seed port leaked into hostname scope: %q", created.Config.AllowedHost)
	}
	if err := validateURLScope(created.Config.SeedURL, created.Config.AllowedHost, created.Config.AllowedPathPrefix); err != nil {
		t.Fatalf("created source cannot crawl its seed: %v", err)
	}
}

func TestSourceRegistryCreateRejectsOutOfScopeSeedsBeforeWriting(t *testing.T) {
	for _, test := range []struct{ name, seed, host, prefix string }{
		{"host", "https://other.example.test/guide/", "example.test", "/guide/"},
		{"path", "https://example.test/outside/", "example.test", "/guide/"},
		{"credentials", "https://user:password@example.test/guide/", "example.test", "/guide/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := NewSourceRegistry(root).Create(SourceWizardInput{
				Slug: "invalid-source", Name: "Invalid source", SeedURL: test.seed, AllowedHost: test.host, AllowedPathPrefix: test.prefix, Scope: "Guide", SkillMarkdown: "# Scope",
			})
			if err == nil {
				t.Fatal("created a source whose seed is outside its scope")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid source wrote files: entries=%v error=%v", entries, err)
			}
		})
	}
}

func writeTestSourceConfig(t *testing.T, root, slug string, config SourceConfig) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := MarshalSourceConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, slug+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
