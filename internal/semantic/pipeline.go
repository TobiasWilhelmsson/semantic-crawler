package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type FetchResult struct {
	URL        string
	StatusCode int
	HTML       string
}

type Fetcher interface {
	Fetch(ctx context.Context, url string) (FetchResult, error)
}

type HTTPFetcher struct {
	Client            *http.Client
	UserAgent         string
	Delay             time.Duration
	MaxRetries        int
	RetryDelay        time.Duration
	AllowedHost       string
	AllowedPathPrefix string
	MaxResponseBytes  int64
}

func (fetcher *HTTPFetcher) Fetch(ctx context.Context, url string) (FetchResult, error) {
	if err := validateURLScope(url, fetcher.AllowedHost, fetcher.AllowedPathPrefix); err != nil {
		return FetchResult{}, err
	}
	client := fetcher.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	guarded := *client
	originalRedirectCheck := client.CheckRedirect
	guarded.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := validateURLScope(request.URL.String(), fetcher.AllowedHost, fetcher.AllowedPathPrefix); err != nil {
			return err
		}
		if originalRedirectCheck != nil {
			return originalRedirectCheck(request, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many source redirects")
		}
		return nil
	}
	client = &guarded
	limit := fetcher.MaxResponseBytes
	if limit == 0 {
		limit = DefaultMaxPageBytes
	}
	attempts := fetcher.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var last FetchResult
	for attempt := 1; attempt <= attempts; attempt++ {
		if fetcher.Delay > 0 {
			if err := sleepContext(ctx, fetcher.Delay); err != nil {
				return FetchResult{}, err
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return FetchResult{}, fmt.Errorf("create fetch request: %w", err)
		}
		if fetcher.UserAgent != "" {
			request.Header.Set("User-Agent", fetcher.UserAgent)
		}
		response, err := client.Do(request)
		if err != nil {
			return FetchResult{}, fmt.Errorf("fetch %s: %w", url, err)
		}
		body, readErr := readBoundedResponse(response.Body, limit)
		closeErr := response.Body.Close()
		if readErr != nil {
			return FetchResult{}, fmt.Errorf("read %s: %w", url, readErr)
		}
		if closeErr != nil {
			return FetchResult{}, fmt.Errorf("close %s: %w", url, closeErr)
		}
		last = FetchResult{URL: response.Request.URL.String(), StatusCode: response.StatusCode, HTML: string(body)}
		if !retryableHTTPStatus(response.StatusCode) || attempt == attempts {
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return last, fmt.Errorf("source returned HTTP %d", response.StatusCode)
			}
			return last, nil
		}
		delay := fetcher.retryDelay(response)
		if delay > 0 {
			if err := sleepContext(ctx, delay); err != nil {
				return FetchResult{}, err
			}
		}
	}
	return last, nil
}

func retryableHTTPStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func (fetcher *HTTPFetcher) retryDelay(response *http.Response) time.Duration {
	if value := response.Header.Get("Retry-After"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	if fetcher.RetryDelay > 0 {
		return fetcher.RetryDelay
	}
	return time.Second
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type SemanticModel interface {
	Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error)
	Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error)
}

type PageVersion struct {
	ExtractionIdentity string
	ID                 int64
	SourceID           int64
	RunID              int64
	URL                string
	Title              string
	StatusCode         int
	RawHTML            string
	SnapshotHTML       string
	CoreText           string
	ContentHash        string
	PreviousHash       string
	CheckedAt          time.Time
	ChangedAt          time.Time
	FetchMode          string
	Processed          bool
}

type QueueStatus string

const (
	QueuePending    QueueStatus = "pending"
	QueueProcessing QueueStatus = "processing"
	QueueCompleted  QueueStatus = "completed"
	QueueFailed     QueueStatus = "failed"
)

type QueueItem struct {
	RunID     int64
	URL       string
	ParentURL string
	Depth     int
	Status    QueueStatus
}

type CrawlTracker interface {
	StartCrawlRun(ctx context.Context, config CrawlConfig) (int64, error)
	UpsertQueueItem(ctx context.Context, item QueueItem) error
	UpdateQueueItem(ctx context.Context, runID int64, url string, status QueueStatus, action RouteAction, lastError string) error
	FinishCrawlRun(ctx context.Context, runID int64, summary CrawlSummary, runError string) error
	TouchPageCheckedAt(ctx context.Context, pageID int64, checkedAt time.Time) error
}

type CrawlStore interface {
	ReviewRepository
	LatestPageVersion(ctx context.Context, sourceID int64, url string) (PageVersion, bool, error)
	SavePageVersion(ctx context.Context, page PageVersion) (PageVersion, error)
	MarkPageProcessed(ctx context.Context, pageID int64) error
}

type CrawlConfig struct {
	ForceReextract    bool   `json:"force_reextract,omitempty"`
	ModelMode         string `json:"model_mode,omitempty"`
	SourceID          int64
	SeedURL           string
	AllowedHost       string
	AllowedPathPrefix string
	Scope             string
	SourceSkill       SourceSkill
	MaxDepth          int
	MaxPages          int
	SkillVersion      int
	Mode              string
	Model             string
	PromptVersion     string
}

type CrawlSummary struct {
	Discovered    int
	Unchanged     int
	PendingReview int
	Ignored       int
	NeedsReview   int
	Failed        int
}

type Pipeline struct {
	fetcher Fetcher
	model   SemanticModel
	store   CrawlStore
}

func NewPipeline(fetcher Fetcher, model SemanticModel, store CrawlStore) *Pipeline {
	return &Pipeline{fetcher: fetcher, model: model, store: store}
}

type crawlItem struct {
	URL       string
	ParentURL string
	Depth     int
}

func (pipeline *Pipeline) Run(ctx context.Context, config CrawlConfig) (summary CrawlSummary, err error) {
	if config.AllowedHost == "" {
		if parsed, parseErr := url.Parse(config.SeedURL); parseErr == nil {
			config.AllowedHost = parsed.Hostname()
		}
	}
	if err := validateURLScope(config.SeedURL, config.AllowedHost, config.AllowedPathPrefix); err != nil {
		return summary, err
	}
	fetcher := pipeline.fetcher
	if httpFetcher, ok := fetcher.(*HTTPFetcher); ok {
		scoped := *httpFetcher
		scoped.AllowedHost = config.AllowedHost
		scoped.AllowedPathPrefix = config.AllowedPathPrefix
		fetcher = &scoped
	}
	if config.MaxPages <= 0 {
		config.MaxPages = 25
	}
	var runID int64
	tracker, tracked := pipeline.store.(CrawlTracker)
	var activeURL string
	var activeAction RouteAction
	var outcome string
	updateQueue := func(url string, status QueueStatus, action RouteAction, lastError string) error {
		if tracked {
			if outcomes, ok := pipeline.store.(QueueOutcomeTracker); ok && (status == QueueCompleted || status == QueueFailed) {
				value := outcome
				if status == QueueFailed {
					value = "failed"
				} else if value == "" {
					switch action {
					case RouteExtract:
						value = "candidate"
					case RouteNeedsReview:
						value = "needs_review"
					default:
						value = "ignored"
					}
				}
				if err := outcomes.RecordQueueOutcome(ctx, runID, url, value); err != nil {
					return err
				}
			}
			if err := tracker.UpdateQueueItem(ctx, runID, url, status, action, lastError); err != nil {
				return err
			}
		}
		if status == QueueCompleted || status == QueueFailed {
			activeURL = ""
		}
		return nil
	}
	if tracked {
		runID, err = tracker.StartCrawlRun(ctx, config)
		if err != nil {
			return CrawlSummary{}, err
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			runError := ""
			if err != nil {
				if activeURL != "" {
					err = errors.Join(err, tracker.UpdateQueueItem(cleanupCtx, runID, activeURL, QueueFailed, activeAction, err.Error()))
				}
				runError = err.Error()
			}
			err = errors.Join(err, tracker.FinishCrawlRun(cleanupCtx, runID, summary, runError))
		}()
		if err := tracker.UpsertQueueItem(ctx, QueueItem{RunID: runID, URL: config.SeedURL, Status: QueuePending}); err != nil {
			return CrawlSummary{}, err
		}
	}
	queue := []crawlItem{{URL: config.SeedURL}}
	seen := map[string]struct{}{config.SeedURL: {}}
	if canonical, canonicalErr := CanonicalizeURL(config.SeedURL, config.SeedURL, config.AllowedHost); canonicalErr == nil {
		seen[canonical] = struct{}{}
	}
	summary = CrawlSummary{Discovered: 1}

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		item := queue[0]
		queue = queue[1:]
		requestedURL := item.URL
		activeURL, activeAction = requestedURL, ""
		outcome = ""
		if err := updateQueue(requestedURL, QueueProcessing, "", ""); err != nil {
			return summary, err
		}
		result, err := fetcher.Fetch(ctx, item.URL)
		if ctxErr := ctx.Err(); ctxErr != nil {
			summary.Failed++
			return summary, ctxErr
		}
		if err == nil && (result.StatusCode < 200 || result.StatusCode >= 300) {
			err = fmt.Errorf("source returned HTTP %d", result.StatusCode)
		}
		if err == nil && result.URL != "" {
			err = validateURLScope(result.URL, config.AllowedHost, config.AllowedPathPrefix)
		}
		if err != nil {
			summary.Failed++
			if err := updateQueue(requestedURL, QueueFailed, "", err.Error()); err != nil {
				return summary, err
			}
			continue
		}
		if result.URL != "" {
			item.URL = result.URL
		}
		seen[item.URL] = struct{}{}
		if canonical, canonicalErr := CanonicalizeURL(item.URL, item.URL, config.AllowedHost); canonicalErr == nil {
			seen[canonical] = struct{}{}
		}

		links, err := harvestLinks(result.HTML, item.URL, config.AllowedHost)
		if err != nil {
			summary.Failed++
			if err := updateQueue(requestedURL, QueueFailed, "", err.Error()); err != nil {
				return summary, err
			}
			continue
		}
		if item.Depth < config.MaxDepth {
			for _, link := range links {
				if err := validateURLScope(link, config.AllowedHost, config.AllowedPathPrefix); err != nil {
					continue
				}
				if summary.Discovered >= config.MaxPages {
					break
				}
				if _, exists := seen[link]; exists {
					continue
				}
				seen[link] = struct{}{}
				summary.Discovered++
				queue = append(queue, crawlItem{URL: link, ParentURL: item.URL, Depth: item.Depth + 1})
				if tracked {
					if err := tracker.UpsertQueueItem(ctx, QueueItem{RunID: runID, URL: link, ParentURL: item.URL, Depth: item.Depth + 1, Status: QueuePending}); err != nil {
						return summary, err
					}
				}
			}
		}

		blocks, snapshot, err := BuildCandidateBlocks(strings.NewReader(result.HTML), item.URL)
		if err != nil {
			summary.Failed++
			if err := updateQueue(requestedURL, QueueFailed, "", err.Error()); err != nil {
				return summary, err
			}
			continue
		}
		rules, err := pipeline.store.Rules(ctx, config.SourceID, item.URL)
		if err != nil {
			return summary, err
		}
		identity := extractionIdentity(config, rules, pipeline.model)
		hash := ContentHash(blocks)
		previous, exists, err := pipeline.store.LatestPageVersion(ctx, config.SourceID, item.URL)
		if err != nil {
			return summary, err
		}
		if !config.ForceReextract && exists && previous.Processed && previous.ContentHash == hash && previous.ExtractionIdentity == identity {
			summary.Unchanged++
			outcome = "unchanged"
			if tracked {
				if err := tracker.TouchPageCheckedAt(ctx, previous.ID, time.Now().UTC()); err != nil {
					return summary, err
				}
			}
			if err := updateQueue(requestedURL, QueueCompleted, RouteNavigateOnly, ""); err != nil {
				return summary, err
			}
			continue
		}
		now := time.Now().UTC()
		page := PageVersion{ExtractionIdentity: identity, SourceID: config.SourceID, RunID: runID, URL: item.URL, Title: pageTitle(result.HTML), StatusCode: result.StatusCode, RawHTML: result.HTML, SnapshotHTML: snapshot, CoreText: blocksText(blocks, 0), ContentHash: hash, CheckedAt: now, ChangedAt: now, FetchMode: config.Mode}
		if exists {
			page.PreviousHash = previous.ContentHash
		}
		page, err = pipeline.store.SavePageVersion(ctx, page)
		if err != nil {
			return summary, err
		}
		appliedRules, _ := json.Marshal(rules)
		route, routeUsage, err := pipeline.model.Route(ctx, buildRoutePrompt(config, page, blocks, rules))
		if ctxErr := ctx.Err(); ctxErr != nil {
			summary.Failed++
			return summary, ctxErr
		}
		if err != nil {
			summary.Failed++
			if err := updateQueue(requestedURL, QueueFailed, "", err.Error()); err != nil {
				return summary, err
			}
			continue
		}
		activeAction = route.Action
		routeJSON, _ := json.Marshal(route)
		switch route.Action {
		case RouteIgnore, RouteNavigateOnly:
			if err := pipeline.store.MarkPageProcessed(ctx, page.ID); err != nil {
				return summary, err
			}
			summary.Ignored++
			if err := updateQueue(requestedURL, QueueCompleted, route.Action, ""); err != nil {
				return summary, err
			}
			continue
		case RouteNeedsReview:
			err = pipeline.saveProcessedCandidate(ctx, Candidate{ExtractionIdentity: identity, SourceID: config.SourceID, PageVersionID: page.ID, URL: item.URL, Status: CandidatePendingReview, RouteResponse: routeJSON, PromptVersion: config.PromptVersion, SkillVersion: config.SkillVersion, SourceSkillHash: config.SourceSkill.Hash, SourceSkillVersion: config.SourceSkill.Version, AppliedRules: appliedRules, Model: config.Model, InputTokens: routeUsage.InputTokens, OutputTokens: routeUsage.OutputTokens, LatencyMS: routeUsage.Latency.Milliseconds(), RawResponse: routeUsage.RawResponse})
			if err != nil {
				return summary, err
			}
			summary.NeedsReview++
			if err := updateQueue(requestedURL, QueueCompleted, route.Action, ""); err != nil {
				return summary, err
			}
			continue
		}

		extraction, extractionUsage, err := pipeline.model.Extract(ctx, buildExtractionPrompt(config, page, blocks, rules), blocks)
		if ctxErr := ctx.Err(); ctxErr != nil {
			summary.Failed++
			return summary, ctxErr
		}
		if err != nil {
			summary.Failed++
			if err := updateQueue(requestedURL, QueueFailed, route.Action, err.Error()); err != nil {
				return summary, err
			}
			continue
		}
		if err := ValidateExtraction(extraction, blocks); err != nil {
			summary.Failed++
			if err := updateQueue(requestedURL, QueueFailed, route.Action, err.Error()); err != nil {
				return summary, err
			}
			continue
		}
		sections := sectionsFromDecisions(blocks, extraction.Blocks)
		markdown, err := markdownFromSections(sections)
		if err != nil {
			return summary, err
		}
		blockJSON, _ := json.Marshal(extraction)
		err = pipeline.saveProcessedCandidate(ctx, Candidate{ExtractionIdentity: identity, SourceID: config.SourceID, PageVersionID: page.ID, URL: item.URL, Status: CandidatePendingReview, Sections: sections, Markdown: markdown, RouteResponse: routeJSON, BlockResponse: blockJSON, PromptVersion: config.PromptVersion, SkillVersion: config.SkillVersion, SourceSkillHash: config.SourceSkill.Hash, SourceSkillVersion: config.SourceSkill.Version, AppliedRules: appliedRules, Model: config.Model, InputTokens: routeUsage.InputTokens + extractionUsage.InputTokens, OutputTokens: routeUsage.OutputTokens + extractionUsage.OutputTokens, LatencyMS: (routeUsage.Latency + extractionUsage.Latency).Milliseconds(), RawResponse: routeUsage.RawResponse + "\n" + extractionUsage.RawResponse})
		if err != nil {
			return summary, err
		}
		summary.PendingReview++
		if err := updateQueue(requestedURL, QueueCompleted, route.Action, ""); err != nil {
			return summary, err
		}
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	return summary, nil
}

func (pipeline *Pipeline) saveProcessedCandidate(ctx context.Context, candidate Candidate) error {
	transactional, ok := pipeline.store.(ReviewTransactor)
	if !ok {
		return fmt.Errorf("crawl repository does not support transactions")
	}
	return transactional.WithReviewTransaction(ctx, func(repository ReviewRepository) error {
		store, ok := repository.(CrawlStore)
		if !ok {
			return fmt.Errorf("transactional repository does not support crawl completion")
		}
		if _, err := store.SaveCandidate(ctx, candidate); err != nil {
			return err
		}
		return store.MarkPageProcessed(ctx, candidate.PageVersionID)
	})
}

func harvestLinks(html, pageURL, allowedHost string) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse links: %w", err)
	}
	seen := make(map[string]struct{})
	var links []string
	doc.Find("a[href]").Each(func(_ int, selection *goquery.Selection) {
		href, _ := selection.Attr("href")
		canonical, canonicalErr := CanonicalizeURL(pageURL, href, allowedHost)
		if canonicalErr != nil {
			return
		}
		if _, exists := seen[canonical]; exists {
			return
		}
		seen[canonical] = struct{}{}
		links = append(links, canonical)
	})
	return links, nil
}

func pageTitle(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}
	return normalizeText(doc.Find("title").First().Text())
}

func buildRoutePrompt(config CrawlConfig, page PageVersion, blocks []CandidateBlock, rules []SkillRule) string {
	return fmt.Sprintf("Scope: %s\nURL: %s\nTitle: %s\nSource skill guide:\n%s\nReviewer rules:\n%s\nText sample: %s", config.Scope, page.URL, page.Title, config.SourceSkill.Content, rulesText(rules), blocksText(blocks, 2000))
}

func buildExtractionPrompt(config CrawlConfig, page PageVersion, blocks []CandidateBlock, rules []SkillRule) string {
	return fmt.Sprintf("Scope: %s\nURL: %s\nSource skill guide:\n%s\nReviewer rules:\n%s\nReviewer rules are mandatory. If a reviewer rule names exact block_id values, include those block_id values with include=true unless the exact same rule explicitly says to drop them.\nSelect relevant numbered source blocks without rewriting them. Do not invent, summarize, or silently correct source claims; select source blocks by block_id. Preserve wording, qualifications, exceptions, dates, units, table headers and relationships, and substantive context within scope. Selection fidelity does not verify the truth of source claims.\nUse type=info for informational boxes, type=critical for urgent safety or risk notices, and type=none for ordinary content, navigation, metadata, media, and video.\nIf the source skill asks for metadata, breadcrumbs, or media, include those blocks when present.\nSource blocks:\n%s", config.Scope, page.URL, config.SourceSkill.Content, rulesText(rules), blocksText(blocks, 0))
}

func rulesText(rules []SkillRule) string {
	parts := make([]string, 0, len(rules))
	for _, rule := range rules {
		parts = append(parts, fmt.Sprintf("v%d %s: %s", rule.Version, rule.Scope, rule.Instruction))
	}
	return strings.Join(parts, "\n")
}

func blocksText(blocks []CandidateBlock, limit int) string {
	var builder strings.Builder
	for _, block := range blocks {
		role, blockType := structuralBlockHints(block)
		fmt.Fprintf(&builder, "%s <%s> role_hint=%s type_hint=%s text=%s\n", block.ID, block.Tag, role, blockType, block.Text)
		if limit > 0 && builder.Len() >= limit {
			break
		}
	}
	return builder.String()
}

func sectionsFromDecisions(blocks []CandidateBlock, decisions []BlockDecision) []Section {
	byID := make(map[string]CandidateBlock, len(blocks))
	for _, block := range blocks {
		byID[block.ID] = block
	}
	var sections []Section
	for _, decision := range decisions {
		if !decision.Include {
			continue
		}
		block := byID[decision.BlockID]
		sections = append(sections, Section{BlockID: block.ID, Name: block.ID, Role: decision.Role, Type: decision.Type, ParseMode: decision.ParseMode, Content: block.HTML, Locator: block.Locator})
	}
	return sections
}

func markdownFromSections(sections []Section) (string, error) {
	return formatMarkdownSections(sections)
}

func (store *MemoryStore) PutPageVersion(page PageVersion) PageVersion {
	stored, _ := store.SavePageVersion(context.Background(), page)
	return stored
}

func (store *MemoryStore) LatestPageVersion(_ context.Context, sourceID int64, url string) (PageVersion, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := len(store.pages) - 1; index >= 0; index-- {
		if store.pages[index].SourceID == sourceID && store.pages[index].URL == url {
			return store.pages[index], true, nil
		}
	}
	return PageVersion{}, false, nil
}

func (store *MemoryStore) SavePageVersion(_ context.Context, page PageVersion) (PageVersion, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if page.ID == 0 {
		page.ID = int64(len(store.pages) + 1)
	}
	store.pages = append(store.pages, page)
	return page, nil
}

func (store *MemoryStore) MarkPageProcessed(_ context.Context, pageID int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.pages {
		if store.pages[index].ID == pageID {
			store.pages[index].Processed = true
			return nil
		}
	}
	return fmt.Errorf("page version %d not found", pageID)
}
