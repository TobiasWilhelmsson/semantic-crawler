package semantic

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"strconv"
)

func registerExportRoutes(router *gin.Engine, store ReviewWebStore, options ReviewRouterOptions, base string) {
	router.GET(base+"/export", func(c *gin.Context) {
		if c.Query("download") != "1" {
			writePage(c, "Export content", "sources", uiTemplate("export"), map[string]any{"CandidateID": c.Query("candidate_id")})
			return
		}
		storage, ok := store.(*GormStore)
		if !ok {
			c.String(501, "Exports require a saved source workspace.")
			return
		}
		format := ExportFormat(c.DefaultQuery("format", "jsonl"))
		profile := ContentProfile(c.DefaultQuery("content_profile", "complete"))
		var id int64
		var err error
		if raw := c.Query("candidate_id"); raw != "" {
			id, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				c.String(422, "Invalid candidate selection.")
				return
			}
		}
		source := SourceEntry{Slug: options.SourceSlug, Config: SourceConfig{SourceID: options.SourceID, Name: options.SourceName}}
		docs, err := storage.ExportDocuments(c.Request.Context(), source, ExportOptions{CandidateID: id, IncludeUnreviewed: c.Query("include_unreviewed") == "1"})
		if err != nil {
			c.String(422, "Cannot export: %v", err)
			return
		}
		data, name, mime, err := ExportDownload(c.Request.Context(), docs, format, profile)
		if err != nil {
			c.String(422, "Cannot export: %v", err)
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		c.Header("X-Document-Count", strconv.Itoa(len(docs)))
		c.Data(200, mime, data)
	})
}
