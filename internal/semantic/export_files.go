package semantic

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type ExportManifest struct {
	SchemaVersion string             `json:"schema_version"`
	Format        ExportFormat       `json:"format"`
	Profile       ContentProfile     `json:"content_profile"`
	Count         int                `json:"document_count"`
	Documents     []ExportFileRecord `json:"documents"`
}
type ExportFileRecord struct {
	File     string         `json:"file"`
	Document ExportDocument `json:"document"`
	Warnings []string       `json:"warnings"`
}
type exportFile struct {
	name string
	data []byte
}

func prepareExport(ctx context.Context, docs []ExportDocument, format ExportFormat, profile ContentProfile) ([]exportFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if profile != ProfileComplete && profile != ProfileLegacy {
		return nil, fmt.Errorf("unknown content profile %q", profile)
	}
	if format != ExportJSON && format != ExportJSONL && format != ExportMarkdown && format != ExportText {
		return nil, fmt.Errorf("unknown export format %q", format)
	}
	// Work on copies: a legacy request must not contaminate another export.
	prepared := make([]ExportDocument, len(docs))
	for i, d := range docs {
		prepared[i] = d
		prepared[i].Sections = append([]ExportSection{}, d.Sections...)
		if profile == ProfileLegacy {
			for j := range prepared[i].Sections {
				v := prepared[i].Sections[j].parseMode
				prepared[i].Sections[j].LegacyParseMode = &v
			}
		}
	}
	if format == ExportJSON {
		data, err := json.MarshalIndent(struct {
			SchemaVersion string           `json:"schema_version"`
			Profile       ContentProfile   `json:"content_profile"`
			Count         int              `json:"document_count"`
			Documents     []ExportDocument `json:"documents"`
		}{"semantic-crawler/dataset-v1", profile, len(prepared), prepared}, "", "  ")
		return []exportFile{{"documents.json", append(data, '\n')}}, err
	}
	if format == ExportJSONL {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, d := range prepared {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := enc.Encode(d); err != nil {
				return nil, err
			}
		}
		return []exportFile{{"documents.jsonl", buf.Bytes()}}, nil
	}
	manifest := ExportManifest{SchemaVersion: "semantic-crawler/export-v1", Format: format, Profile: profile, Count: len(prepared), Documents: []ExportFileRecord{}}
	files := make([]exportFile, 0, len(prepared)+1)
	seen := map[string]bool{}
	for _, d := range prepared {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rendered, err := RenderExportDocument(d, format, profile)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256([]byte(d.Source.Slug + "\n" + d.URL))
		ext := "md"
		if format == ExportText {
			ext = "txt"
		}
		name := fmt.Sprintf("page-%x-%d.%s", hash[:12], d.CandidateID, ext)
		if seen[name] {
			return nil, fmt.Errorf("duplicate export document")
		}
		seen[name] = true
		files = append(files, exportFile{name, rendered.Content})
		manifest.Documents = append(manifest.Documents, ExportFileRecord{name, d, rendered.Warnings})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	files = append(files, exportFile{"manifest.json", append(data, '\n')})
	return files, nil
}

// ExportDownload builds a complete attachment before HTTP headers are sent.
func ExportDownload(ctx context.Context, docs []ExportDocument, format ExportFormat, profile ContentProfile) ([]byte, string, string, error) {
	files, err := prepareExport(ctx, docs, format, profile)
	if err != nil {
		return nil, "", "", err
	}
	if format == ExportJSON {
		return files[0].data, "documents.json", "application/json", nil
	}
	if format == ExportJSONL {
		return files[0].data, "documents.jsonl", "application/x-ndjson", nil
	}
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, "", "", err
		}
		entry, err := archive.Create(f.name)
		if err != nil {
			return nil, "", "", err
		}
		if _, err = entry.Write(f.data); err != nil {
			return nil, "", "", err
		}
	}
	if err = archive.Close(); err != nil {
		return nil, "", "", err
	}
	return buf.Bytes(), "documents.zip", "application/zip", nil
}

func WriteExport(ctx context.Context, destination string, docs []ExportDocument, format ExportFormat, profile ContentProfile) error {
	files, err := prepareExport(ctx, docs, format, profile)
	if err != nil {
		return err
	}
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("output path is required")
	}
	for _, part := range strings.Split(filepath.ToSlash(destination), "/") {
		if part == ".." {
			return fmt.Errorf("output path must not contain parent traversal")
		}
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err = checkExportParents(filepath.Dir(absolute)); err != nil {
		return err
	}
	if _, err = os.Lstat(absolute); err == nil {
		return fmt.Errorf("output already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(absolute)
	// Keep staging beside the destination so publication stays on one filesystem.
	staging, err := os.MkdirTemp(parent, ".semantic-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for _, f := range files {
		if err = ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(staging, f.name)
		if err = os.WriteFile(p, f.data, 0600); err != nil {
			return err
		}
		file, e := os.OpenFile(p, os.O_RDWR, 0)
		if e != nil {
			return e
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if format == ExportJSON || format == ExportJSONL {
		return os.Link(filepath.Join(staging, files[0].name), absolute)
	}
	return renameExportDirectory(staging, absolute)
}
func checkExportParents(parent string) error {
	// macOS uses /var -> /private/var and /tmp -> /private/tmp. Resolve those
	// system roots, but reject user-provided symlinks in output directories.
	clean := filepath.Clean(parent)
	if runtime.GOOS == "darwin" && strings.HasPrefix(clean, "/var/") {
		clean = "/private" + clean
	}
	if runtime.GOOS == "darwin" && (clean == "/tmp" || strings.HasPrefix(clean, "/tmp/")) {
		clean = "/private" + clean
	}
	parts := strings.Split(clean, string(filepath.Separator))
	current := filepath.VolumeName(clean) + string(filepath.Separator)
	for _, part := range parts {
		if part == "" || part == filepath.VolumeName(clean) {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("output parent is not a real directory: %s", current)
		}
	}
	return nil
}
