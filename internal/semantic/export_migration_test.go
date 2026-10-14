package semantic

import (
	"context"
	"os"
	"path/filepath"
	"semantic-crawler/internal/models"
	"testing"
)

func TestExportReadsLegacyDatabaseWithoutMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	store, err := OpenReviewStore(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	s, p, c := exportExample()
	p.ID = 0
	p, err = store.SavePageVersion(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	c.ID = 0
	c.PageVersionID = p.ID
	c, err = store.SaveCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&models.SemanticPageVersion{}, &models.SemanticCandidate{}} {
		if err = store.db.Migrator().DropColumn(model, "ExtractionIdentity"); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenExportStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	documents, err := reader.ExportDocuments(ctx, s, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("read-only export changed legacy database")
	}
	if len(documents) != 1 || documents[0].Extraction.Identity != "" || documents[0].Status != CandidateApproved {
		t.Fatalf("legacy provenance %#v", documents)
	}
	store, err = OpenReviewStore(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	candidate, err := store.Candidate(ctx, c.ID)
	if err != nil || candidate.Status != CandidateApproved || candidate.Markdown != c.Markdown {
		t.Fatalf("migration damaged approval %v", err)
	}
}
