package semantic

import (
	"context"
	"testing"
)

func TestSourceRegistryOverwritePreservesSourceIdentityAndRules(t *testing.T) {
	registry := NewSourceRegistry(t.TempDir())
	input := SourceWizardInput{Slug: "my-source", Name: "My source", SeedURL: "https://example.test/start/", Scope: "Keep text", SkillMarkdown: "Keep source text"}
	original, err := registry.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	if _, err := store.AddRule(context.Background(), SkillRule{SourceID: original.Config.SourceID, Scope: RuleScopeSource, Instruction: "retain warnings", Version: 2, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	input.Overwrite = true
	input.Scope = "Keep text and tables"
	updated, err := registry.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Config.SourceID != original.Config.SourceID {
		t.Errorf("overwrite changed source identity: original=%d updated=%d", original.Config.SourceID, updated.Config.SourceID)
	}
	rules, err := store.Rules(context.Background(), updated.Config.SourceID, updated.Config.SeedURL)
	if err != nil || len(rules) != 1 || rules[0].Instruction != "retain warnings" {
		t.Errorf("overwritten source lost its applicable rules: rules=%+v err=%v", rules, err)
	}
	if updated.Config.Scope != "Keep text and tables" {
		t.Errorf("overwrite did not update the source scope: %+v", updated.Config)
	}
}
