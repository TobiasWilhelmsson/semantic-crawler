//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package semantic

import (
	"fmt"
	"os"
	"runtime"
)

func lockReviewerLearningFile(_ *os.File) error {
	return fmt.Errorf("reviewer-learning file locking is unsupported on %s", runtime.GOOS)
}

func reviewerLearningFileLockContended(_ error) bool { return false }
