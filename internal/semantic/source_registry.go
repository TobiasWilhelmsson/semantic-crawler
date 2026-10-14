package semantic

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const defaultSourceConfigRoot = "configs/sources"

var sourceSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type SourceConfig struct {
	SourceID          int64  `json:"source_id"`
	Name              string `json:"name"`
	SeedURL           string `json:"seed_url"`
	AllowedHost       string `json:"allowed_host"`
	AllowedPathPrefix string `json:"allowed_path_prefix"`
	Scope             string `json:"scope"`
	MaxDepth          int    `json:"max_depth"`
	MaxPages          int    `json:"max_pages"`
	SkillVersion      int    `json:"skill_version"`
	SourceSkillPath   string `json:"source_skill_path"`
	GoldPath          string `json:"gold_path,omitempty"`
	FixtureDirectory  string `json:"fixture_directory,omitempty"`
}

type SourceEntry struct {
	Slug       string
	ConfigPath string
	Config     SourceConfig
}

type SourceStatus struct {
	Slug   string
	Source *SourceEntry
	Error  string
}

func (registry SourceRegistry) ListStatus() ([]SourceStatus, error) {
	paths, err := filepath.Glob(filepath.Join(registry.root, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	result := make([]SourceStatus, 0, len(paths))
	for _, path := range paths {
		slug := strings.TrimSuffix(filepath.Base(path), ".json")
		status := SourceStatus{Slug: slug}
		entry, err := registry.Get(slug)
		if err != nil || entry.Config.SourceID <= 0 {
			status.Error = "Source configuration is invalid; check its JSON file and positive source_id."
		} else {
			status.Source = &entry
		}
		result = append(result, status)
	}
	return result, nil
}

type SourceWizardInput struct {
	Slug              string
	Name              string
	SeedURL           string
	AllowedHost       string
	AllowedPathPrefix string
	Scope             string
	MaxDepth          int
	MaxPages          int
	SkillMarkdown     string
	Overwrite         bool
}

type SourceRegistry struct {
	root string
}

func NewSourceRegistry(root string) SourceRegistry {
	if root == "" {
		root = defaultSourceConfigRoot
	}
	return SourceRegistry{root: root}
}

func (registry SourceRegistry) List() ([]SourceEntry, error) {
	matches, err := filepath.Glob(filepath.Join(registry.root, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	entries := make([]SourceEntry, 0, len(matches))
	for _, path := range matches {
		slug := strings.TrimSuffix(filepath.Base(path), ".json")
		config, err := LoadSourceConfigFile(path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, SourceEntry{Slug: slug, ConfigPath: path, Config: config})
	}
	return entries, nil
}

func (registry SourceRegistry) Get(slug string) (SourceEntry, error) {
	if err := validateSourceSlug(slug); err != nil {
		return SourceEntry{}, err
	}
	path := filepath.Join(registry.root, slug+".json")
	config, err := LoadSourceConfigFile(path)
	if err != nil {
		return SourceEntry{}, err
	}
	return SourceEntry{Slug: slug, ConfigPath: path, Config: config}, nil
}

func (registry SourceRegistry) Create(input SourceWizardInput) (SourceEntry, error) {
	if err := validateSourceSlug(input.Slug); err != nil {
		return SourceEntry{}, err
	}
	parsed, err := url.Parse(input.SeedURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return SourceEntry{}, fmt.Errorf("seed URL must be an absolute http(s) URL")
	}
	if strings.TrimSpace(input.Name) == "" {
		return SourceEntry{}, fmt.Errorf("source name is required")
	}
	if strings.TrimSpace(input.Scope) == "" {
		return SourceEntry{}, fmt.Errorf("source scope is required")
	}
	if strings.TrimSpace(input.SkillMarkdown) == "" {
		return SourceEntry{}, fmt.Errorf("source skill markdown is required")
	}
	configPath := filepath.Join(registry.root, input.Slug+".json")
	var existing *SourceConfig
	if _, err := os.Stat(configPath); err == nil {
		if !input.Overwrite {
			return SourceEntry{}, fmt.Errorf("source %q already exists", input.Slug)
		}
		config, err := LoadSourceConfigFile(configPath)
		if err != nil {
			return SourceEntry{}, err
		}
		existing = &config
	} else if !os.IsNotExist(err) {
		return SourceEntry{}, fmt.Errorf("inspect source config: %w", err)
	}
	host := strings.TrimSpace(input.AllowedHost)
	if host == "" {
		host = parsed.Hostname()
	}
	prefix := normalizePathPrefix(input.AllowedPathPrefix)
	if prefix == "" {
		prefix = normalizePathPrefix(parsed.Path)
	}
	if err := validateURLScope(parsed.String(), host, prefix); err != nil {
		return SourceEntry{}, err
	}
	maxDepth := input.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	maxPages := input.MaxPages
	if maxPages <= 0 {
		maxPages = 20
	}
	skillPath := filepath.Join(registry.root, "source-skills", input.Slug+".md")
	var sourceID int64
	if existing != nil {
		sourceID = existing.SourceID
	} else {
		sourceID, err = registry.nextSourceID()
		if err != nil {
			return SourceEntry{}, err
		}
	}
	config := SourceConfig{
		SourceID:          sourceID,
		Name:              strings.TrimSpace(input.Name),
		SeedURL:           parsed.String(),
		AllowedHost:       host,
		AllowedPathPrefix: prefix,
		Scope:             strings.TrimSpace(input.Scope),
		MaxDepth:          maxDepth,
		MaxPages:          maxPages,
		SkillVersion:      1,
		SourceSkillPath:   skillPath,
	}
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		return SourceEntry{}, fmt.Errorf("create source skill directory: %w", err)
	}
	if err := os.MkdirAll(registry.root, 0o755); err != nil {
		return SourceEntry{}, fmt.Errorf("create source config directory: %w", err)
	}
	if err := os.WriteFile(skillPath, []byte(input.SkillMarkdown), 0o644); err != nil {
		return SourceEntry{}, fmt.Errorf("write source skill: %w", err)
	}
	data, err := MarshalSourceConfig(config)
	if err != nil {
		return SourceEntry{}, err
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return SourceEntry{}, fmt.Errorf("write source config: %w", err)
	}
	return SourceEntry{Slug: input.Slug, ConfigPath: configPath, Config: config}, nil
}

func (registry SourceRegistry) nextSourceID() (int64, error) {
	sources, err := registry.List()
	if err != nil {
		return 0, err
	}
	var maxID int64
	for _, source := range sources {
		if source.Config.SourceID > maxID {
			maxID = source.Config.SourceID
		}
	}
	return maxID + 1, nil
}

func LoadSourceConfigFile(path string) (SourceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourceConfig{}, fmt.Errorf("read source config %s: %w", path, err)
	}
	var config SourceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return SourceConfig{}, fmt.Errorf("decode source config %s: %w", path, err)
	}
	return config, nil
}

func MarshalSourceConfig(config SourceConfig) ([]byte, error) {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DefaultSemanticDBPath(slug string) string {
	if slug == "" {
		slug = "semantic-crawler"
	}
	return filepath.Join("data", "semantic-"+slug+".db")
}

func DefaultSemanticReportDir(slug string) string {
	if slug == "" {
		return filepath.Join("artifacts", "semantic-crawler")
	}
	return filepath.Join("artifacts", "semantic-crawler", slug)
}

func validateSourceSlug(slug string) error {
	if !sourceSlugPattern.MatchString(slug) {
		return fmt.Errorf("source slug must use lowercase letters, numbers, and hyphens")
	}
	return nil
}

func normalizePathPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if !strings.HasSuffix(value, "/") {
		value += "/"
	}
	return value
}
