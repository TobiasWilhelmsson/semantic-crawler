package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"semantic-crawler/internal/semantic"
)

func TestScanServerMissingProviderStillOpensSourceWorkspace(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_API_KEY", "")
	source := semantic.SourceEntry{Slug: "sample", Config: semantic.SourceConfig{SourceID: 2}}
	workspace, err := openReviewWorkspace(context.Background(), source, filepath.Join(t.TempDir(), "source.db"), true, serverModelResolver, nil, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Store.Close()
	if _, _, err = serverModelResolver(source, "openai"); err == nil {
		t.Fatal("provider unexpectedly ready")
	}
	if workspace.Retrier == nil || workspace.Reviews == nil {
		t.Fatal("missing review services")
	}
}
func TestScanServerReadOnlyOpeningDoesNotCreateDatabase(t *testing.T) {
	_, err := openReviewWorkspace(context.Background(), semantic.SourceEntry{}, filepath.Join(t.TempDir(), "missing.db"), false, serverModelResolver, nil, "fixture")
	if !errors.Is(err, semantic.ErrWorkspaceEmpty) {
		t.Fatalf("missing storage %v", err)
	}
}
