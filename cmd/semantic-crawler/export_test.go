package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"semantic-crawler/internal/semantic"
	"strings"
	"testing"
	"time"
)

func TestExportSourceNeedsNoModelAndPreservesStorage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "data.db")
	store, err := semantic.OpenReviewStore(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.SavePageVersion(ctx, semantic.PageVersion{SourceID: 1, URL: "https://example.test/a", Title: "Policy"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, err = store.SaveCandidate(ctx, semantic.Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: semantic.CandidateApproved, ApprovedAt: &now, Sections: []semantic.Section{{Content: "<p>Fee 25 SEK except students.</p>"}}})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	before, _ := os.ReadFile(path)
	output := filepath.Join(root, "out.jsonl")
	selection := sourceSelection{Slug: "sample", DatabasePath: path, Config: semantic.SourceConfig{SourceID: 1, SourceSkillPath: "missing-instructions.md"}}
	if err = exportSource(ctx, selection, output, semantic.ExportJSONL, semantic.ProfileComplete, semantic.ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(output)
	if !strings.Contains(string(body), "except students") {
		t.Fatalf("output %s", body)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("export changed storage")
	}
}
func TestMakeExportDefaultOutputResolvesSourceWithoutEvaluatingInput(t *testing.T) {
	project, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "-n", "export", "SOURCE=city-services")
	command.Dir = project
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make: %v %s", err, output)
	}
	if !strings.Contains(string(output), "exports/city-services.jsonl") {
		t.Fatalf("wrong default output: %s", output)
	}
}
