package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewerLearningResumeFixture(t *testing.T, mode ReviewerLearningMode) ReviewerLearningOptions {
	t.Helper()
	directory := t.TempDir()
	fixtures := filepath.Join(directory, "fixtures")
	if err := os.Mkdir(fixtures, 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := `{"urls":[
		{"url":"https://example.test/topic/a/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/b/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]},
		{"url":"https://example.test/topic/c/","identifier":[{"name":"title","role":"header","container":"h1","parse_mode":true}]}
	]}`
	mappingPath := filepath.Join(directory, "mapping.json")
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(fixtures, name+".html"), []byte(`<main><h1>Title `+name+`</h1></main>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ReviewerLearningOptions{
		FixtureSet: "resume-inputs", FixtureDirectory: fixtures, MappingPath: mappingPath,
		ReportDirectory: filepath.Join(directory, "reports"), Scope: "Keep article headings.",
		SourceSkill: SourceSkill{Content: "Keep headings.", Hash: "original-skill", Version: 1},
		Model:       &FixtureModel{}, ModelMode: "fixture", LearningMode: mode,
	}
}

func TestReviewerLearningResumeRejectsChangedEvidenceInputs(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *ReviewerLearningOptions)
	}{
		{"model mode", func(_ *testing.T, options *ReviewerLearningOptions) { options.ModelMode = "openai" }},
		{"start offset", func(_ *testing.T, options *ReviewerLearningOptions) { options.StartPage = 2 }},
		{"page limit", func(_ *testing.T, options *ReviewerLearningOptions) { options.MaxPages = 1 }},
		{"URL filter", func(_ *testing.T, options *ReviewerLearningOptions) { options.URLContains = "/b/" }},
		{"scope", func(_ *testing.T, options *ReviewerLearningOptions) { options.Scope = "Keep warnings only." }},
		{"skill content", func(_ *testing.T, options *ReviewerLearningOptions) { options.SourceSkill.Content = "Drop headings." }},
		{"skill version", func(_ *testing.T, options *ReviewerLearningOptions) { options.SourceSkill.Version = 2 }},
		{"live fetching", func(_ *testing.T, options *ReviewerLearningOptions) { options.LiveFetch = true }},
		{"fixture source", func(_ *testing.T, options *ReviewerLearningOptions) {
			options.FixtureDirectory = filepath.Dir(options.FixtureDirectory)
		}},
		{"quality threshold", func(_ *testing.T, options *ReviewerLearningOptions) { options.QualityThreshold = 0.99 }},
		{"adaptive stopping", func(_ *testing.T, options *ReviewerLearningOptions) { options.AdaptiveStop = true }},
		{"baseline selector", func(t *testing.T, options *ReviewerLearningOptions) {
			content, err := os.ReadFile(options.MappingPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(options.MappingPath, bytes.ReplaceAll(content, []byte(`"container":"h1"`), []byte(`"container":"main"`)), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"baseline order", func(t *testing.T, options *ReviewerLearningOptions) {
			content, err := os.ReadFile(options.MappingPath)
			if err != nil {
				t.Fatal(err)
			}
			content = bytes.ReplaceAll(content, []byte("/a/"), []byte("/temporary/"))
			content = bytes.ReplaceAll(content, []byte("/b/"), []byte("/a/"))
			content = bytes.ReplaceAll(content, []byte("/temporary/"), []byte("/b/"))
			if err := os.WriteFile(options.MappingPath, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"audited override", func(t *testing.T, options *ReviewerLearningOptions) {
			options.GoldOverridePath = filepath.Join(filepath.Dir(options.MappingPath), "overrides.json")
			if err := os.WriteFile(options.GoldOverridePath, []byte(`{"overrides":[{"url":"https://example.test/topic/a/","identifier_name":"title","role":"header","audited_text":"Different audited title","parse_mode":true}]}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		for _, testCase := range cases {
			t.Run(string(mode)+"/"+testCase.name, func(t *testing.T) {
				options := reviewerLearningResumeFixture(t, mode)
				if _, err := RunReviewerLearningBenchmark(context.Background(), options); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(options.ReportDirectory, "reviewer-learning.json")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				testCase.change(t, &options)
				options.Resume = true
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("resume must reject incompatible input without panicking: %v", recovered)
					}
				}()
				_, err = RunReviewerLearningBenchmark(context.Background(), options)
				if err == nil || !strings.Contains(err.Error(), "different report directory") {
					t.Fatalf("changed evidence inputs must reject resume with fresh-directory guidance, got %v", err)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("rejected resume overwrote existing evidence")
				}
			})
		}
	}
}

func TestReviewerLearningResumeRejectsMissingInputIdentity(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		t.Run(string(mode), func(t *testing.T) {
			options := reviewerLearningResumeFixture(t, mode)
			if _, err := RunReviewerLearningBenchmark(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(options.ReportDirectory, "reviewer-learning.json")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var report map[string]any
			if err := json.Unmarshal(content, &report); err != nil {
				t.Fatal(err)
			}
			delete(report, "input_identity")
			content, err = json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
			options.Resume = true
			if _, err := RunReviewerLearningBenchmark(context.Background(), options); err == nil || !strings.Contains(err.Error(), "different report directory") {
				t.Fatalf("legacy report without input identity must reject resume, got %v", err)
			}
		})
	}
}

func TestReviewerLearningResumeRecoversFetchFailureWithoutReplacingLaterEvidence(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		t.Run(string(mode), func(t *testing.T) {
			options := reviewerLearningResumeFixture(t, mode)
			missingPath := filepath.Join(options.FixtureDirectory, "b.html")
			if err := os.Remove(missingPath); err != nil {
				t.Fatal(err)
			}
			initial, err := RunReviewerLearningBenchmark(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			if initial.FetchFailedPages != 1 || initial.RunStatus != ReviewerLearningRunStatusIncompleteFetchFailed {
				t.Fatalf("expected incomplete fetch coverage, got %#v", initial)
			}
			laterEvidence, err := json.Marshal(initial.Pages[2])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(missingPath, []byte(`<main><h1>Title b</h1></main>`), 0o644); err != nil {
				t.Fatal(err)
			}
			options.Resume = true
			resumed, err := RunReviewerLearningBenchmark(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.RunStatus != ReviewerLearningRunStatusCompleted || resumed.FetchFailedPages != 0 || len(resumed.MissingURLs) != 0 || resumed.DiscoveredURLs != 3 || len(resumed.Pages) != 3 || !resumed.ProofUsable {
				t.Fatalf("resume must retry recovered fetch and rebuild full-coverage counters, got status=%s failed=%d missing=%v discovered=%d pages=%d proof=%t", resumed.RunStatus, resumed.FetchFailedPages, resumed.MissingURLs, resumed.DiscoveredURLs, len(resumed.Pages), resumed.ProofUsable)
			}
			if resumed.Pages[1].FinalApprovalMethod != ReviewerLearningApprovalFirstPass || resumed.Pages[1].FirstPass.Metrics.RelevantTextF1 != 1 {
				t.Fatalf("recovered page was not extracted: %#v", resumed.Pages[1])
			}
			laterAfter, err := json.Marshal(resumed.Pages[2])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(laterEvidence, laterAfter) {
				t.Fatal("resume discarded or reran valid later evidence")
			}
		})
	}
}

func TestReviewerLearningDeepResumeBindsOpenAIModelConfiguration(t *testing.T) {
	for _, field := range []string{"endpoint", "model", "max tokens", "provider"} {
		t.Run(field, func(t *testing.T) {
			options := reviewerLearningResumeFixture(t, ReviewerLearningModeDeep)
			if err := os.WriteFile(options.MappingPath, []byte(`{"urls":[{"url":"https://example.test/topic/a/","identifier":[]}]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			config := OpenAIConfig{BaseURL: "http://127.0.0.1:1", Model: "first-model", MaxTokens: 1024}
			options.ModelMode = "openai"
			options.Model = NewOpenAIClient(config)
			if _, err := RunReviewerLearningBenchmark(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "endpoint":
				config.BaseURL = "http://127.0.0.1:2"
			case "model":
				config.Model = "second-model"
			case "max tokens":
				config.MaxTokens = 2048
			case "provider":
				config.Provider = "openai"
			}
			options.Model = NewOpenAIClient(config)
			options.Resume = true
			if _, err := RunReviewerLearningBenchmark(context.Background(), options); err == nil {
				t.Fatal("different OpenAI extraction configuration must reject resume")
			}
		})
	}
}

func TestReviewerLearningDeepFetchRecoveryModelFailurePreservesLaterCheckpoint(t *testing.T) {
	options := reviewerLearningResumeFixture(t, ReviewerLearningModeDeep)
	options.Model = &usageModel{}
	missingPath := filepath.Join(options.FixtureDirectory, "b.html")
	if err := os.Remove(missingPath); err != nil {
		t.Fatal(err)
	}
	initial, err := RunReviewerLearningBenchmark(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	laterEvidence, err := json.Marshal(initial.Pages[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(missingPath, []byte(`<main><h1>Title b</h1></main>`), 0o644); err != nil {
		t.Fatal(err)
	}
	options.Resume = true
	options.Model = &failFirstExtractModel{}
	if _, err := RunReviewerLearningBenchmark(context.Background(), options); err == nil {
		t.Fatal("expected extraction failure during recovered fetch")
	}
	options.Model = &FixtureModel{}
	resumed, err := RunReviewerLearningBenchmark(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	laterAfter, err := json.Marshal(resumed.Pages[2])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(laterEvidence, laterAfter) {
		t.Fatal("retrying a failed recovery discarded and reran valid later evidence")
	}
}

func TestReviewerLearningDeepChunkDoesNotPauseAfterLastFetchAttempt(t *testing.T) {
	options := reviewerLearningResumeFixture(t, ReviewerLearningModeDeep)
	options.StartPage = 3
	options.ChunkPages = 1
	if err := os.Remove(filepath.Join(options.FixtureDirectory, "c.html")); err != nil {
		t.Fatal(err)
	}
	report, err := RunReviewerLearningBenchmark(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.RunStatus != ReviewerLearningRunStatusIncompleteFetchFailed {
		t.Fatalf("last fetch failure should finish incomplete instead of advertising an unprocessed chunk: %s", report.RunStatus)
	}
}

func TestReviewerLearningFreshRunPreservesExistingReportEvidence(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		for _, reportContent := range []struct {
			name string
			json string
		}{
			{"truncated JSON", `{"fixture_set":"interrupted`},
			{"empty report", `{}`},
		} {
			t.Run(string(mode)+"/"+reportContent.name, func(t *testing.T) {
				options := reviewerLearningResumeFixture(t, mode)
				if _, err := RunReviewerLearningBenchmark(context.Background(), options); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(options.ReportDirectory, "reviewer-learning.json")
				if err := os.WriteFile(path, []byte(reportContent.json), 0o644); err != nil {
					t.Fatal(err)
				}
				before := snapshotReviewerLearningEvidence(t, options.ReportDirectory)
				if _, err := RunReviewerLearningBenchmark(context.Background(), options); err == nil {
					t.Fatal("fresh run must reject an existing report without explicit force")
				}
				after := snapshotReviewerLearningEvidence(t, options.ReportDirectory)
				if len(before) != len(after) {
					t.Fatal("rejected fresh run changed the evidence file set")
				}
				for name, content := range before {
					if !bytes.Equal(content, after[name]) {
						t.Fatalf("rejected fresh run overwrote %s", name)
					}
				}
			})
		}
	}
}

func snapshotReviewerLearningEvidence(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		snapshot[entry.Name()] = content
	}
	return snapshot
}

func TestReviewerLearningFailedAtomicCheckpointPreservesCanonicalReport(t *testing.T) {
	directory := t.TempDir()
	original := ReviewerLearningReport{FixtureSet: "original-evidence", RunStatus: ReviewerLearningRunStatusCompleted}
	if err := WriteReviewerLearningReports(directory, original); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "reviewer-learning.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0o700)
	if probe, err := os.CreateTemp(directory, "permission-probe-"); err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("directory permissions do not prevent file creation on this platform")
	}
	updated := ReviewerLearningReport{FixtureSet: "replacement-evidence", RunStatus: ReviewerLearningRunStatusCompleted}
	if err := WriteReviewerLearningReports(directory, updated); err == nil {
		t.Error("checkpoint replacement must fail when a temporary sibling cannot be created")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed checkpoint replacement truncated or changed the previous canonical report")
	}
}
