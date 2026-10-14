package semantic

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gin-gonic/gin"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"semantic-crawler/internal/models"
)

type ReviewWebStore interface {
	ReviewRepository
	PageVersionByID(ctx context.Context, id int64) (PageVersion, error)
	CandidatesByStatus(ctx context.Context, statuses ...CandidateStatus) ([]Candidate, error)
	LatestApprovedCandidate(ctx context.Context, sourceID int64, url string, excludeID int64) (Candidate, bool, error)
	AllRules(ctx context.Context) ([]SkillRule, error)
}

var reviewModeOnce sync.Once

type ReviewRouterOptions struct {
	FetchSettings    FetchSettings
	Workspaces       *SourceWorkspaces
	Scans            *ScanManager
	Prepare          func(context.Context, ScanRequest) (PreparedScan, error)
	BasePath         string
	SourceID         int64
	SourceSlug       string
	SourceName       string
	CSRFToken        string
	ReportRoot       string
	SourceConfigRoot string
	// AllowedHosts replaces the default loopback hostnames when nonempty.
	AllowedHosts []string
}

func NewReviewRouter(store ReviewWebStore, reviews *ReviewService, deliveries *DeliveryService, retrier ...ReviewRetrier) http.Handler {
	return NewReviewRouterWithOptions(store, reviews, deliveries, ReviewRouterOptions{}, retrier...)
}

func NewReviewRouterWithOptions(store ReviewWebStore, reviews *ReviewService, deliveries *DeliveryService, options ReviewRouterOptions, retrier ...ReviewRetrier) http.Handler {
	reviewModeOnce.Do(func() { gin.SetMode(gin.ReleaseMode) })
	routeBase := options.BasePath
	if routeBase == "" {
		routeBase = "/semantic"
	}
	if options.CSRFToken == "" {
		options.CSRFToken = rand.Text()
	}
	if options.ReportRoot == "" {
		options.ReportRoot = filepath.Join("artifacts", "semantic-crawler")
	}
	if options.SourceConfigRoot == "" {
		options.SourceConfigRoot = defaultSourceConfigRoot
	}
	sourceRegistry := NewSourceRegistry(options.SourceConfigRoot)
	router := gin.New()
	hostnames := options.AllowedHosts
	if len(hostnames) == 0 {
		hostnames = []string{"localhost", "127.0.0.1", "::1"}
	}
	allowedHosts := make(map[string]struct{}, len(hostnames))
	for _, hostname := range hostnames {
		if normalized := reviewHostname(strings.TrimSpace(hostname)); normalized != "" {
			allowedHosts[normalized] = struct{}{}
		}
	}
	var requireCSRF func(*gin.Context) bool
	router.Use(func(c *gin.Context) {
		c.Header("Content-Security-Policy", "frame-ancestors 'none'")
		c.Header("X-Frame-Options", "DENY")
		if _, allowed := allowedHosts[reviewHostname(c.Request.Host)]; !allowed {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		setReviewContext(c, options, routeBase)
		if c.Request.Method == http.MethodPost && !requireCSRF(c) {
			return
		}
		if !checkReviewSource(c, store, options.SourceID, routeBase) {
			return
		}
		c.Next()
	})
	requireCSRF = func(c *gin.Context) bool {
		if message := reviewCSRFError(c.Request, options.CSRFToken); message != "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": message})
			return false
		}
		return true
	}
	router.GET(routeBase+"/review", func(c *gin.Context) {
		candidates, err := store.CandidatesByStatus(c.Request.Context(), CandidatePendingReview, CandidateEscalated, CandidateQueued)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		if options.Workspaces != nil && c.Query("run_id") != "" {
			id, err := strconv.ParseInt(c.Query("run_id"), 10, 64)
			if err != nil || id <= 0 {
				c.Status(404)
				return
			}
			gormStore, ok := store.(*GormStore)
			if !ok {
				c.Status(404)
				return
			}
			run, err := gormStore.ScanRun(c.Request.Context(), id)
			if err != nil || run.SourceID != options.SourceID {
				c.Status(404)
				return
			}
			candidates, err = gormStore.CandidatesForRun(c.Request.Context(), id)
			if err != nil {
				c.String(500, "Candidates are unavailable.")
				return
			}
		}
		hasScans := true
		if options.Workspaces != nil {
			if concrete, ok := store.(*GormStore); ok {
				runs, err := concrete.ScanRuns(c.Request.Context(), options.SourceID, 1)
				hasScans = err == nil && len(runs) > 0
			}
		}
		writePage(c, "Review Queue", "review", queueTemplate, map[string]any{"Candidates": filterSourceCandidates(candidates, options.SourceID), "CSRFToken": options.CSRFToken, "HasScans": hasScans})
	})
	router.GET("/semantic", func(c *gin.Context) {
		candidates, err := store.CandidatesByStatus(c.Request.Context())
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		rules, err := store.AllRules(c.Request.Context())
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		writePage(c, "Dashboard", "dashboard", dashboardTemplate, map[string]any{"Stats": dashboardStats(filterSourceCandidates(candidates, options.SourceID), filterSourceRules(rules, options.SourceID)), "ScansAvailable": options.Scans != nil})
	})
	router.GET(routeBase+"/escalations", func(c *gin.Context) {
		candidates, err := store.CandidatesByStatus(c.Request.Context(), CandidateEscalated)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		writePage(c, "Escalations", "escalations", escalationsTemplate, map[string]any{"Candidates": filterSourceCandidates(candidates, options.SourceID)})
	})
	router.GET(routeBase+"/rules", func(c *gin.Context) {
		rules, err := store.AllRules(c.Request.Context())
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		writePage(c, "Rules And Prompts", "rules", rulesTemplate, map[string]any{"Rules": filterSourceRules(rules, options.SourceID), "CSRFToken": options.CSRFToken})
	})
	router.GET("/semantic/guides", func(c *gin.Context) {
		writePage(c, "Guides", "guides", guidesTemplate, map[string]any{"Guides": guideDocs()})
	})
	router.GET("/semantic/guides/:name", func(c *gin.Context) {
		guide, ok := guideDoc(c.Param("name"))
		if !ok {
			c.String(http.StatusNotFound, "guide not found")
			return
		}
		content, err := readKnownFile(guide.Path)
		if err != nil {
			c.String(http.StatusNotFound, "guide not found: %v", err)
			return
		}
		writePage(c, guide.Title, "guides", guideDetailTemplate, map[string]any{"Guide": guide, "ContentHTML": renderGuideMarkdown(content)})
	})
	router.GET("/semantic/sources", func(c *gin.Context) {
		if slug := c.Query("source"); slug != "" && options.Workspaces != nil {
			if _, _, err := options.Workspaces.Lookup(slug); err != nil {
				c.Status(404)
				return
			}
			c.Redirect(303, "/semantic/sources/"+slug)
			return
		}
		if options.Workspaces != nil {
			statuses, err := options.Workspaces.List()
			if err != nil {
				c.String(500, "Sources are unavailable.")
				return
			}
			writePage(c, "Sources", "sources", sourcesTemplate, map[string]any{"Statuses": statuses, "CSRFToken": options.CSRFToken})
			return
		}
		sources, err := sourceRegistry.List()
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		writePage(c, "Sources", "sources", sourcesTemplate, map[string]any{"Sources": sourceCards(sources), "CSRFToken": options.CSRFToken})
	})
	router.GET("/semantic/sources/new", func(c *gin.Context) {
		writePage(c, "Add Source", "sources", sourceNewTemplate, map[string]any{"CSRFToken": options.CSRFToken, "Form": SourceWizardInput{MaxDepth: 2, MaxPages: 20, SkillMarkdown: defaultSourceSkillTemplate()}})
	})
	router.POST("/semantic/sources/new", func(c *gin.Context) {
		if !requireCSRF(c) {
			return
		}
		maxDepth, depthErr := strconv.Atoi(c.PostForm("max_depth"))
		maxPages, pageErr := strconv.Atoi(c.PostForm("max_pages"))
		if c.PostForm("max_depth") == "" {
			maxDepth, depthErr = 2, nil
		}
		if c.PostForm("max_pages") == "" {
			maxPages, pageErr = 20, nil
		}
		input := SourceWizardInput{
			Slug:              c.PostForm("slug"),
			Name:              c.PostForm("name"),
			SeedURL:           c.PostForm("seed_url"),
			AllowedHost:       c.PostForm("allowed_host"),
			AllowedPathPrefix: c.PostForm("allowed_path_prefix"),
			Scope:             c.PostForm("scope"),
			MaxDepth:          maxDepth,
			MaxPages:          maxPages,
			SkillMarkdown:     c.PostForm("skill_markdown"),
			Overwrite:         c.PostForm("overwrite") == "true",
		}
		if depthErr != nil || pageErr != nil || maxDepth < 1 || maxDepth > 10 || maxPages < 1 || maxPages > 1000 {
			c.Status(422)
			writePage(c, "Add source", "sources", sourceNewTemplate, map[string]any{"Form": input, "Error": "Enter a depth from 1–10 and page limit from 1–1,000.", "CSRFToken": options.CSRFToken})
			return
		}
		created, err := sourceRegistry.Create(input)
		if err != nil {
			c.Status(http.StatusUnprocessableEntity)
			writePage(c, "Add source", "sources", sourceNewTemplate, map[string]any{"Form": input, "Error": err.Error(), "CSRFToken": options.CSRFToken})
			return
		}
		if options.Workspaces != nil {
			_ = options.Workspaces.Recover(c.Request.Context(), created.Slug)
		}
		c.Redirect(http.StatusSeeOther, "/semantic/sources/"+created.Slug)
	})
	router.GET("/semantic/sources/:slug", func(c *gin.Context) {
		if options.Workspaces != nil {
			source, ok := scanSource(c, options)
			if !ok {
				return
			}
			data := map[string]any{"Source": sourceCard(source), "Pending": 0, "HasWorkspace": false}
			if workspace, err := options.Workspaces.Resolve(c.Request.Context(), source.Slug, false); err == nil {
				data["HasWorkspace"] = true
				var count int64
				if err = workspace.Store.db.WithContext(c.Request.Context()).Model(&models.SemanticCandidate{}).Where("source_id = ? AND status IN ?", source.Config.SourceID, []string{"pending_review", "queued", "escalated"}).Count(&count).Error; err == nil {
					data["Pending"] = count
				}
				if runs, err := workspace.Store.ScanRuns(c.Request.Context(), source.Config.SourceID, 1); err == nil && len(runs) > 0 {
					data["LastScan"] = runs[0]
				}
			}
			card := sourceCard(source)
			if _, path, err := options.Workspaces.Lookup(source.Slug); err == nil {
				old := card.DBPath
				card.DBPath = path
				card.ModelCommand = strings.ReplaceAll(card.ModelCommand, old, path)
				card.DemoCommand = strings.ReplaceAll(card.DemoCommand, old, path)
				card.UICmd = strings.ReplaceAll(card.UICmd, old, path)
			}
			data["Source"] = card
			writePage(c, source.Config.Name, "sources", sourceDetailTemplate, data)
			return
		}
		source, err := sourceRegistry.Get(c.Param("slug"))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		writePage(c, source.Config.Name, "sources", sourceDetailTemplate, map[string]any{"Source": sourceCard(source), "CSRFToken": options.CSRFToken})
	})
	router.GET(routeBase+"/review/:id", func(c *gin.Context) {
		candidate, page, sectionsJSON, ok := loadReview(c, store)
		if !ok {
			return
		}
		rules, err := store.Rules(c.Request.Context(), candidate.SourceID, candidate.URL)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		previous, hasPrevious, err := store.LatestApprovedCandidate(c.Request.Context(), candidate.SourceID, candidate.URL, candidate.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		previousJSON, _ := json.MarshalIndent(previous.Sections, "", "  ")
		writePage(c, "Review Candidate", "review", reviewTemplate, map[string]any{"Candidate": candidate, "Page": page, "SectionsJSON": sectionsJSON, "Rules": filterSourceRules(rules, options.SourceID), "Previous": previous, "PreviousJSON": string(previousJSON), "HasPrevious": hasPrevious, "Changed": hasPrevious && string(previousJSON) != sectionsJSON, "CSRFToken": options.CSRFToken})
	})
	router.GET(routeBase+"/snapshots/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.String(http.StatusBadRequest, "invalid page ID")
			return
		}
		page, err := store.PageVersionByID(c.Request.Context(), id)
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.Header("Content-Security-Policy", "default-src 'none'; script-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; img-src https: data:; style-src 'unsafe-inline' https:; font-src https: data:; frame-ancestors 'self'")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page.SnapshotHTML))
	})
	for _, action := range []string{"approve", "reject", "escalate"} {
		action := action
		router.POST(routeBase+"/review/:id/"+action, func(c *gin.Context) {
			if !requireCSRF(c) {
				return
			}
			id, err := candidateID(c)
			if err != nil {
				return
			}
			switch action {
			case "approve":
				_, err = reviews.Approve(c.Request.Context(), id, c.PostForm("reviewer"))
			case "reject":
				err = reviews.Reject(c.Request.Context(), id)
			case "escalate":
				err = reviews.Escalate(c.Request.Context(), id)
			}
			if err != nil {
				c.String(http.StatusUnprocessableEntity, err.Error())
				return
			}
			c.Redirect(http.StatusSeeOther, routeBase+"/review")
		})
	}
	router.POST(routeBase+"/review/:id/edit", func(c *gin.Context) {
		if !requireCSRF(c) {
			return
		}
		id, err := candidateID(c)
		if err != nil {
			return
		}
		var sections []Section
		if err := json.Unmarshal([]byte(c.PostForm("sections")), &sections); err != nil {
			c.String(http.StatusBadRequest, "invalid sections JSON: %v", err)
			return
		}
		if _, err := reviews.EditAndApprove(c.Request.Context(), id, c.PostForm("reviewer"), sections, c.PostForm("comment")); err != nil {
			c.String(http.StatusUnprocessableEntity, err.Error())
			return
		}
		c.Redirect(http.StatusSeeOther, routeBase+"/review")
	})
	router.POST(routeBase+"/review/:id/remove-selected", func(c *gin.Context) {
		if !requireCSRF(c) {
			return
		}
		id, err := candidateID(c)
		if err != nil {
			return
		}
		approved, err := reviews.RemoveSelectedAndApprove(c.Request.Context(), id, c.PostForm("reviewer"), c.PostFormArray("remove_block_ids"), c.PostForm("comment"))
		if err != nil {
			c.String(http.StatusUnprocessableEntity, err.Error())
			return
		}
		c.Redirect(http.StatusSeeOther, fmt.Sprintf(routeBase+"/review/%d", approved.ID))
	})
	router.POST(routeBase+"/review/:id/retry", func(c *gin.Context) {
		if !requireCSRF(c) {
			return
		}
		id, err := candidateID(c)
		if err != nil {
			return
		}
		_, rerun, err := reviews.RetryWithInstruction(c.Request.Context(), id, c.PostForm("reviewer"), c.PostForm("instruction"), RuleScope(c.PostForm("scope")))
		if err != nil {
			c.String(http.StatusUnprocessableEntity, err.Error())
			return
		}
		if len(retrier) > 0 && retrier[0] != nil {
			if err := retrier[0].Retry(c.Request.Context(), rerun.ID); err != nil {
				c.String(http.StatusUnprocessableEntity, err.Error())
				return
			}
		}
		c.Redirect(http.StatusSeeOther, routeBase+"/review")
	})

	router.POST(routeBase+"/review/:id/resume", func(c *gin.Context) {
		if !requireCSRF(c) {
			return
		}
		id, err := candidateID(c)
		if err != nil {
			return
		}
		if len(retrier) == 0 || retrier[0] == nil {
			c.String(http.StatusNotImplemented, "retry processing is not configured")
			return
		}
		if err := retrier[0].Retry(c.Request.Context(), id); err != nil {
			c.String(http.StatusUnprocessableEntity, "retry could not complete: %v", err)
			return
		}
		c.Redirect(http.StatusSeeOther, fmt.Sprintf(routeBase+"/review/%d", id))
	})
	router.POST(routeBase+"/rules/:id/disable", func(c *gin.Context) {
		if !requireCSRF(c) {
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.String(http.StatusBadRequest, "invalid rule ID")
			return
		}
		if err := reviews.DisableRule(c.Request.Context(), id, c.PostForm("reviewer")); err != nil {
			c.String(http.StatusUnprocessableEntity, err.Error())
			return
		}
		c.Redirect(http.StatusSeeOther, safeReviewReturnPathForBase(c.PostForm("return_to"), routeBase))
	})
	router.POST(routeBase+"/review/:id/deliver", func(c *gin.Context) {
		if !requireCSRF(c) {
			return
		}
		if deliveries == nil {
			c.String(http.StatusNotImplemented, "delivery is not configured")
			return
		}
		id, err := candidateID(c)
		if err != nil {
			return
		}
		if _, err := deliveries.DeliverWithSchema(c.Request.Context(), id, c.PostForm("endpoint"), c.PostForm("delivery_schema")); err != nil {
			c.String(http.StatusUnprocessableEntity, err.Error())
			return
		}
		c.Redirect(http.StatusSeeOther, fmt.Sprintf(routeBase+"/review/%d", id))
	})
	registerExportRoutes(router, store, options, routeBase)
	if routeBase == "/semantic" {
		registerReviewAssets(router)
	}
	if options.Workspaces != nil && routeBase == "/semantic" {
		registerScanRoutes(router, options, requireCSRF)
		return newWorkspaceHandler(router, options)
	}
	return router
}

func reviewHostname(authority string) string {
	if ip := net.ParseIP(authority); ip != nil {
		return ip.String()
	}
	parsed, err := url.Parse("http://" + authority)
	if err != nil || parsed.Host == "" || parsed.Host != authority || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	hostname := parsed.Hostname()
	if ip := net.ParseIP(hostname); ip != nil {
		return ip.String()
	}
	return strings.ToLower(hostname)
}

func safeReviewReturnPathForBase(value, base string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return base + "/review"
	}
	switch parsed.Path {
	case "/semantic", base + "/review", "/semantic/escalations", base + "/rules", "/semantic/guides", "/semantic/reports", "/semantic/sources":
		return parsed.RequestURI()
	}
	for _, prefix := range []string{base + "/review/", "/semantic/guides/", "/semantic/reports/", "/semantic/sources/"} {
		if strings.HasPrefix(parsed.Path, prefix) {
			return parsed.RequestURI()
		}
	}
	return base + "/review"
}

func candidateID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid candidate ID")
	}
	return id, err
}

func loadReview(c *gin.Context, store ReviewWebStore) (Candidate, PageVersion, string, bool) {
	id, err := candidateID(c)
	if err != nil {
		return Candidate{}, PageVersion{}, "", false
	}
	candidate, err := store.Candidate(c.Request.Context(), id)
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return Candidate{}, PageVersion{}, "", false
	}
	page, err := store.PageVersionByID(c.Request.Context(), candidate.PageVersionID)
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return Candidate{}, PageVersion{}, "", false
	}
	encoded, _ := json.MarshalIndent(candidate.Sections, "", "  ")
	return candidate, page, string(encoded), true
}

func writeHTML(c *gin.Context, source string, data any) {
	tmpl, err := template.New("page").Funcs(templateFuncs()).Parse(source)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(c.Writer, data); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
	}
}

func writePage(c *gin.Context, title, active, content string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = title
	data["Active"] = active
	addReviewContext(c, data)
	writeHTML(c, layoutTemplate+content, data)
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"plainText":   plainTextPreview,
		"statusClass": statusClass,
		"formatTime":  formatTime,
		"navActive":   navActive,
		"elapsed":     scanElapsed,
	}
}

func renderMarkdown(markdown []byte) template.HTML {
	var rendered bytes.Buffer
	converter := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))
	if err := converter.Convert(markdown, &rendered); err != nil {
		return template.HTML(bluemonday.StrictPolicy().SanitizeBytes(markdown))
	}
	policy := bluemonday.UGCPolicy()
	policy.AllowAttrs("class").OnElements("code", "pre")
	return template.HTML(policy.SanitizeBytes(rendered.Bytes()))
}

func renderGuideMarkdown(markdown []byte) template.HTML {
	rendered := renderMarkdown(markdown)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(rendered)))
	if err != nil {
		return rendered
	}
	doc.Find("a[href]").Each(func(_ int, link *goquery.Selection) {
		href, _ := link.Attr("href")
		parsed, err := url.Parse(href)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" {
			return
		}
		for _, guide := range guideDocs() {
			if parsed.Path == filepath.Base(guide.Path) || parsed.Path == filepath.ToSlash(guide.Path) {
				parsed.Path = "/semantic/guides/" + guide.Slug
				link.SetAttr("href", parsed.String())
				break
			}
		}
	})
	content, err := doc.Find("body").Html()
	if err != nil {
		return rendered
	}
	// The input has already been sanitized, and only allowlisted hrefs changed.
	return template.HTML(content)
}

func plainTextPreview(value string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(value))
	if err != nil {
		return normalizeText(value)
	}
	text := normalizeText(doc.Text())
	if text == "" {
		return normalizeText(value)
	}
	return text
}

type dashboardMetrics struct {
	Pending            int
	Escalated          int
	Approved           int
	RejectedSuperseded int
	ActiveRules        int
}

type guideDocument struct {
	Slug        string
	Title       string
	Description string
	Path        string
}

type sourceCardData struct {
	AllowedHost       string
	AllowedPathPrefix string
	Slug              string
	Name              string
	ConfigPath        string
	SeedURL           string
	Scope             string
	DBPath            string
	ReportDir         string
	DemoCommand       string
	ModelCommand      string
	UICmd             string
	CheckCmd          string
}

func dashboardStats(candidates []Candidate, rules []SkillRule) dashboardMetrics {
	var stats dashboardMetrics
	for _, candidate := range candidates {
		switch candidate.Status {
		case CandidatePendingReview, CandidateQueued:
			stats.Pending++
		case CandidateEscalated:
			stats.Escalated++
		case CandidateApproved:
			stats.Approved++
		case CandidateRejected, CandidateSuperseded:
			stats.RejectedSuperseded++
		}
	}
	for _, rule := range rules {
		if rule.Status == "active" {
			stats.ActiveRules++
		}
	}
	return stats
}

func sourceCards(sources []SourceEntry) []sourceCardData {
	cards := make([]sourceCardData, 0, len(sources))
	for _, source := range sources {
		cards = append(cards, sourceCard(source))
	}
	return cards
}

func sourceCard(source SourceEntry) sourceCardData {
	configPath := source.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(defaultSourceConfigRoot, source.Slug+".json")
	}
	dbPath := DefaultSemanticDBPath(source.Slug)
	reportDir := DefaultSemanticReportDir(source.Slug)
	reviewMode := "openai"
	if source.Config.FixtureDirectory != "" {
		reviewMode = "fixture"
	}
	return sourceCardData{
		AllowedHost: source.Config.AllowedHost, AllowedPathPrefix: source.Config.AllowedPathPrefix,
		Slug:         source.Slug,
		Name:         source.Config.Name,
		ConfigPath:   configPath,
		SeedURL:      source.Config.SeedURL,
		Scope:        source.Config.Scope,
		DBPath:       dbPath,
		ReportDir:    reportDir,
		DemoCommand:  fmt.Sprintf("go run ./cmd/semantic-crawler -action run -mode snapshot -model-mode fixture -source-config %s -db %s", configPath, dbPath),
		ModelCommand: fmt.Sprintf("go run ./cmd/semantic-crawler -action run -mode live -model-mode openai -source-config %s -db %s", configPath, dbPath),
		UICmd:        fmt.Sprintf("go run ./cmd/semantic-crawler -action serve -model-mode %s -source-config %s -db %s -addr 127.0.0.1:8091", reviewMode, configPath, dbPath),
		CheckCmd:     fmt.Sprintf("go run ./cmd/semantic-crawler -action check -mode live -model-mode openai -source-config %s -db %s", configPath, dbPath),
	}
}

func defaultSourceSkillTemplate() string {
	return `# Source Skill

## Scope

Describe the content this source should contribute and which topics are in scope.

## Always Keep

- Substantive source content inside the configured scope, without rewriting it.
- Qualifications, exceptions, dates, units, warnings, limits, table headers and related values.

## Always Drop

- Cookie notices, navigation, share/print/email controls, login controls and irrelevant boilerplate. Keep contact/about information when it belongs to the configured scope.

## Known Layout Patterns

- Add repeatable lessons discovered during review.
`
}

func guideDocs() []guideDocument {
	return []guideDocument{
		{Slug: "semantic-readme", Title: "Overview", Description: "How crawling, source selection, review and delivery fit together.", Path: filepath.Join("docs", "semantic_readme.md")},
		{Slug: "quick-start", Title: "Quick start", Description: "Start offline, configure your provider and run your first scan.", Path: filepath.Join("docs", "semantic_quick_start.md")},
		{Slug: "reviewer-handbook", Title: "Review and recovery", Description: "Compare source content, save instructions, recover retries and deliver approvals.", Path: filepath.Join("docs", "semantic_reviewer_handbook.md")},
		{Slug: "next-source-onboarding", Title: "Source onboarding", Description: "Configure a new source and its extraction scope.", Path: filepath.Join("docs", "semantic_next_source_onboarding.md")},
		{Slug: "crawler", Title: "CLI reference", Description: "Startup and command guidance for the bundled sample.", Path: filepath.Join("docs", "semantic-crawler.md")},
		{Slug: "output-formats", Title: "Saving and exporting content", Description: "Approved exports, document schemas, content profiles and conversion limits.", Path: filepath.Join("docs", "output-formats.md")},
		{Slug: "providers", Title: "Model providers", Description: "Bring your own provider key or compatible endpoint; understand costs and compatibility checks.", Path: filepath.Join("docs", "providers.md")},
	}
}

func guideDoc(slug string) (guideDocument, bool) {
	for _, guide := range guideDocs() {
		if guide.Slug == slug {
			return guide, true
		}
	}
	return guideDocument{}, false
}

func readKnownFile(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil || filepath.IsAbs(path) {
		return content, err
	}
	return os.ReadFile(filepath.Join("..", "..", path))
}

func statusClass(status CandidateStatus) string {
	switch status {
	case CandidateApproved:
		return "status status-approved"
	case CandidateEscalated:
		return "status status-escalated"
	case CandidateRejected:
		return "status status-rejected"
	case CandidateSuperseded:
		return "status status-superseded"
	case CandidatePendingReview:
		return "status status-pending"
	default:
		return "status"
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "not recorded"
	}
	return value.UTC().Format("2006-01-02 15:04 UTC")
}

func navActive(active, item string) template.HTMLAttr {
	if active == item {
		return ` class="active"`
	}
	return ""
}

func (store *MemoryStore) PageVersionByID(_ context.Context, id int64) (PageVersion, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, page := range store.pages {
		if page.ID == id {
			return page, nil
		}
	}
	return PageVersion{}, fmt.Errorf("page version %d not found", id)
}

func (store *MemoryStore) CandidatesByStatus(_ context.Context, statuses ...CandidateStatus) ([]Candidate, error) {
	if len(statuses) == 0 {
		return store.Candidates(), nil
	}
	allowed := make(map[CandidateStatus]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	all := store.Candidates()
	result := make([]Candidate, 0, len(all))
	for _, candidate := range all {
		if _, exists := allowed[candidate.Status]; exists {
			result = append(result, candidate)
		}
	}
	return result, nil
}

func (store *MemoryStore) LatestApprovedCandidate(_ context.Context, sourceID int64, url string, excludeID int64) (Candidate, bool, error) {
	all := store.Candidates()
	for index := len(all) - 1; index >= 0; index-- {
		candidate := all[index]
		if candidate.ID != excludeID && candidate.SourceID == sourceID && candidate.URL == url && candidate.Status == CandidateApproved {
			return candidate, true, nil
		}
	}
	return Candidate{}, false, nil
}

func reviewCSRFError(request *http.Request, expected string) string {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return "cross-origin mutation is not allowed"
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		if err != nil || parsed.Scheme != scheme || !strings.EqualFold(parsed.Host, request.Host) || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "cross-origin mutation is not allowed"
		}
	}
	token := request.PostFormValue("csrf_token")
	if token == "" {
		token = request.Header.Get("X-CSRF-Token")
	}
	if token != expected {
		return "invalid CSRF token"
	}
	return ""
}
