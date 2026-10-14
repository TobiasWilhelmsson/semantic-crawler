//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package semantic

import (
	"errors"
	"os"
	"syscall"
)

func lockReviewerLearningFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func reviewerLearningFileLockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}
