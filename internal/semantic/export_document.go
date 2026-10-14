package semantic

import (
	"fmt"
	"time"
)

const DocumentSchemaVersion = "semantic-crawler/document-v1"

// ExportDocument is a portable content record. Numeric references belong to the
// source workspace; HTML remains untrusted source material.
type ExportDocument struct {
	SchemaVersion          string           `json:"schema_version"`
	Source                 ExportSource     `json:"source"`
	URL                    string           `json:"url"`
	Title                  string           `json:"title"`
	CandidateID            int64            `json:"candidate_id"`
	PageVersionID          int64            `json:"page_version_id"`
	RunID                  int64            `json:"run_id,omitempty"`
	Status                 CandidateStatus  `json:"status"`
	CapturedAt             *time.Time       `json:"captured_at,omitempty"`
	ApprovedAt             *time.Time       `json:"approved_at,omitempty"`
	ApprovedBy             string           `json:"approved_by,omitempty"`
	ContentHash            string           `json:"content_hash,omitempty"`
	Extraction             ExportProvenance `json:"extraction"`
	NewerUnapprovedVersion bool             `json:"newer_unapproved_version"`
	Sections               []ExportSection  `json:"sections"`
	legacyMarkdown         string
}
type ExportSource struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}
type ExportProvenance struct {
	Model                 string `json:"model,omitempty"`
	PromptVersion         string `json:"prompt_version,omitempty"`
	SourceInstructionHash string `json:"source_instruction_hash,omitempty"`
	Identity              string `json:"identity,omitempty"`
}
type ExportSection struct {
	BlockID         string    `json:"block_id"`
	Role            BlockRole `json:"role"`
	Severity        BlockType `json:"severity"`
	HTML            string    `json:"html"`
	Locator         string    `json:"locator"`
	LegacyParseMode *bool     `json:"legacy_parse_mode,omitempty"`
	parseMode       bool
}

func BuildExportDocument(source SourceEntry, page PageVersion, candidate Candidate) (ExportDocument, error) {
	if source.Slug == "" || source.Config.SourceID <= 0 || candidate.ID <= 0 || page.ID <= 0 || candidate.SourceID != source.Config.SourceID || page.SourceID != candidate.SourceID || candidate.PageVersionID != page.ID || page.URL == "" || candidate.URL != page.URL {
		return ExportDocument{}, fmt.Errorf("candidate, page and source references do not match")
	}
	if candidate.Status == CandidateApproved && candidate.ApprovedAt == nil {
		return ExportDocument{}, fmt.Errorf("approved candidate has no approval timestamp")
	}
	d := ExportDocument{SchemaVersion: DocumentSchemaVersion, Source: ExportSource{Slug: source.Slug, Name: source.Config.Name}, URL: page.URL, Title: page.Title, CandidateID: candidate.ID, PageVersionID: page.ID, RunID: page.RunID, Status: candidate.Status, ApprovedAt: candidate.ApprovedAt, ApprovedBy: candidate.ApprovedBy, ContentHash: page.ContentHash, Extraction: ExportProvenance{Identity: candidate.ExtractionIdentity, Model: candidate.Model, PromptVersion: candidate.PromptVersion, SourceInstructionHash: candidate.SourceSkillHash}, Sections: make([]ExportSection, 0, len(candidate.Sections)), legacyMarkdown: candidate.Markdown}
	if !page.ChangedAt.IsZero() {
		captured := page.ChangedAt
		d.CapturedAt = &captured
	}
	for _, s := range candidate.Sections {
		d.Sections = append(d.Sections, ExportSection{BlockID: s.BlockID, Role: s.Role, Severity: s.Type, HTML: s.Content, Locator: s.Locator, parseMode: s.ParseMode})
	}
	return d, nil
}
