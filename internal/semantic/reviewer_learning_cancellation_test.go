package semantic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type cancelingReviewerLearningModel struct {
	cancel context.CancelFunc
	fail   bool
}

func (model *cancelingReviewerLearningModel) Route(ctx context.Context, prompt string) (RouteResponse, ModelUsage, error) {
	return (&FixtureModel{}).Route(ctx, prompt)
}
func (model *cancelingReviewerLearningModel) Extract(ctx context.Context, prompt string, blocks []CandidateBlock) (ExtractionResponse, ModelUsage, error) {
	response, usage, err := (&FixtureModel{}).Extract(ctx, prompt, blocks)
	model.cancel()
	if model.fail {
		err = ctx.Err()
	}
	return response, usage, err
}

type reviewerLearningProgressFunc func([]byte) (int, error)

func (writer reviewerLearningProgressFunc) Write(data []byte) (int, error) { return writer(data) }

func assertInterruptedReviewerLearningCheckpoint(t *testing.T, options ReviewerLearningOptions, report ReviewerLearningReport, err error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was swallowed or replaced: err=%v status=%q pages=%d", err, report.RunStatus, len(report.Pages))
	}
	if report.RunStatus != ReviewerLearningRunStatusInterrupted || !report.CompletedAt.IsZero() || report.ProofUsable {
		t.Fatalf("canceled report was not interrupted: status=%q completed=%v proof=%t", report.RunStatus, report.CompletedAt, report.ProofUsable)
	}
	saved, loadErr := LoadReviewerLearningReport(options.ReportDirectory)
	if loadErr != nil || saved.RunStatus != ReviewerLearningRunStatusInterrupted {
		t.Fatalf("recoverable interrupted checkpoint missing: status=%q err=%v", saved.RunStatus, loadErr)
	}
	status, statusErr := LoadReviewerLearningStatus(options.ReportDirectory)
	if statusErr != nil || status.Locked {
		t.Fatalf("canceled run retained writer lock: locked=%t err=%v", status.Locked, statusErr)
	}
}

func TestReviewerLearningCancellationAfterFinalFetch(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		for _, fail := range []bool{false, true} {
			name := string(mode) + "/success"
			if fail {
				name = string(mode) + "/error"
			}
			t.Run(name, func(t *testing.T) {
				options := reviewerLearningResumeFixture(t, mode)
				options.MaxPages = 1
				options.LiveFetch = true
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				options.Fetcher = crawlRecoveryFetcher(func(ctx context.Context, url string) (FetchResult, error) {
					cancel()
					if fail {
						return FetchResult{}, ctx.Err()
					}
					return FetchResult{URL: url, StatusCode: 200, HTML: "<main><h1>Title a</h1></main>"}, nil
				})
				report, err := RunReviewerLearningBenchmark(ctx, options)
				assertInterruptedReviewerLearningCheckpoint(t, options, report, err)
				options.Resume = true
				options.Fetcher = crawlRecoveryFetcher(func(_ context.Context, url string) (FetchResult, error) {
					return FetchResult{URL: url, StatusCode: 200, HTML: "<main><h1>Title a</h1></main>"}, nil
				})
				resumed, resumeErr := RunReviewerLearningBenchmark(context.Background(), options)
				if resumeErr != nil || resumed.RunStatus != ReviewerLearningRunStatusCompleted || len(resumed.Pages) != 1 || !reviewerLearningPageComplete(resumed.Pages[0]) {
					t.Fatalf("canceled fetch did not resume: status=%q pages=%d err=%v", resumed.RunStatus, len(resumed.Pages), resumeErr)
				}
			})
		}
	}
}

func TestReviewerLearningCancellationAfterFinalModelCall(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		for _, fail := range []bool{false, true} {
			name := string(mode) + "/success"
			if fail {
				name = string(mode) + "/error"
			}
			t.Run(name, func(t *testing.T) {
				options := reviewerLearningResumeFixture(t, mode)
				options.MaxPages = 1
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				options.Model = &cancelingReviewerLearningModel{cancel: cancel, fail: fail}
				report, err := RunReviewerLearningBenchmark(ctx, options)
				assertInterruptedReviewerLearningCheckpoint(t, options, report, err)
				options.Resume = true
				options.Model = &FixtureModel{}
				resumed, resumeErr := RunReviewerLearningBenchmark(context.Background(), options)
				if resumeErr != nil || resumed.RunStatus != ReviewerLearningRunStatusCompleted || len(resumed.Pages) != 1 {
					t.Fatalf("canceled model call did not resume: status=%q pages=%d err=%v", resumed.RunStatus, len(resumed.Pages), resumeErr)
				}
			})
		}
	}
}

func TestReviewerLearningCancellationBeforeSuccessExits(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		for _, exit := range []string{"completed", "adaptive", "chunk"} {
			if mode == ReviewerLearningModeAdaptive && exit == "chunk" {
				continue
			}
			t.Run(string(mode)+"/"+exit, func(t *testing.T) {
				options := reviewerLearningResumeFixture(t, mode)
				options.MaxPages = 1
				if exit == "adaptive" {
					options.AdaptiveStop = true
					options.MinPages = 1
					options.StableStreak = 1
				}
				if exit == "chunk" {
					options.MaxPages = 2
					options.ChunkPages = 1
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				options.ProgressWriter = reviewerLearningProgressFunc(func(data []byte) (int, error) { cancel(); return len(data), nil })
				report, err := RunReviewerLearningBenchmark(ctx, options)
				assertInterruptedReviewerLearningCheckpoint(t, options, report, err)
				saved, _ := LoadReviewerLearningReport(options.ReportDirectory)
				if len(saved.Pages) != 1 || !reviewerLearningPageComplete(saved.Pages[0]) {
					t.Fatal("cancellation discarded completed page evidence")
				}
			})
		}
	}
}

func TestReviewerLearningCancellationJoinsCheckpointError(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		t.Run(string(mode), func(t *testing.T) {
			options := reviewerLearningResumeFixture(t, mode)
			options.MaxPages = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			options.ProgressWriter = reviewerLearningProgressFunc(func(data []byte) (int, error) {
				cancel()
				path := filepath.Join(options.ReportDirectory, "reviewer-learning-status.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
				return len(data), nil
			})
			report, err := RunReviewerLearningBenchmark(ctx, options)
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "write reviewer learning status") || report.RunStatus != ReviewerLearningRunStatusInterrupted {
				t.Fatalf("checkpoint error replaced or lost cancellation: status=%q err=%v", report.RunStatus, err)
			}
		})
	}
}

func TestReviewerLearningCancellationPreservesLaterCompletedEvidence(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		t.Run(string(mode), func(t *testing.T) {
			options := reviewerLearningResumeFixture(t, mode)
			options.LiveFetch = true
			options.Fetcher = crawlRecoveryFetcher(func(_ context.Context, url string) (FetchResult, error) {
				slug := strings.TrimSuffix(url, "/")
				slug = slug[strings.LastIndex(slug, "/")+1:]
				return FetchResult{URL: url, StatusCode: 200, HTML: "<main><h1>Title " + slug + "</h1></main>"}, nil
			})
			original, err := RunReviewerLearningBenchmark(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			original.Pages[1] = ReviewerLearningPage{Index: 2, URL: original.Pages[1].URL, BaselineStatus: BaselineFetchFailed, FetchError: "temporary failure"}
			if err := WriteReviewerLearningReports(options.ReportDirectory, original); err != nil {
				t.Fatal(err)
			}
			original, err = LoadReviewerLearningReport(options.ReportDirectory)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			options.Resume = true
			options.Fetcher = crawlRecoveryFetcher(func(ctx context.Context, url string) (FetchResult, error) { cancel(); return FetchResult{}, ctx.Err() })
			report, err := RunReviewerLearningBenchmark(ctx, options)
			assertInterruptedReviewerLearningCheckpoint(t, options, report, err)
			saved, err := LoadReviewerLearningReport(options.ReportDirectory)
			if err != nil {
				t.Fatal(err)
			}
			if len(saved.Pages) != 3 || !reflect.DeepEqual(saved.Pages[0], original.Pages[0]) || !reflect.DeepEqual(saved.Pages[2], original.Pages[2]) {
				t.Fatalf("canceling an earlier recovery changed evidence: original0=%#v saved0=%#v original2=%#v saved2=%#v", original.Pages[0], saved.Pages[0], original.Pages[2], saved.Pages[2])
			}
		})
	}
}
