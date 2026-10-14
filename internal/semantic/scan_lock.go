package semantic

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type ScanLock struct {
	file *os.File
	once sync.Once
	err  error
}

func canonicalScanPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if info, statErr := os.Lstat(absolute); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(absolute)
		if readErr != nil {
			return "", readErr
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(absolute), target)
		}
		return canonicalScanPath(target)
	}
	parent := filepath.Dir(absolute)
	var suffix []string
	for {
		resolved, err = filepath.EvalSymlinks(parent)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Join(resolved, filepath.Base(absolute)), nil
		}
		if !errors.Is(err, os.ErrNotExist) || parent == filepath.Dir(parent) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
}
func AcquireScanLock(path string) (*ScanLock, error) {
	identity, err := canonicalScanPath(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(identity), 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(identity+".scan.writer.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockReviewerLearningFile(file); err != nil {
		file.Close()
		if reviewerLearningFileLockContended(err) {
			return nil, ErrScanBusy
		}
		return nil, fmt.Errorf("lock scan database: %w", err)
	}
	return &ScanLock{file: file}, nil
}
func (lease *ScanLock) Close() error {
	lease.once.Do(func() { lease.err = lease.file.Close() })
	return lease.err
}
