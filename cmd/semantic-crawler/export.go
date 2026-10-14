package main

import (
	"context"
	"fmt"
	"semantic-crawler/internal/semantic"
)

func exportSource(ctx context.Context, selection sourceSelection, output string, format semantic.ExportFormat, profile semantic.ContentProfile, options semantic.ExportOptions) error {
	store, err := semantic.OpenExportStore(ctx, selection.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	source := semantic.SourceEntry{Slug: selection.Slug, ConfigPath: selection.ConfigPath, Config: selection.Config}
	documents, err := store.ExportDocuments(ctx, source, options)
	if err != nil {
		return err
	}
	if err = semantic.WriteExport(ctx, output, documents, format, profile); err != nil {
		return err
	}
	fmt.Printf("Exported %d documents to %s (%s, %s).\n", len(documents), output, format, profile)
	return nil
}
