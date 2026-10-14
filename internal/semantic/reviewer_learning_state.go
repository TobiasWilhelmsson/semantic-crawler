package semantic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ReviewerLearningLock struct {
	RunID     string    `json:"run_id"`
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	file      *os.File
}

func reviewerLearningLockPath(directory string) string {
	return filepath.Join(directory, "reviewer-learning.lock.json")
}

func writeReviewerLearningLock(directory string, lock ReviewerLearningLock) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".reviewer-learning-lock-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), reviewerLearningLockPath(directory))
}

func readReviewerLearningLock(directory string) (ReviewerLearningLock, error) {
	content, err := os.ReadFile(reviewerLearningLockPath(directory))
	if err != nil {
		return ReviewerLearningLock{}, err
	}
	var lock ReviewerLearningLock
	if err := json.Unmarshal(content, &lock); err != nil {
		return ReviewerLearningLock{}, err
	}
	return lock, nil
}

func reviewerLearningWriterLockPath(directory string) string {
	return filepath.Join(directory, ".reviewer-learning.writer.lock")
}

func acquireReviewerLearningLock(directory, runID string, _ time.Duration, force bool) (ReviewerLearningLock, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return ReviewerLearningLock{}, err
	}
	file, err := os.OpenFile(reviewerLearningWriterLockPath(directory), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return ReviewerLearningLock{}, err
	}
	if err := lockReviewerLearningFile(file); err != nil {
		file.Close()
		return ReviewerLearningLock{}, fmt.Errorf("reviewer-learning report is locked by another writer: %w", err)
	}
	acquired := false
	defer func() {
		if !acquired {
			file.Close()
		}
	}()
	now := time.Now().UTC()
	_, readErr := readReviewerLearningLock(directory)
	if readErr != nil && !os.IsNotExist(readErr) && !force {
		return ReviewerLearningLock{}, readErr
	}
	host, _ := os.Hostname()
	lock := ReviewerLearningLock{RunID: runID, PID: os.Getpid(), Host: host, Command: stringsJoin(os.Args, " "), StartedAt: now, UpdatedAt: now, file: file}
	if err := writeReviewerLearningLock(directory, lock); err != nil {
		return ReviewerLearningLock{}, err
	}
	acquired = true
	return lock, nil
}

func verifyReviewerLearningLock(directory string, lock ReviewerLearningLock) error {
	if lock.file == nil || lock.file.Name() != reviewerLearningWriterLockPath(directory) {
		return fmt.Errorf("reviewer-learning writer lock is not held")
	}
	if _, err := lock.file.Stat(); err != nil {
		return fmt.Errorf("reviewer-learning writer lock is no longer held: %w", err)
	}
	current, err := readReviewerLearningLock(directory)
	if err != nil {
		return fmt.Errorf("read reviewer-learning lock ownership: %w", err)
	}
	if current.RunID != lock.RunID {
		return fmt.Errorf("reviewer-learning lock ownership changed from run %s to %s", lock.RunID, current.RunID)
	}
	return nil
}

func refreshReviewerLearningLock(directory string, lock ReviewerLearningLock) error {
	if err := verifyReviewerLearningLock(directory, lock); err != nil {
		return err
	}
	lock.UpdatedAt = time.Now().UTC()
	return writeReviewerLearningLock(directory, lock)
}

func releaseReviewerLearningLock(directory string, lock ReviewerLearningLock) {
	if lock.file == nil {
		return
	}
	if err := verifyReviewerLearningLock(directory, lock); err == nil {
		_ = os.Remove(reviewerLearningLockPath(directory))
	}
	_ = lock.file.Close()
}

func reviewerLearningRunID() string {
	return fmt.Sprintf("rl-%d-%d", time.Now().UTC().UnixNano(), os.Getpid())
}

func stringsJoin(values []string, sep string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += sep
		}
		result += value
	}
	return result
}

func reviewerLearningWriterActive(directory string) (bool, error) {
	file, err := os.OpenFile(reviewerLearningWriterLockPath(directory), os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open reviewer-learning writer lock for status: %w", err)
	}
	defer file.Close()
	if err := lockReviewerLearningFile(file); err != nil {
		if reviewerLearningFileLockContended(err) {
			return true, nil
		}
		return false, fmt.Errorf("probe reviewer-learning writer lock: %w", err)
	}
	return false, nil
}
