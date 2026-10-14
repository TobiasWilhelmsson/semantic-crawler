package semantic

import (
	"embed"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

//go:embed ui/*
var reviewAssets embed.FS

func uiTemplate(name string) string {
	body, err := reviewAssets.ReadFile("ui/" + name + ".html")
	if err != nil {
		panic(err)
	}
	return string(body)
}

var layoutTemplate = uiTemplate("layoutTemplate")
var dashboardTemplate = uiTemplate("dashboardTemplate")
var queueTemplate = uiTemplate("queueTemplate")
var escalationsTemplate = uiTemplate("escalationsTemplate")
var rulesTemplate = uiTemplate("rulesTemplate")
var guidesTemplate = uiTemplate("guidesTemplate")
var guideDetailTemplate = uiTemplate("guideDetailTemplate")
var sourcesTemplate = uiTemplate("sourcesTemplate")
var sourceNewTemplate = uiTemplate("sourceNewTemplate")
var sourceDetailTemplate = uiTemplate("sourceDetailTemplate")
var reviewTemplate = uiTemplate("reviewTemplate")

func filterSourceCandidates(candidates []Candidate, sourceID int64) []Candidate {
	if sourceID == 0 {
		return candidates
	}
	result := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.SourceID == sourceID {
			result = append(result, candidate)
		}
	}
	return result
}
func filterSourceRules(rules []SkillRule, sourceID int64) []SkillRule {
	if sourceID == 0 {
		return rules
	}
	result := make([]SkillRule, 0, len(rules))
	for _, rule := range rules {
		if rule.SourceID == sourceID {
			result = append(result, rule)
		}
	}
	return result
}
func setReviewContext(c *gin.Context, options ReviewRouterOptions, base string) {
	c.Set("review_base", base)
	c.Set("review_options", options)
}
func addReviewContext(c *gin.Context, data map[string]any) {
	base, _ := c.Get("review_base")
	if base == nil {
		base = "/semantic"
	}
	data["Base"] = base
	value, _ := c.Get("review_options")
	options, _ := value.(ReviewRouterOptions)
	slug, name := options.SourceSlug, options.SourceName
	if options.Workspaces != nil && slug == "" {
		slug = options.Workspaces.startup.Slug
		name = options.Workspaces.startup.Config.Name
	}
	data["CurrentSourceSlug"] = slug
	data["CurrentSourceName"] = name
	data["SourceBase"] = "/semantic/sources/" + slug
	if options.Workspaces != nil {
		statuses, err := options.Workspaces.List()
		if err == nil {
			data["SourceOptions"] = statuses
		}
		data["ManagedScans"] = true
	}
	if options.Scans != nil {
		if source, id, active := options.Scans.Active(); active {
			data["ActiveScanURL"] = fmt.Sprintf("/semantic/sources/%s/scans/%d", source, id)
		}
	}
}
func checkReviewSource(c *gin.Context, store ReviewWebStore, sourceID int64, base string) bool {
	if sourceID == 0 {
		return true
	}
	path := strings.TrimPrefix(c.Request.URL.Path, base)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || (parts[0] != "review" && parts[0] != "snapshots" && parts[0] != "rules") {
		return true
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatus(404)
		return false
	}
	valid := false
	switch parts[0] {
	case "review":
		candidate, err := store.Candidate(c.Request.Context(), id)
		if c.Request.Method == http.MethodPost && errors.Is(err, gorm.ErrRecordNotFound) {
			return true
		}
		valid = err == nil && candidate.SourceID == sourceID
	case "snapshots":
		page, err := store.PageVersionByID(c.Request.Context(), id)
		valid = err == nil && page.SourceID == sourceID
	case "rules":
		rules, err := store.AllRules(c.Request.Context())
		if err == nil {
			for _, rule := range rules {
				if rule.ID == id && rule.SourceID == sourceID {
					valid = true
				}
			}
		}
	}
	if !valid {
		c.AbortWithStatus(404)
	}
	return valid
}
func newWorkspaceHandler(root http.Handler, options ReviewRouterOptions) http.Handler {
	var mu sync.Mutex
	handlers := make(map[string]http.Handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The root security middleware guards the new routes, and source routers apply
		// the same Host/CSRF policy before accessing review state.
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/semantic/sources/"), "/")
		if strings.HasPrefix(r.URL.Path, "/semantic/sources/") && len(parts) > 1 && (parts[1] == "review" || parts[1] == "snapshots" || parts[1] == "rules" || parts[1] == "escalations" || parts[1] == "export") {
			if !allowedReviewHost(r.Host, options.AllowedHosts) {
				w.WriteHeader(403)
				return
			}
			if r.Method == http.MethodPost {
				if message := reviewCSRFError(r, options.CSRFToken); message != "" {
					http.Error(w, message, http.StatusForbidden)
					return
				}
			}
			workspace, err := options.Workspaces.Resolve(r.Context(), parts[0], false)
			if err != nil {
				if err == ErrWorkspaceEmpty {
					w.Header().Set("Location", "/semantic/sources/"+parts[0])
					w.WriteHeader(303)
				} else {
					http.Error(w, "Source workspace is unavailable", 404)
				}
				return
			}
			mu.Lock()
			handler := handlers[parts[0]]
			if handler == nil {
				child := options
				child.BasePath = "/semantic/sources/" + parts[0]
				child.SourceID = workspace.Source.Config.SourceID
				child.SourceSlug = parts[0]
				child.SourceName = workspace.Source.Config.Name
				handler = NewReviewRouterWithOptions(workspace.Store, workspace.Reviews, workspace.Deliveries, child, workspace.Retrier)
				handlers[parts[0]] = handler
			}
			mu.Unlock()
			handler.ServeHTTP(w, r)
			return
		}
		root.ServeHTTP(w, r)
	})
}
func allowedReviewHost(host string, hosts []string) bool {
	if len(hosts) == 0 {
		hosts = []string{"localhost", "127.0.0.1", "::1"}
	}
	for _, allowed := range hosts {
		if reviewHostname(host) == reviewHostname(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

func scanElapsed(run ScanRun) string {
	end := time.Now()
	if run.CompletedAt != nil {
		end = *run.CompletedAt
	}
	duration := end.Sub(run.StartedAt)
	if duration < 0 {
		duration = 0
	}
	return duration.Truncate(time.Second).String()
}
