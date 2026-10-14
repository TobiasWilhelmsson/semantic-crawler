package semantic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SnapshotFetcher struct {
	Directory string
}

func (fetcher *SnapshotFetcher) Fetch(_ context.Context, url string) (FetchResult, error) {
	slug := strings.TrimSuffix(url, "/")
	slug = slug[strings.LastIndex(slug, "/")+1:]
	data, err := os.ReadFile(filepath.Join(fetcher.Directory, slug+".html"))
	if err != nil {
		return FetchResult{}, fmt.Errorf("read snapshot for %s: %w", url, err)
	}
	return FetchResult{URL: url, StatusCode: 200, HTML: string(data)}, nil
}
