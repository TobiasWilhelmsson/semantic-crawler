package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"semantic-crawler/internal/models"
)

type WorkspaceOpener func(context.Context, SourceEntry, string, bool) (*SourceWorkspace, error)
type ModelResolver func(SourceEntry, string) (SemanticModel, string, error)
type SourceWorkspaces struct {
	mu        sync.Mutex
	registry  SourceRegistry
	startup   SourceEntry
	startupDB string
	dataRoot  string
	opener    WorkspaceOpener
	open      map[string]*SourceWorkspace
}

func NewSourceWorkspaces(registry SourceRegistry, startup SourceEntry, startupDB string, opener WorkspaceOpener, dataRoot ...string) *SourceWorkspaces {
	root := "data"
	if len(dataRoot) > 0 {
		root = dataRoot[0]
	}
	return &SourceWorkspaces{registry: registry, startup: startup, startupDB: startupDB, dataRoot: root, opener: opener, open: make(map[string]*SourceWorkspace)}
}
func (sources *SourceWorkspaces) Lookup(slug string) (SourceEntry, string, error) {
	if err := validateSourceSlug(slug); err != nil {
		return SourceEntry{}, "", err
	}
	if slug == sources.startup.Slug {
		entry := sources.startup
		if entry.ConfigPath != "" {
			config, err := LoadSourceConfigFile(entry.ConfigPath)
			if err != nil {
				return SourceEntry{}, "", err
			}
			entry.Config = config
		}
		if entry.Config.SourceID <= 0 {
			return SourceEntry{}, "", fmt.Errorf("source ID must be positive")
		}
		return entry, sources.startupDB, nil
	}
	entry, err := sources.registry.Get(slug)
	if err != nil {
		return SourceEntry{}, "", err
	}
	if entry.Config.SourceID <= 0 {
		return SourceEntry{}, "", fmt.Errorf("source ID must be positive")
	}
	return entry, filepath.Join(sources.dataRoot, "semantic-"+slug+".db"), nil
}
func (sources *SourceWorkspaces) Resolve(ctx context.Context, slug string, create bool) (*SourceWorkspace, error) {
	sources.mu.Lock()
	defer sources.mu.Unlock()
	entry, path, err := sources.Lookup(slug)
	if err != nil {
		return nil, err
	}
	identity, err := canonicalScanPath(path)
	if err != nil {
		return nil, err
	}
	for other, workspace := range sources.open {
		if other != slug {
			existing, err := canonicalScanPath(workspace.DBPath)
			if err != nil {
				return nil, err
			}
			if existing == identity {
				return nil, fmt.Errorf("this database is already assigned to another source workspace")
			}
		}
	}
	if workspace := sources.open[slug]; workspace != nil {
		if workspace.Source.Config.SourceID != entry.Config.SourceID {
			return nil, fmt.Errorf("source identity changed; restart the review server")
		}
		if create {
			if err = workspace.Store.Migrate(ctx); err != nil {
				return nil, err
			}
		}
		return workspace, nil
	}
	if sources.opener == nil {
		return nil, fmt.Errorf("source workspace opening is unavailable")
	}
	workspace, err := sources.opener(ctx, entry, path, create)
	if err != nil {
		return nil, err
	}
	sources.open[slug] = workspace
	return workspace, nil
}

// Recover is called during explicit server setup or source creation, never by a
// progress GET. Active external owners keep their records unchanged.
func (sources *SourceWorkspaces) Recover(ctx context.Context, slug string) error {
	entry, path, err := sources.Lookup(slug)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lease, err := AcquireScanLock(path)
	if errors.Is(err, ErrScanBusy) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	workspace, err := sources.Resolve(ctx, slug, false)
	if err != nil {
		return err
	}
	return recoverScanRecords(ctx, workspace.Store, entry.Config.SourceID)
}
func (sources *SourceWorkspaces) Close() error {
	sources.mu.Lock()
	defer sources.mu.Unlock()
	var err error
	for _, workspace := range sources.open {
		if workspace.Store != nil {
			err = errors.Join(err, workspace.Store.Close())
		}
	}
	sources.open = make(map[string]*SourceWorkspace)
	return err
}
func (sources *SourceWorkspaces) List() ([]SourceStatus, error) {
	statuses, err := sources.registry.ListStatus()
	if err != nil {
		return nil, err
	}
	found := false
	startupStatus := SourceStatus{Slug: sources.startup.Slug}
	entry, _, lookupErr := sources.Lookup(sources.startup.Slug)
	if lookupErr != nil {
		startupStatus.Error = "Source configuration is invalid; check its JSON file and positive source_id."
	} else {
		startupStatus.Source = &entry
	}
	for i := range statuses {
		if statuses[i].Slug == sources.startup.Slug {
			statuses[i] = startupStatus
			found = true
		}
	}
	if !found {
		statuses = append([]SourceStatus{startupStatus}, statuses...)
	}
	return statuses, nil
}
func OpenReviewStore(ctx context.Context, path string, create bool) (*GormStore, error) {
	if !create {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil, ErrWorkspaceEmpty
		} else if err != nil {
			return nil, err
		}
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	location := url.URL{Scheme: "file", Path: absolute}
	query := url.Values{"_busy_timeout": {"5000"}}
	if !create {
		query.Set("mode", "rw")
	}
	location.RawQuery = query.Encode()
	db, err := gorm.Open(sqlite.Open(location.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	sql, err := db.DB()
	if err != nil {
		return nil, err
	}
	sql.SetMaxOpenConns(1)
	store := NewGormStore(db)
	if create {
		if err = store.Migrate(ctx); err != nil {
			sql.Close()
			return nil, err
		}
	}
	return store, nil
}
func (store *GormStore) Migrate(ctx context.Context) error {
	return store.db.WithContext(ctx).AutoMigrate(&models.SemanticSource{}, &models.SemanticCrawlRun{}, &models.SemanticQueueItem{}, &models.SemanticPageVersion{}, &models.SemanticCandidate{}, &models.SemanticReview{}, &models.SemanticSkillRule{}, &models.SemanticDelivery{})
}
func (store *GormStore) Close() error {
	db, err := store.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
func PrepareScan(ctx context.Context, sources *SourceWorkspaces, resolve ModelResolver, request ScanRequest, fetch FetchSettings) (PreparedScan, error) {
	if err := ctx.Err(); err != nil {
		return PreparedScan{}, err
	}
	if request.MaxPages < 1 || request.MaxPages > 1000 || request.MaxDepth < 0 || request.MaxDepth > 10 {
		return PreparedScan{}, fmt.Errorf("page limit must be 1–1,000 and depth must be 0–10")
	}
	if request.ModelMode != "fixture" && request.ModelMode != "openai" {
		return PreparedScan{}, fmt.Errorf("choose offline sample or configured model")
	}
	source, path, err := sources.Lookup(request.SourceSlug)
	if err != nil {
		return PreparedScan{}, fmt.Errorf("source configuration is unavailable")
	}
	c := source.Config
	if c.SourceID <= 0 || validateURLScope(c.SeedURL, c.AllowedHost, c.AllowedPathPrefix) != nil {
		return PreparedScan{}, fmt.Errorf("source ID, seed URL, or scope boundary is invalid")
	}
	if request.ModelMode == "fixture" && c.FixtureDirectory == "" {
		return PreparedScan{}, fmt.Errorf("this source has no configured offline fixtures")
	}
	if request.ModelMode == "fixture" {
		if info, err := os.Stat(c.FixtureDirectory); err != nil || !info.IsDir() {
			return PreparedScan{}, fmt.Errorf("offline fixture directory is unavailable")
		}
	}
	skill, err := LoadSourceSkill(c.SourceSkillPath, c.SkillVersion)
	if err != nil {
		return PreparedScan{}, fmt.Errorf("source instructions are unavailable; check the configured file")
	}
	if resolve == nil {
		return PreparedScan{}, fmt.Errorf("model configuration is unavailable")
	}
	model, name, err := resolve(source, request.ModelMode)
	if err != nil {
		return PreparedScan{}, err
	}
	config := CrawlConfig{ForceReextract: request.ForceReextract, ModelMode: request.ModelMode, SourceID: c.SourceID, SeedURL: c.SeedURL, AllowedHost: c.AllowedHost, AllowedPathPrefix: c.AllowedPathPrefix, Scope: c.Scope, SourceSkill: skill, MaxDepth: request.MaxDepth, MaxPages: request.MaxPages, SkillVersion: c.SkillVersion, Mode: "live", Model: name, PromptVersion: ExtractionVersion}
	var fetcher Fetcher
	if request.ModelMode == "fixture" {
		config.Mode = "snapshot"
		fetcher = &SnapshotFetcher{Directory: c.FixtureDirectory}
	} else {
		if fetch.Delay == 0 {
			fetch.Delay = 750 * time.Millisecond
		}
		if fetch.MaxPageBytes == 0 {
			fetch.MaxPageBytes = DefaultMaxPageBytes
		}
		if fetch.Delay < 0 || fetch.Retries < 0 || fetch.RetryDelay < 0 || fetch.MaxPageBytes <= 0 || fetch.MaxPageBytes == 1<<63-1 {
			return PreparedScan{}, fmt.Errorf("fetch settings are invalid")
		}
		fetcher = &HTTPFetcher{UserAgent: "SemanticCrawler/1.0", Delay: fetch.Delay, MaxRetries: fetch.Retries, RetryDelay: fetch.RetryDelay, MaxResponseBytes: fetch.MaxPageBytes, AllowedHost: c.AllowedHost, AllowedPathPrefix: c.AllowedPathPrefix}
	}
	encoded, err := json.Marshal(scanConfiguration{CrawlConfig: config, SourceSlug: source.Slug, ModelMode: request.ModelMode, Fetch: fetch})
	if err != nil {
		return PreparedScan{}, err
	}
	encoded = append(encoded, []byte(modelIdentity(model))...)
	digest := sha256.Sum256(encoded)
	return PreparedScan{Source: source, DBPath: path, Request: request, Config: config, Identity: hex.EncodeToString(digest[:]), Fetch: fetch, Fetcher: fetcher, Model: model, OpenWorkspace: func(ctx context.Context, create bool) (*SourceWorkspace, error) {
		return sources.Resolve(ctx, source.Slug, create)
	}}, nil
}

type workspaceRetry struct {
	workspace  *SourceWorkspace
	resolve    ModelResolver
	legacyMode string
}

func NewWorkspaceRetrier(workspace *SourceWorkspace, resolve ModelResolver, legacyMode string) ReviewRetrier {
	return &workspaceRetry{workspace: workspace, resolve: resolve, legacyMode: legacyMode}
}
func (retry *workspaceRetry) Retry(ctx context.Context, id int64) error {
	candidate, err := retry.workspace.Store.Candidate(ctx, id)
	if err != nil {
		return err
	}
	if candidate.SourceID != retry.workspace.Source.Config.SourceID {
		return fmt.Errorf("candidate does not belong to this source")
	}
	page, err := retry.workspace.Store.PageVersionByID(ctx, candidate.PageVersionID)
	if err != nil {
		return err
	}
	mode := retry.legacyMode
	config := CrawlConfig{Scope: retry.workspace.Source.Config.Scope, Model: candidate.Model, PromptVersion: candidate.PromptVersion}
	if page.RunID != 0 {
		run, err := retry.workspace.Store.ScanRun(ctx, page.RunID)
		if err != nil {
			return fmt.Errorf("candidate scan metadata is unavailable")
		}
		config = run.Config
		if run.ModelMode != "" {
			mode = run.ModelMode
		} else if candidate.Model == "fixture" || candidate.Model == "deterministic-fixture-model" {
			mode = "fixture"
		} else {
			mode = "openai"
		}
	} else {
		config.SourceSkill, err = LoadSourceSkill(retry.workspace.Source.Config.SourceSkillPath, retry.workspace.Source.Config.SkillVersion)
		if err != nil {
			return fmt.Errorf("source instructions are unavailable")
		}
	}
	model, name, err := retry.resolve(retry.workspace.Source, mode)
	if err != nil {
		return err
	}
	if candidate.Model != "" && name != candidate.Model {
		return fmt.Errorf("configured model differs from the candidate's recorded model")
	}
	config.PromptVersion = ExtractionVersion
	return NewRetryProcessor(retry.workspace.Store, model, config).Retry(ctx, id)
}
