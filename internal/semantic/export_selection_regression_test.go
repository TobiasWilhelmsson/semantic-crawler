package semantic

import (
	"context"
	"testing"
)

// An unselected record must not prevent recovery/export of healthy content.
func TestExportIgnoresMalformedUnselectedCandidates(t *testing.T) {
	for _, other := range []struct {
		name   string
		source int64
		status CandidateStatus
		url    string
	}{
		{"different source", 99, CandidateApproved, "https://other.test"},
		{"rejected revision", 1, CandidateRejected, "https://example.test/fees"},
		{"unreviewed revision", 1, CandidatePendingReview, "https://example.test/fees"},
	} {
		t.Run(other.name, func(t *testing.T) {
			store, source := newExportStore(t)
			ctx := context.Background()
			c, err := store.Candidate(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			c.ID = 0
			c.SourceID = other.source
			c.Status = other.status
			c.URL = other.url
			c, err = store.SaveCandidate(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.db.Exec("UPDATE semantic_candidates SET sections = ? WHERE id = ?", `{"malformed":"sections must be an array"}`, c.ID).Error; err != nil {
				t.Fatal(err)
			}
			docs, err := store.ExportDocuments(ctx, source, ExportOptions{})
			if err != nil {
				t.Fatalf("unselected candidate blocked healthy export: %v", err)
			}
			if len(docs) != 1 || docs[0].CandidateID != 1 {
				t.Fatalf("wrong export: %#v", docs)
			}
			if other.source == 1 && !docs[0].NewerUnapprovedVersion {
				t.Fatal("lost newer revision warning")
			}
			if other.source == 1 {
				if _, err = store.ExportDocuments(ctx, source, ExportOptions{CandidateID: c.ID, IncludeUnreviewed: true}); err == nil {
					t.Fatal("malformed selected candidate must still fail")
				}
			}
		})
	}
}
