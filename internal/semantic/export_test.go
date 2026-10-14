package semantic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newExportStore(t *testing.T) (*GormStore, SourceEntry) {
	t.Helper()
	store, err := OpenReviewStore(context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s, p, c := exportExample()
	p.ID = 0
	p, err = store.SavePageVersion(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	c.ID = 0
	c.PageVersionID = p.ID
	if _, err = store.SaveCandidate(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return store, s
}
func TestExportSelectionAndDraftBoundaries(t *testing.T) {
	ctx := context.Background()
	store, s := newExportStore(t)
	old, _ := store.Candidate(ctx, 1)
	draft := old
	draft.ID = 0
	draft.Status = CandidatePendingReview
	draft.ApprovedAt = nil
	draft.ApprovedBy = ""
	draft, err := store.SaveCandidate(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := store.ExportDocuments(ctx, s, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].CandidateID != 1 || !docs[0].NewerUnapprovedVersion {
		t.Fatalf("selection %#v", docs)
	}
	if _, err = store.ExportDocuments(ctx, s, ExportOptions{CandidateID: draft.ID}); err == nil {
		t.Fatal("exported unreviewed silently")
	}
	docs, err = store.ExportDocuments(ctx, s, ExportOptions{IncludeUnreviewed: true})
	if err != nil || len(docs) != 1 || docs[0].CandidateID != draft.ID {
		t.Fatalf("draft %#v %v", docs, err)
	}
	s.Config.SourceID = 99
	if _, err = store.ExportDocuments(ctx, s, ExportOptions{CandidateID: 1}); err == nil {
		t.Fatal("cross source export")
	}
}
func TestExportFilesAreCompleteAndNoClobber(t *testing.T) {
	s, p, c := exportExample()
	d, _ := BuildExportDocument(s, p, c)
	ctx := context.Background()
	for _, format := range []ExportFormat{ExportJSON, ExportJSONL, ExportMarkdown, ExportText} {
		t.Run(string(format), func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "result")
			if err := WriteExport(ctx, dest, []ExportDocument{d}, format, ProfileComplete); err != nil {
				t.Fatal(err)
			}
			if err := WriteExport(ctx, dest, nil, format, ProfileComplete); err == nil {
				t.Fatal("overwrote existing output")
			}
			if format == ExportJSON || format == ExportJSONL {
				b, err := os.ReadFile(dest)
				if err != nil {
					t.Fatal(err)
				}
				if !json.Valid(b) || !strings.Contains(string(b), "except students") {
					t.Fatalf("bad JSON %s", b)
				}
			} else {
				if _, err := os.Stat(filepath.Join(dest, "manifest.json")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestExportRefusesUnsafeDestinationsAndCancellation(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	os.Mkdir(real, 0700)
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteExport(context.Background(), filepath.Join(link, "out"), nil, ExportJSONL, ProfileComplete); err == nil {
		t.Fatal("accepted symlink")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(root, "cancelled")
	if err := WriteExport(ctx, dest, nil, ExportJSON, ProfileComplete); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("published cancelled output")
	}
	missing := filepath.Join(root, "missing.db")
	if _, err := OpenExportStore(context.Background(), missing); err == nil {
		t.Fatal("opened missing database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("created missing DB")
	}
}

func TestExportInvalidOrCancelledDoesNotCreateParents(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx := context.Background()
		format := ExportFormat("invalid")
		if cancelled {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
			format = ExportJSON
		}
		parent := filepath.Join(t.TempDir(), "new-parent")
		err := WriteExport(ctx, filepath.Join(parent, "out"), nil, format, ProfileComplete)
		if err == nil {
			t.Fatal("unexpected success")
		}
		if _, err := os.Stat(parent); !os.IsNotExist(err) {
			t.Fatal("created parents on invalid/cancelled request")
		}
	}
}
