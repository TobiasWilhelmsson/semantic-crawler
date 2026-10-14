package semantic

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewerLearningReportResumeRejectsInvalidCanonical(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		for _, invalid := range []string{"null", "{}", "[]", `"report"`, `{"unrelated":true}`} {
			t.Run(string(mode)+"/"+invalid, func(t *testing.T) {
				options := reviewerLearningResumeFixture(t, mode)
				options.Resume = true
				if err := os.MkdirAll(options.ReportDirectory, 0755); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(options.ReportDirectory, "reviewer-learning.json")
				if err := os.WriteFile(path, []byte(invalid), 0644); err != nil {
					t.Fatal(err)
				}
				report, err := RunReviewerLearningBenchmark(context.Background(), options)
				after, _ := os.ReadFile(path)
				if err == nil || string(after) != invalid {
					t.Fatalf("invalid canonical resumed: err=%v status=%q pages=%d canonical replaced=%t", err, report.RunStatus, len(report.Pages), string(after) != invalid)
				}
			})
		}
	}
}

func TestReviewerLearningReportRepairRejectsActiveWriter(t *testing.T) {
	options := reviewerLearningResumeFixture(t, ReviewerLearningModeDeep)
	if _, err := RunReviewerLearningBenchmark(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(options.ReportDirectory, "reviewer-learning.json"))
	lock, err := acquireReviewerLearningLock(options.ReportDirectory, "active-writer", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseReviewerLearningLock(options.ReportDirectory, lock)
	_, err = RepairReviewerLearningReport(options.ReportDirectory)
	after, _ := os.ReadFile(filepath.Join(options.ReportDirectory, "reviewer-learning.json"))
	if err == nil {
		t.Fatalf("repair bypassed active-writer lock and rewrote checkpoint=%t", string(before) != string(after))
	}
}

func TestReviewerLearningReportEmptyBaselineCannotBeUsableProof(t *testing.T) {
	options := reviewerLearningResumeFixture(t, ReviewerLearningModeDeep)
	if err := os.WriteFile(options.MappingPath, []byte(`{"urls":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := RunReviewerLearningBenchmark(context.Background(), options)
	if err == nil && report.ProofUsable {
		t.Fatalf("zero-page baseline produced usable proof: expected=%d pages=%d status=%q", report.ExpectedURLs, len(report.Pages), report.RunStatus)
	}
}

func TestReviewerLearningReportAdaptiveRejectsActiveWriter(t *testing.T) {
	options := reviewerLearningResumeFixture(t, ReviewerLearningModeAdaptive)
	lock, err := acquireReviewerLearningLock(options.ReportDirectory, "active-writer", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseReviewerLearningLock(options.ReportDirectory, lock)
	report, err := RunReviewerLearningBenchmark(context.Background(), options)
	if err == nil {
		t.Fatalf("adaptive bypassed active-writer lock: completed=%d status=%q", len(report.Pages), report.RunStatus)
	}
}

func TestReviewerLearningReportForceCannotTakeActiveLock(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			directory := t.TempDir()
			owner, err := acquireReviewerLearningLock(directory, "owner", time.Hour, false)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseReviewerLearningLock(directory, owner)
			if _, err := acquireReviewerLearningLock(directory, "successor", time.Nanosecond, force); err == nil {
				t.Fatal("active writer lock was taken over")
			}
		})
	}
}

func TestReviewerLearningReportFormerOwnerCannotRefreshOrReleaseSuccessor(t *testing.T) {
	directory := t.TempDir()
	former, err := acquireReviewerLearningLock(directory, "former", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	releaseReviewerLearningLock(directory, former)
	successor, err := acquireReviewerLearningLock(directory, "successor", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseReviewerLearningLock(directory, successor)
	if err := refreshReviewerLearningLock(directory, former); err == nil {
		t.Fatal("released owner refreshed successor lock")
	}
	releaseReviewerLearningLock(directory, former)
	current, err := readReviewerLearningLock(directory)
	if err != nil || current.RunID != successor.RunID {
		t.Fatalf("successor lock lost: current=%q err=%v", current.RunID, err)
	}
}

func TestReviewerLearningReportDirectCheckpointRejectsActiveWriter(t *testing.T) {
	directory := t.TempDir()
	owner, err := acquireReviewerLearningLock(directory, "owner", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseReviewerLearningLock(directory, owner)
	if err := WriteReviewerLearningReports(directory, ReviewerLearningReport{RunStatus: ReviewerLearningRunStatusCompleted}); err == nil {
		t.Fatal("checkpoint bypassed active writer lock")
	}
}

func TestReviewerLearningReportLockAcrossProcesses(t *testing.T) {
	if directory := os.Getenv("SEMANTIC_TEST_REPORT_LOCK_DIRECTORY"); directory != "" {
		lock, err := acquireReviewerLearningLock(directory, "child-writer", time.Hour, false)
		if err != nil {
			t.Fatal(err)
		}
		defer releaseReviewerLearningLock(directory, lock)
		fmt.Fprintln(os.Stdout, "lock-held")
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		return
	}
	directory := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestReviewerLearningReportLockAcrossProcesses$")
	command.Env = append(os.Environ(), "SEMANTIC_TEST_REPORT_LOCK_DIRECTORY="+directory)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "lock-held" {
		t.Fatal("child did not acquire lock")
	}
	for _, force := range []bool{false, true} {
		if _, err := acquireReviewerLearningLock(directory, "parent-writer", time.Nanosecond, force); err == nil {
			t.Errorf("cross-process lock takeover succeeded: force=%t", force)
		}
	}
	if _, err := stdin.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	recovered, err := acquireReviewerLearningLock(directory, "recovered", time.Hour, false)
	if err != nil {
		t.Fatalf("finished child lock was not released: %v", err)
	}
	releaseReviewerLearningLock(directory, recovered)
}

func TestReviewerLearningReportCanceledRunReleasesLock(t *testing.T) {
	for _, mode := range []ReviewerLearningMode{ReviewerLearningModeDeep, ReviewerLearningModeAdaptive} {
		t.Run(string(mode), func(t *testing.T) {
			options := reviewerLearningResumeFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := RunReviewerLearningBenchmark(ctx, options); err == nil {
				t.Fatal("canceled run continued through the benchmark")
			}
			lock, err := acquireReviewerLearningLock(options.ReportDirectory, "after-cancel", time.Hour, false)
			if err != nil {
				t.Fatalf("canceled run retained writer lock: %v", err)
			}
			releaseReviewerLearningLock(options.ReportDirectory, lock)
		})
	}
}

func TestReviewerLearningReportModelErrorReleasesLock(t *testing.T) {
	options := reviewerLearningResumeFixture(t, ReviewerLearningModeDeep)
	options.Model = &failFirstExtractModel{}
	if _, err := RunReviewerLearningBenchmark(context.Background(), options); err == nil {
		t.Fatal("expected model error")
	}
	lock, err := acquireReviewerLearningLock(options.ReportDirectory, "after-error", time.Hour, false)
	if err != nil {
		t.Fatalf("failed run retained writer lock: %v", err)
	}
	releaseReviewerLearningLock(options.ReportDirectory, lock)
}

func TestReviewerLearningReportForceRecoversMalformedLockMetadata(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(reviewerLearningLockPath(directory), []byte(`{"run_id":`), 0644); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireReviewerLearningLock(directory, "recovered", time.Hour, true)
	if err != nil {
		t.Fatalf("force could not recover abandoned malformed metadata: %v", err)
	}
	defer releaseReviewerLearningLock(directory, lock)
	current, err := readReviewerLearningLock(directory)
	if err != nil || current.RunID != lock.RunID {
		t.Fatalf("recovered metadata invalid: owner=%q err=%v", current.RunID, err)
	}
}

func TestReviewerLearningReportFreshAbandonedMetadataRecovers(t *testing.T) {
	options := reviewerLearningResumeFixture(t, ReviewerLearningModeDeep)
	if _, err := RunReviewerLearningBenchmark(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	abandoned, err := acquireReviewerLearningLock(options.ReportDirectory, "abandoned", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := abandoned.file.Close(); err != nil {
		t.Fatal(err)
	}
	options.Resume = true
	resumed, err := RunReviewerLearningBenchmark(context.Background(), options)
	if err != nil {
		t.Fatalf("fresh abandoned metadata prevented normal resume: %v", err)
	}
	if resumed.RunStatus != ReviewerLearningRunStatusCompleted || len(resumed.Pages) != 3 {
		t.Fatalf("normal resume did not preserve complete report: %#v", resumed)
	}
	if _, err := readReviewerLearningLock(options.ReportDirectory); !os.IsNotExist(err) {
		t.Fatalf("resumed run retained lock metadata: %v", err)
	}
}

func TestReviewerLearningReportStatusUsesOSLock(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		t.Run(fmt.Sprint(canonical), func(t *testing.T) {
			directory := t.TempDir()
			if canonical {
				if err := WriteReviewerLearningReports(directory, ReviewerLearningReport{FixtureSet: "status-evidence", RunStatus: ReviewerLearningRunStatusRunning}); err != nil {
					t.Fatal(err)
				}
			}
			owner, err := acquireReviewerLearningLock(directory, "active", time.Hour, false)
			if err != nil {
				t.Fatal(err)
			}
			active, err := LoadReviewerLearningStatus(directory)
			if err != nil || !active.Locked || active.RunStatus != ReviewerLearningRunStatusRunning {
				t.Fatalf("held OS lock not reflected in status: %#v err=%v", active, err)
			}
			if err := owner.file.Close(); err != nil {
				t.Fatal(err)
			}
			abandoned, err := LoadReviewerLearningStatus(directory)
			if err != nil || abandoned.Locked || abandoned.RunStatus == ReviewerLearningRunStatusRunning {
				t.Fatalf("abandoned metadata presented active writer: %#v err=%v", abandoned, err)
			}
			if canonical && abandoned.RunStatus != ReviewerLearningRunStatusInterrupted {
				t.Fatalf("abandoned running report was not interrupted: %#v", abandoned)
			}
		})
	}
}

func TestReviewerLearningReportStatusSurfacesLockProbeError(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(reviewerLearningWriterLockPath(directory), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReviewerLearningStatus(directory); err == nil {
		t.Fatal("lock probe error was treated as an unlocked report")
	}
}
