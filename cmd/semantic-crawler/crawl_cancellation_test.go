package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"semantic-crawler/internal/models"
)

func TestCLIInterruptFinalizesCrawlRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal does not support interrupt delivery on Windows")
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	directory := t.TempDir()
	binary := filepath.Join(directory, "semantic-crawler")
	build := exec.Command("go", "build", "-o", binary, "./cmd/semantic-crawler")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	database := filepath.Join(directory, "review.db")
	command := exec.Command(binary, "-action", "run", "-source-config", liveTestConfig(t, server.URL), "-db", database, "-mode", "live", "-model-mode", "fixture")
	command.Dir = filepath.Join("..", "..")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("crawl did not reach source fetch")
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		waited = true
		if err == nil {
			t.Fatal("interrupted crawl reported success")
		}
	case <-time.After(7 * time.Second):
		_ = command.Process.Kill()
		<-done
		waited = true
		t.Fatal("interrupted crawl did not stop")
	}
	db, err := gorm.Open(sqlite.Open(database), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var run models.SemanticCrawlRun
	if err := db.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" || run.CompletedAt == nil {
		t.Fatalf("interrupted run was left unfinished: status=%s completed=%v", run.Status, run.CompletedAt)
	}
	var queued models.SemanticQueueItem
	if err := db.First(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if queued.Status != "failed" {
		t.Fatalf("interrupted source fetch was left %s", queued.Status)
	}
}
