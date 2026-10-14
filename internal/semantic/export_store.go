package semantic

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"

	"semantic-crawler/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type ExportOptions struct {
	CandidateID       int64
	IncludeUnreviewed bool
}

// OpenExportStore opens existing storage read-only and never migrates it.
func OpenExportStore(ctx context.Context, path string) (*GormStore, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	location := url.URL{Scheme: "file", Path: absolute}
	q := url.Values{"mode": {"ro"}, "_busy_timeout": {"5000"}}
	location.RawQuery = q.Encode()
	db, err := gorm.Open(sqlite.Open(location.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open existing export database: %w", err)
	}
	raw, err := db.DB()
	if err != nil {
		return nil, err
	}
	raw.SetMaxOpenConns(1)
	if err = raw.PingContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return NewGormStore(db), nil
}

func (store *GormStore) ExportDocuments(ctx context.Context, source SourceEntry, options ExportOptions) ([]ExportDocument, error) {
	if source.Config.SourceID <= 0 || source.Slug == "" || options.CandidateID < 0 {
		return nil, fmt.Errorf("valid explicit source and candidate selection required")
	}
	result := []ExportDocument{}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reader := NewGormStore(tx)
		// Select using lightweight metadata before decoding content. Malformed
		// unselected revisions must not block recovery of healthy documents, and
		// historical model responses need not be loaded for an export.
		var candidates []models.SemanticCandidate
		if err := tx.Model(&models.SemanticCandidate{}).
			Select("id", "origin_url", "status").
			Where("source_id = ?", source.Config.SourceID).Find(&candidates).Error; err != nil {
			return err
		}
		type selection struct {
			ID     int64
			URL    string
			Status CandidateStatus
		}
		latest := map[string]selection{}
		newest := map[string]selection{}
		for _, record := range candidates {
			c := selection{ID: record.ID, URL: record.OriginURL, Status: CandidateStatus(record.Status)}
			if c.ID > newest[c.URL].ID {
				newest[c.URL] = c
			}
			eligible := c.Status == CandidateApproved || options.IncludeUnreviewed && (c.Status == CandidatePendingReview || c.Status == CandidateEscalated || c.Status == CandidateQueued)
			if options.CandidateID > 0 {
				eligible = c.ID == options.CandidateID
				if eligible && c.Status != CandidateApproved && !options.IncludeUnreviewed {
					return fmt.Errorf("candidate is not approved; explicitly include unreviewed content")
				}
			}
			if eligible && c.ID > latest[c.URL].ID {
				latest[c.URL] = c
			}
		}
		if options.CandidateID > 0 && len(latest) == 0 {
			return fmt.Errorf("candidate not found in selected source")
		}
		urls := make([]string, 0, len(latest))
		for u := range latest {
			urls = append(urls, u)
		}
		sort.Strings(urls)
		for _, u := range urls {
			if err := ctx.Err(); err != nil {
				return err
			}
			c, err := reader.Candidate(ctx, latest[u].ID)
			if err != nil {
				return err
			}
			page, err := reader.PageVersionByID(ctx, c.PageVersionID)
			if err != nil {
				return err
			}
			d, err := BuildExportDocument(source, page, c)
			if err != nil {
				return err
			}
			d.NewerUnapprovedVersion = newest[u].ID > c.ID && newest[u].Status != CandidateApproved
			result = append(result, d)
		}
		return nil
	}, &sql.TxOptions{ReadOnly: true})
	return result, err
}
