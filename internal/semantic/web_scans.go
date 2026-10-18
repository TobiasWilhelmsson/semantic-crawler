package semantic

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

type scanMode struct {
	Value, Label, Reason string
	Ready                bool
}

type scanForm struct {
	ForceReextract                              bool
	Fetch                                       FetchSettings
	Source                                      sourceCardData
	RequestKey, Mode, MaxPages, MaxDepth, Error string
	Modes                                       []scanMode
	Ready                                       bool
}

func registerScanRoutes(router *gin.Engine, options ReviewRouterOptions, csrf func(*gin.Context) bool) {
	router.GET("/semantic/sources/:slug/scans/new", func(c *gin.Context) {
		entry, ok := scanSource(c, options)
		if !ok {
			return
		}
		form := newScanForm(c, options, entry)
		if previous := c.Query("from"); previous != "" {
			id, err := strconv.ParseInt(previous, 10, 64)
			workspace, openErr := options.Workspaces.Resolve(c.Request.Context(), entry.Slug, false)
			if err != nil || openErr != nil {
				c.Status(404)
				return
			}
			run, err := workspace.Store.ScanRun(c.Request.Context(), id)
			if err != nil || run.SourceID != entry.Config.SourceID {
				c.Status(404)
				return
			}
			form.MaxPages = strconv.Itoa(run.Config.MaxPages)
			form.MaxDepth = strconv.Itoa(run.Config.MaxDepth)
			restoreScanMode(&form, run)
		}
		writePage(c, "Start scan", "scans", uiTemplate("scanNew"), map[string]any{"Form": form, "CSRFToken": options.CSRFToken})
	})
	router.POST("/semantic/sources/:slug/scans", func(c *gin.Context) {
		if !csrf(c) {
			return
		}
		entry, ok := scanSource(c, options)
		if !ok {
			return
		}
		form := newScanForm(c, options, entry)
		form.RequestKey = c.PostForm("request_key")
		form.Mode = c.PostForm("model_mode")
		form.MaxPages = c.PostForm("max_pages")
		form.MaxDepth = c.PostForm("max_depth")
		pages, pageErr := strconv.Atoi(form.MaxPages)
		depth, depthErr := strconv.Atoi(form.MaxDepth)
		form.ForceReextract = c.PostForm("force_reextract") == "1"
		request := ScanRequest{ForceReextract: form.ForceReextract, SourceSlug: entry.Slug, RequestKey: form.RequestKey, ModelMode: form.Mode, MaxPages: pages, MaxDepth: depth}
		var scan PreparedScan
		var err error
		if options.Prepare == nil || options.Scans == nil {
			err = fmt.Errorf("managed scans are unavailable")
		} else if pageErr != nil || depthErr != nil || pages < 1 || pages > 1000 || depth < 0 || depth > 10 || len(form.RequestKey) == 0 || len(form.RequestKey) > 128 {
			err = fmt.Errorf("enter a page limit from 1–1,000 and depth from 0–10; use a fresh scan form")
		} else {
			scan, err = options.Prepare(c.Request.Context(), request)
		}
		code := 422
		if err == nil {
			var run ScanRun
			run, err = options.Scans.Start(c.Request.Context(), scan)
			if err == nil {
				c.Redirect(303, scanURL(entry.Slug, run.ID))
				return
			}
			code = 500
			form.Error = "Unable to start the scan. Check source storage and model configuration."
			if errors.Is(err, ErrScanBusy) || errors.Is(err, ErrScanRequestConflict) || errors.Is(err, ErrScanFinalizationPending) {
				code = 409
				form.Error = err.Error()
			}
		}
		if form.Error == "" {
			form.Error = err.Error()
		}
		form.Ready = true
		c.Status(code)
		writePage(c, "Start scan", "scans", uiTemplate("scanNew"), map[string]any{"Form": form, "CSRFToken": options.CSRFToken})
	})
	router.GET("/semantic/sources/:slug/scans", func(c *gin.Context) {
		entry, ok := scanSource(c, options)
		if !ok {
			return
		}
		var runs []ScanRun
		workspace, err := options.Workspaces.Resolve(c.Request.Context(), entry.Slug, false)
		if err == nil {
			runs, err = workspace.Store.ScanRuns(c.Request.Context(), entry.Config.SourceID, 100)
		}
		if err != nil && !errors.Is(err, ErrWorkspaceEmpty) {
			c.String(500, "Scan history is unavailable.")
			return
		}
		for i := range runs {
			runs[i] = scanRunForDisplay(runs[i], entry.Slug, options.Scans)
		}
		writePage(c, "Scans", "scans", uiTemplate("scanHistory"), map[string]any{"Runs": runs, "Source": sourceCard(entry)})
	})
	load := func(c *gin.Context) (*SourceWorkspace, ScanRun, bool) {
		entry, ok := scanSource(c, options)
		if !ok {
			return nil, ScanRun{}, false
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.Status(404)
			return nil, ScanRun{}, false
		}
		workspace, err := options.Workspaces.Resolve(c.Request.Context(), entry.Slug, false)
		if err != nil {
			c.Status(404)
			return nil, ScanRun{}, false
		}
		run, err := workspace.Store.ScanRun(c.Request.Context(), id)
		if err != nil || run.SourceID != entry.Config.SourceID {
			c.Status(404)
			return nil, ScanRun{}, false
		}
		return workspace, run, true
	}
	router.GET("/semantic/sources/:slug/scans/:id", func(c *gin.Context) {
		workspace, run, ok := load(c)
		if !ok {
			return
		}
		progress, err := workspace.Store.ScanProgress(c.Request.Context(), run.ID)
		if err != nil {
			c.String(500, "Scan progress is unavailable.")
			return
		}
		owned := false
		run = scanRunForDisplay(run, workspace.Source.Slug, options.Scans)
		if run.State == ScanFinalizationFailed {
			progress.State = run.State
			progress.FinalizationError = run.Error
		}
		if options.Scans != nil {
			slug, id, active := options.Scans.Active()
			owned = active && slug == workspace.Source.Slug && id == run.ID
		}
		writePage(c, "Scan details", "scans", uiTemplate("scanDetail"), map[string]any{
			"Run":                  run,
			"Progress":             progress,
			"Owned":                owned,
			"CanRetryFinalization": options.Scans != nil,
			"CSRFToken":            options.CSRFToken,
			"ScanURL":              scanURL(workspace.Source.Slug, run.ID),
			"Counters":             scanCounters(progress),
		})
	})
	router.GET("/semantic/sources/:slug/scans/:id/status", func(c *gin.Context) {
		workspace, run, ok := load(c)
		if !ok {
			return
		}
		progress, err := workspace.Store.ScanProgress(c.Request.Context(), run.ID)
		if err != nil {
			c.String(500, "Scan progress is unavailable.")
			return
		}
		run = scanRunForDisplay(run, workspace.Source.Slug, options.Scans)
		if run.State == ScanFinalizationFailed {
			progress.State = run.State
			progress.FinalizationError = run.Error
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, progress)
	})
	router.POST("/semantic/sources/:slug/scans/:id/retry-finalization", func(c *gin.Context) {
		if !csrf(c) {
			return
		}
		workspace, run, ok := load(c)
		if !ok {
			return
		}
		if options.Scans == nil {
			c.String(http.StatusConflict, ErrScanFinalizationNotPending.Error())
			return
		}
		if err := options.Scans.RetryFinalization(c.Request.Context(), workspace.Source.Slug, run.ID); err != nil {
			switch {
			case errors.Is(err, ErrScanBusy):
				c.String(http.StatusConflict, "A scan is running. Wait for it to finish before retrying.")
			case errors.Is(err, ErrScanFinalizationNotPending):
				c.String(http.StatusConflict, err.Error())
			default:
				c.String(http.StatusServiceUnavailable, "Unable to save the scan result. Check source storage, then retry saving from the scan details.")
			}
			return
		}
		c.Redirect(http.StatusSeeOther, scanURL(workspace.Source.Slug, run.ID))
	})
	router.POST("/semantic/sources/:slug/scans/:id/cancel", func(c *gin.Context) {
		if !csrf(c) {
			return
		}
		workspace, run, ok := load(c)
		if !ok {
			return
		}
		if run.State.Active() {
			if options.Scans == nil {
				c.String(409, ErrScanNotOwned.Error())
				return
			}
			if err := options.Scans.Cancel(c.Request.Context(), workspace.Source.Slug, run.ID); err != nil {
				c.String(409, "This scan is no longer owned by the review server. Stop an external scan in its terminal.")
				return
			}
		}
		c.Redirect(303, scanURL(workspace.Source.Slug, run.ID))
	})
}

func scanURL(slug string, id int64) string {
	return fmt.Sprintf("/semantic/sources/%s/scans/%d", slug, id)
}

// Annotate a view without claiming the terminal result was persisted.
func scanRunForDisplay(run ScanRun, slug string, manager *ScanManager) ScanRun {
	if manager != nil && manager.PendingFinalization(slug, run.ID) {
		run.State = ScanFinalizationFailed
		run.Error = ErrScanFinalizationPending.Error()
	}
	return run
}

func scanSource(c *gin.Context, options ReviewRouterOptions) (SourceEntry, bool) {
	entry, _, err := options.Workspaces.Lookup(c.Param("slug"))
	if err != nil {
		c.String(http.StatusNotFound, "Source configuration is unavailable.")
		return SourceEntry{}, false
	}
	child := options
	child.SourceSlug = entry.Slug
	child.SourceID = entry.Config.SourceID
	child.SourceName = entry.Config.Name
	setReviewContext(c, child, "/semantic/sources/"+entry.Slug)
	return entry, true
}

func newScanForm(c *gin.Context, options ReviewRouterOptions, entry SourceEntry) scanForm {
	pages := entry.Config.MaxPages
	if pages < 1 {
		pages = 20
	}
	if pages > 1000 {
		pages = 1000
	}
	depth := entry.Config.MaxDepth
	if depth < 0 {
		depth = 0
	}
	if depth > 10 {
		depth = 10
	}
	mode := "openai"
	if entry.Config.FixtureDirectory != "" {
		mode = "fixture"
	}
	form := scanForm{Source: sourceCard(entry), RequestKey: randomRequestKey(), Mode: mode, MaxPages: strconv.Itoa(pages), MaxDepth: strconv.Itoa(depth)}
	form.Fetch = options.FetchSettings
	if form.Fetch.Delay == 0 {
		form.Fetch.Delay = 750 * time.Millisecond
	}
	if form.Fetch.MaxPageBytes == 0 {
		form.Fetch.MaxPageBytes = DefaultMaxPageBytes
	}
	for _, value := range []string{"fixture", "openai"} {
		label := "Configured model"
		if value == "fixture" {
			label = "Offline sample"
		}
		item := scanMode{Value: value, Label: label}
		if options.Prepare != nil {
			scan, err := options.Prepare(c.Request.Context(), ScanRequest{SourceSlug: entry.Slug, ModelMode: value, MaxPages: pages, MaxDepth: depth, RequestKey: form.RequestKey})
			item.Ready = err == nil
			if err != nil {
				item.Reason = err.Error()
			} else if value == "openai" {
				item.Label += " · " + scan.Config.Model
			}
		}
		if value == form.Mode {
			form.Ready = item.Ready
		}
		form.Modes = append(form.Modes, item)
	}
	if !form.Ready {
		for _, item := range form.Modes {
			if item.Ready {
				form.Mode = item.Value
				form.Ready = true
				break
			}
		}
	}
	return form
}

func randomRequestKey() string { return rand.Text() }

// Restore the previous mode without substituting an available but different
// model. Legacy CLI runs predate explicit mode metadata.
func restoreScanMode(form *scanForm, run ScanRun) {
	mode := run.ModelMode
	if mode == "" {
		switch run.Config.Model {
		case "fixture", "deterministic-fixture-model":
			mode = "fixture"
		case "":
		default:
			mode = "openai"
		}
	}
	form.Mode = mode
	form.Ready = false
	found, selectedReady := false, false
	for _, item := range form.Modes {
		form.Ready = form.Ready || item.Ready
		if item.Value == mode {
			found, selectedReady = true, item.Ready
		}
	}
	if !found {
		form.Mode = ""
		form.Modes = append([]scanMode{{Value: "", Label: "Choose extraction mode"}}, form.Modes...)
		form.Error = "The previous scan did not record a recognized extraction mode. Choose an available mode before starting."
	} else if !selectedReady {
		form.Error = "The previous scan's extraction mode is unavailable. Restore its setup or choose an available mode before starting."
	}
}

func registerReviewAssets(router *gin.Engine) {
	router.GET("/semantic/assets/:name", func(c *gin.Context) {
		name := c.Param("name")
		kind := ""
		switch name {
		case "app.css":
			kind = "text/css; charset=utf-8"
		case "scans.js", "review.js":
			kind = "text/javascript; charset=utf-8"
		default:
			c.Status(404)
			return
		}
		body, err := reviewAssets.ReadFile("ui/" + name)
		if err != nil {
			c.Status(404)
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(200, kind, body)
	})
}

type scanCounter struct {
	Label, Key string
	Value      int
}

func scanCounters(p ScanProgress) []scanCounter {
	return []scanCounter{{"Discovered", "discovered", p.Discovered}, {"Waiting", "waiting", p.Waiting}, {"Processing", "processing", p.Processing}, {"Finished", "finished", p.Finished}, {"Unchanged", "unchanged", p.Unchanged}, {"Candidates", "candidates", p.Candidates}, {"Failed", "failed", p.Failed}}
}
