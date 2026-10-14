package semantic

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func exportExample() (SourceEntry, PageVersion, Candidate) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	return SourceEntry{Slug: "public-services", Config: SourceConfig{SourceID: 1, Name: "Public services"}}, PageVersion{ID: 2, SourceID: 1, RunID: 3, URL: "https://example.test/fees", Title: "Fees – details", ContentHash: "hash", ChangedAt: now}, Candidate{ID: 4, SourceID: 1, PageVersionID: 2, URL: "https://example.test/fees", Status: CandidateApproved, ApprovedAt: &now, ApprovedBy: "Reviewer", Model: "local-model", RawResponse: "PRIVATE_RESPONSE", AppliedRules: json.RawMessage(`{"private":"PRIVATE_RULE"}`), Markdown: "legacy", Sections: []Section{{BlockID: "b0001", Role: RoleInfoBox, Type: TypeCritical, Content: "<aside>Pay 25 SEK by 31 October, except students.</aside>", Locator: "aside"}}}
}

func TestExportDocumentPreservesContentAndLimitsDisclosure(t *testing.T) {
	source, page, candidate := exportExample()
	doc, err := BuildExportDocument(source, page, candidate)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"semantic-crawler/document-v1", "Fees – details", "Pay 25 SEK", "critical", "approved"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s: %s", want, b)
		}
	}
	for _, secret := range []string{"PRIVATE_RESPONSE", "PRIVATE_RULE", "parse_mode", "legacy_markdown"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("leaked %s", secret)
		}
	}
	var decoded ExportDocument
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(decoded)
	if string(again) != string(b) {
		t.Fatal("unstable round trip")
	}
	if len(doc.Sections) != 1 || doc.Sections[0].HTML != candidate.Sections[0].Content {
		t.Fatal("selected content changed")
	}
}
func TestExportDocumentRejectsMismatchedOwnership(t *testing.T) {
	for _, mutate := range []func(*SourceEntry, *PageVersion, *Candidate){func(s *SourceEntry, p *PageVersion, c *Candidate) { s.Config.SourceID = 9 }, func(s *SourceEntry, p *PageVersion, c *Candidate) { p.ID = 9 }, func(s *SourceEntry, p *PageVersion, c *Candidate) { p.URL = "https://other.test" }, func(s *SourceEntry, p *PageVersion, c *Candidate) { c.ApprovedAt = nil }} {
		s, p, c := exportExample()
		mutate(&s, &p, &c)
		if _, err := BuildExportDocument(s, p, c); err == nil {
			t.Fatal("accepted inconsistent document")
		}
	}
}
func TestExportDocumentPreservesDraftAndUnknownProvenance(t *testing.T) {
	s, p, c := exportExample()
	c.Status = CandidatePendingReview
	c.ApprovedAt = nil
	c.ApprovedBy = ""
	p.ChangedAt = time.Time{}
	p.ContentHash = ""
	d, err := BuildExportDocument(s, p, c)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != CandidatePendingReview || d.CapturedAt != nil || d.ApprovedAt != nil {
		t.Fatal("invented provenance")
	}
}
