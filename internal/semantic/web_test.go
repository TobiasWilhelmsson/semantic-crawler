package semantic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestSemanticAdminRoutesRenderShellAndCorePages(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/pending", SnapshotHTML: `<html><body><p data-semantic-block="b0001">Clinical text</p></body></html>`})
	store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidatePendingReview, Model: "fixture", PromptVersion: "semantic-crawler-v1", InputTokens: 10, OutputTokens: 3, LatencyMS: 42})
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/escalated", Status: CandidateEscalated})
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/approved", Status: CandidateApproved})
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/rejected", Status: CandidateRejected})
	_, _ = store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "Remove share controls", Version: 2, Status: "active"})

	sourceRoot := t.TempDir()
	writeTestSourceConfig(t, sourceRoot, "sample", SourceConfig{Name: "Sample Source", SeedURL: "https://source.example.test/start/", AllowedHost: "source.example.test", AllowedPathPrefix: "/start/", Scope: "Clinical scope", MaxDepth: 2, MaxPages: 20, SkillVersion: 1, SourceSkillPath: filepath.Join(sourceRoot, "source-skills", "sample.md")})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret", SourceConfigRoot: sourceRoot})

	for _, path := range []string{"/semantic", "/semantic/review", "/semantic/escalations", "/semantic/rules", "/semantic/guides", "/semantic/sources"} {
		request := newLocalReviewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, expected := range []string{`<meta name="viewport"`, `<main class="app-main"`, `href="/semantic/review"`, `href="/semantic/escalations"`, `href="/semantic/rules"`, `href="/semantic/guides"`, `href="/semantic/sources"`} {
			if !strings.Contains(body, expected) {
				t.Fatalf("%s missing shared shell element %q: %s", path, expected, body)
			}
		}
	}
}

func TestSemanticDashboardShowsCountsAndQuickLinks(t *testing.T) {
	store := NewMemoryStore()
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/pending", Status: CandidatePendingReview})
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/escalated", Status: CandidateEscalated})
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/approved", Status: CandidateApproved})
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/superseded", Status: CandidateSuperseded})
	_, _ = store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "Keep safety warnings", Version: 2, Status: "active"})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})

	request := newLocalReviewRequest(http.MethodGet, "/semantic", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	body := response.Body.String()
	for _, expected := range []string{"Awaiting review", "Escalated", "Approved", "Rejected / superseded", "Active instructions", "1", "Review queue", "Guides"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("dashboard missing %q: %d %s", expected, response.Code, body)
		}
	}
}

func TestReviewQueueEscalationsAndRulesPagesShowExpectedRecords(t *testing.T) {
	store := NewMemoryStore()
	pendingPage := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/pending", SnapshotHTML: `<html><body></body></html>`})
	store.PutCandidate(Candidate{SourceID: 1, PageVersionID: pendingPage.ID, URL: pendingPage.URL, Status: CandidatePendingReview, Model: "fixture", PromptVersion: "semantic-crawler-v1", InputTokens: 11, OutputTokens: 5, LatencyMS: 20})
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/escalated", Status: CandidateEscalated})
	_, _ = store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "source-scoped rule", Version: 2, Status: "active", CreatedBy: "reviewer"})
	disabled, _ := store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeURL, OriginURL: pendingPage.URL, Instruction: "url-scoped disabled rule", Version: 3, Status: "active", CreatedBy: "reviewer"})
	_ = store.DisableRule(context.Background(), disabled.ID)
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})

	queue := httptest.NewRecorder()
	router.ServeHTTP(queue, newLocalReviewRequest(http.MethodGet, "/semantic/review", nil))
	queueBody := queue.Body.String()
	for _, expected := range []string{"https://example.test/pending", "pending_review", "fixture", "semantic-crawler-v1", "/semantic/review/1"} {
		if queue.Code != http.StatusOK || !strings.Contains(queueBody, expected) {
			t.Fatalf("queue missing %q: %d %s", expected, queue.Code, queueBody)
		}
	}

	escalations := httptest.NewRecorder()
	router.ServeHTTP(escalations, newLocalReviewRequest(http.MethodGet, "/semantic/escalations", nil))
	escalationBody := escalations.Body.String()
	if !strings.Contains(escalationBody, "https://example.test/escalated") || strings.Contains(escalationBody, "https://example.test/pending") {
		t.Fatalf("escalations page should only show escalated candidates: %s", escalationBody)
	}

	rules := httptest.NewRecorder()
	router.ServeHTTP(rules, newLocalReviewRequest(http.MethodGet, "/semantic/rules", nil))
	rulesBody := rules.Body.String()
	for _, expected := range []string{"source-scoped rule", "url-scoped disabled rule", "source", "url", "disabled", "/semantic/rules/1/disable"} {
		if rules.Code != http.StatusOK || !strings.Contains(rulesBody, expected) {
			t.Fatalf("rules page missing %q: %d %s", expected, rules.Code, rulesBody)
		}
	}
}

func TestGuidesUseSafeHardcodedRegistryAndReportsAreUnavailable(t *testing.T) {
	store := NewMemoryStore()
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})

	guides := httptest.NewRecorder()
	router.ServeHTTP(guides, newLocalReviewRequest(http.MethodGet, "/semantic/guides", nil))
	guideBody := guides.Body.String()
	for _, expected := range []string{`href="/semantic/guides/semantic-readme"`, `href="/semantic/guides/quick-start"`, `href="/semantic/guides/reviewer-handbook"`, `href="/semantic/guides/next-source-onboarding"`} {
		if guides.Code != http.StatusOK || !strings.Contains(guideBody, expected) {
			t.Fatalf("guides page missing %q: %d %s", expected, guides.Code, guideBody)
		}
	}

	quickStart := httptest.NewRecorder()
	router.ServeHTTP(quickStart, newLocalReviewRequest(http.MethodGet, "/semantic/guides/quick-start", nil))
	quickStartBody := quickStart.Body.String()
	for _, expected := range []string{"<h1>Semantic Crawler Quick Start</h1>", "make setup", "make start", "make doctor", `href="http://127.0.0.1:8091/semantic"`, "same kind of approved, source-correct output", "<code>SkillRule</code>"} {
		if quickStart.Code != http.StatusOK || !strings.Contains(quickStartBody, expected) {
			t.Fatalf("quick start guide missing %q: %d %s", expected, quickStart.Code, quickStartBody)
		}
	}

	guide := httptest.NewRecorder()
	router.ServeHTTP(guide, newLocalReviewRequest(http.MethodGet, "/semantic/guides/reviewer-handbook", nil))
	guideDetail := guide.Body.String()
	if guide.Code != http.StatusOK || !strings.Contains(guideDetail, "<h1>Semantic Reviewer Handbook</h1>") || !strings.Contains(guideDetail, "<li>") || !strings.Contains(guideDetail, "<code>Approve</code>") {
		t.Fatalf("guide detail did not render known guide: %d %s", guide.Code, guide.Body.String())
	}
	if strings.Contains(guideDetail, `<pre class="doc-content"># Semantic Reviewer Handbook`) {
		t.Fatalf("guide markdown should be rendered, not shown as raw markdown: %s", guideDetail)
	}

	for _, path := range []string{"/semantic/reports", "/semantic/reports/benchmark-html"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, path, nil))
		if response.Code != 404 {
			t.Fatalf("report UI remains: %s %d", path, response.Code)
		}
	}
	for _, unsafePath := range []string{"/semantic/guides/../../secret", "/semantic/reports/../../secret"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, unsafePath, nil))
		if response.Code != http.StatusNotFound && response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe path %s returned %d: %s", unsafePath, response.Code, response.Body.String())
		}
	}
}

// Registered guides must be reachable, and relative documentation links must
// resolve to those routes rather than a filesystem-looking URL in the browser.
func TestGuideLinksResolveToAllowlistedRoutes(t *testing.T) {
	store := NewMemoryStore()
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	for _, slug := range []string{"output-formats", "providers"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, "/semantic/guides/"+slug, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<h1>") {
			t.Fatalf("guide %s unavailable: %d", slug, response.Code)
		}
	}
	for _, path := range []string{"/semantic/guides/unknown", "/semantic/guides/%2e%2e%2f.env", "/semantic/guides/README.md"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound && response.Code != http.StatusBadRequest {
			t.Fatalf("unregistered guide exposed: %s: %d", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, "/semantic/guides/quick-start", nil))
	if !strings.Contains(response.Body.String(), `href="/semantic/guides/providers"`) ||
		!strings.Contains(response.Body.String(), `href="/semantic/guides/output-formats"`) {
		t.Fatal("quick-start links do not resolve to the served guides")
	}
}

func TestReviewServerRendersSnapshotAndEditableOutput(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/article", SnapshotHTML: `<html><body><p data-semantic-block="b0001">Clinical text</p></body></html>`})
	candidate := store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidatePendingReview, Sections: []Section{{BlockID: "b0001", Role: RoleContent, Content: "<p>Clinical text</p>"}}})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})

	request := newLocalReviewRequest(http.MethodGet, "/semantic/review/1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{`iframe`, `/semantic/snapshots/1`, `Open live page`, `Clinical text`, `b0001`, `Retry with instruction`, `Remove selected and approve`, `name="remove_block_ids"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("review page missing %q: %s", expected, body)
		}
	}
	if !strings.Contains(body, `<summary>Source HTML</summary>`) || !strings.Contains(body, `&lt;p&gt;Clinical text&lt;/p&gt;`) {
		t.Fatalf("review page should keep raw HTML available for audit: %s", body)
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	frame := document.Find("iframe#source-frame")
	src, _ := frame.Attr("src")
	title, _ := frame.Attr("title")
	_, sandboxed := frame.Attr("sandbox")
	if frame.Length() != 1 || sandboxed || src != "/semantic/snapshots/1" || title == "" {
		t.Fatalf("review iframe should be same-origin readable for synchronized highlighting: %s", body)
	}
	_ = candidate
}

func TestReviewServerRemoveSelectedSectionsApprovesAuditedEdit(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/article", SnapshotHTML: `<html><body></body></html>`})
	store.PutCandidate(Candidate{
		SourceID:      1,
		PageVersionID: page.ID,
		URL:           page.URL,
		Status:        CandidatePendingReview,
		Sections: []Section{
			{BlockID: "keep", Role: RoleContent, Content: "<p>Clinical text</p>"},
			{BlockID: "drop", Role: RoleNavigation, Content: "<p>Share this page</p>"},
		},
	})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	form := url.Values{"csrf_token": {"secret"}, "reviewer": {"reviewer"}, "comment": {"share control"}, "remove_block_ids": {"drop"}}

	request := newLocalReviewRequest(http.MethodPost, "/semantic/review/1/remove-selected", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/semantic/review/2" {
		t.Fatalf("remove-selected approval should redirect to approved revision, got %q", location)
	}
	original, _ := store.Candidate(request.Context(), 1)
	if original.Status != CandidateSuperseded {
		t.Fatalf("original should be superseded: %#v", original)
	}
	approved, _ := store.Candidate(request.Context(), 2)
	if approved.Status != CandidateApproved || len(approved.Sections) != 1 || approved.Sections[0].BlockID != "keep" {
		t.Fatalf("approved candidate should keep only selected sections: %#v", approved)
	}
	reviews := store.Reviews()
	if len(reviews) != 1 || reviews[0].Action != "remove_selected_approve" || !strings.Contains(reviews[0].Comment, "removed blocks: drop") {
		t.Fatalf("review audit trail missing removed block IDs: %#v", reviews)
	}
}

func TestSemanticSourcePagesListKnownSourcesAndCreateNewSource(t *testing.T) {
	store := NewMemoryStore()
	sourceRoot := t.TempDir()
	writeTestSourceConfig(t, sourceRoot, "sample", SourceConfig{Name: "Sample Source", SeedURL: "https://source.example.test/start/", AllowedHost: "source.example.test", AllowedPathPrefix: "/start/", Scope: "Clinical scope", MaxDepth: 2, MaxPages: 20, SkillVersion: 1, SourceSkillPath: filepath.Join(sourceRoot, "source-skills", "sample.md")})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret", SourceConfigRoot: sourceRoot})

	list := httptest.NewRecorder()
	router.ServeHTTP(list, newLocalReviewRequest(http.MethodGet, "/semantic/sources", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Sample Source") || !strings.Contains(list.Body.String(), "/semantic/sources/sample") {
		t.Fatalf("source list missing expected content: %d %s", list.Code, list.Body.String())
	}

	detail := httptest.NewRecorder()
	router.ServeHTTP(detail, newLocalReviewRequest(http.MethodGet, "/semantic/sources/sample", nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "semantic-sample.db") || !strings.Contains(detail.Body.String(), "go run ./cmd/semantic-crawler") {
		t.Fatalf("source detail missing commands: %d %s", detail.Code, detail.Body.String())
	}

	form := url.Values{
		"csrf_token":     {"secret"},
		"slug":           {"new-source"},
		"name":           {"New Source"},
		"seed_url":       {"https://example.test/clinical/"},
		"scope":          {"Include clinical content."},
		"skill_markdown": {"# New Source\n\nKeep clinical content.\n"},
	}
	request := newLocalReviewRequest(http.MethodPost, "/semantic/sources/new", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	create := httptest.NewRecorder()
	router.ServeHTTP(create, request)
	if create.Code != http.StatusSeeOther || create.Header().Get("Location") != "/semantic/sources/new-source" {
		t.Fatalf("unexpected create response: %d %q %s", create.Code, create.Header().Get("Location"), create.Body.String())
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, "new-source.json")); err != nil {
		t.Fatalf("new source config was not created: %v", err)
	}
}

func TestSnapshotResponseUsesStrictCSP(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/article", SnapshotHTML: `<html><body><script>alert(1)</script></body></html>`})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})

	request := newLocalReviewRequest(http.MethodGet, "/semantic/snapshots/1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	csp := response.Header().Get("Content-Security-Policy")
	for _, expected := range []string{"default-src 'none'", "script-src 'none'", "object-src 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, expected) {
			t.Fatalf("snapshot CSP missing %q: %q", expected, csp)
		}
	}
	_ = page
}

func TestReviewServerSourceScopedRetryCreatesNewQueuedCandidate(t *testing.T) {
	store := NewMemoryStore()
	pending := store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview, SkillVersion: 1})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	form := url.Values{"reviewer": {"reviewer"}, "instruction": {"Retain every dosage table"}, "scope": {"source"}, "csrf_token": {"secret"}}

	request := newLocalReviewRequest(http.MethodPost, "/semantic/review/1/retry", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	stored, _ := store.Candidate(request.Context(), pending.ID)
	if stored.Status != CandidateSuperseded {
		t.Fatalf("pending candidate was not superseded: %#v", stored)
	}
	rules, _ := store.Rules(request.Context(), 1, "https://example.test/other")
	if len(rules) != 1 || rules[0].Scope != RuleScopeSource {
		t.Fatalf("source rule was not created: %#v", rules)
	}
}

func TestReviewServerRequiresCSRFTokenWhenConfigured(t *testing.T) {
	store := NewMemoryStore()
	store.PutCandidate(Candidate{SourceID: 1, URL: "https://example.test/a", Status: CandidatePendingReview, SkillVersion: 1})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	form := url.Values{"reviewer": {"reviewer"}}

	request := newLocalReviewRequest(http.MethodPost, "/semantic/review/1/approve", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing CSRF token to be forbidden, got %d %s", response.Code, response.Body.String())
	}

	form.Set("csrf_token", "secret")
	request = newLocalReviewRequest(http.MethodPost, "/semantic/review/1/approve", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected valid CSRF token to allow POST, got %d %s", response.Code, response.Body.String())
	}
}

func TestReviewServerRendersCSRFTokenInForms(t *testing.T) {
	store := NewMemoryStore()
	page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/article", SnapshotHTML: `<html><body></body></html>`})
	store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: CandidatePendingReview})
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})

	request := newLocalReviewRequest(http.MethodGet, "/semantic/review/1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="csrf_token" value="secret"`) {
		t.Fatalf("review page did not render CSRF token: %d %s", response.Code, response.Body.String())
	}
}

func TestReviewServerSanitizesRuleDisableReturnPath(t *testing.T) {
	store := NewMemoryStore()
	rule, err := store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "retain tables", Version: 1, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	form := url.Values{"csrf_token": {"secret"}, "return_to": {"https://evil.test/phish"}}

	request := newLocalReviewRequest(http.MethodPost, "/semantic/rules/1/disable", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/semantic/review" {
		t.Fatalf("expected unsafe redirect to be sanitized, got %d %q", response.Code, response.Header().Get("Location"))
	}
	rules, _ := store.Rules(request.Context(), rule.SourceID, "https://example.test/a")
	if len(rules) != 0 {
		t.Fatalf("rule should still have been disabled: %#v", rules)
	}
}

func TestReviewServerAllowsRulesReturnPathAfterDisablingRule(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.AddRule(context.Background(), SkillRule{SourceID: 1, Scope: RuleScopeSource, Instruction: "retain tables", Version: 1, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	router := NewReviewRouterWithOptions(store, NewReviewService(store), nil, ReviewRouterOptions{CSRFToken: "secret"})
	form := url.Values{"csrf_token": {"secret"}, "return_to": {"/semantic/rules"}}

	request := newLocalReviewRequest(http.MethodPost, "/semantic/rules/1/disable", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/semantic/rules" {
		t.Fatalf("expected rules return path to be allowed, got %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestReviewPageOnlyOffersValidStateTransitions(t *testing.T) {
	for _, status := range []CandidateStatus{CandidateQueued, CandidatePendingReview, CandidateEscalated, CandidateApproved, CandidateRejected, CandidateSuperseded} {
		t.Run(string(status), func(t *testing.T) {
			store := NewMemoryStore()
			page := store.PutPageVersion(PageVersion{SourceID: 1, URL: "https://example.test/page"})
			candidate := store.PutCandidate(Candidate{SourceID: 1, PageVersionID: page.ID, URL: page.URL, Status: status, Sections: []Section{{BlockID: "b0001", Content: "<p>Content</p>"}}})
			router := NewReviewRouter(store, NewReviewService(store), nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newLocalReviewRequest(http.MethodGet, "/semantic/review/1", nil))
			if response.Code != 200 {
				t.Fatalf("review page: %d", response.Code)
			}
			doc, err := goquery.NewDocumentFromReader(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			editable := status == CandidatePendingReview || status == CandidateEscalated
			for action, want := range map[string]bool{"edit": editable, "remove-selected": editable, "approve": editable, "retry": editable, "reject": editable, "escalate": status == CandidatePendingReview, "resume": status == CandidateQueued, "deliver": status == CandidateApproved} {
				found := doc.Find(`form[action="/semantic/review/1/`+action+`"]`).Length() > 0
				if found != want {
					t.Errorf("candidate %d in %s: action %s offered=%t want=%t", candidate.ID, status, action, found, want)
				}
			}
			if !strings.Contains(doc.Text(), "Content") {
				t.Error("read-only output must remain visible")
			}
		})
	}
}
