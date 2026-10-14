package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"semantic-crawler/internal/semantic"
)

func liveTestConfig(t *testing.T, seed string) string {
	t.Helper()
	directory := t.TempDir()
	skill := filepath.Join(directory, "source.md")
	if err := os.WriteFile(skill, []byte("Keep source paragraphs."), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(seed)
	if err != nil {
		t.Fatal(err)
	}
	config := semantic.SourceConfig{SourceID: 1, Name: "Synthetic local source", SeedURL: seed, AllowedHost: parsed.Hostname(), AllowedPathPrefix: "/", Scope: "source content", MaxPages: 1, SkillVersion: 1, SourceSkillPath: skill}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "source.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func runTestCLI(t *testing.T, config string, flags ...string) ([]byte, error) {
	t.Helper()
	args := []string{"run", "./cmd/semantic-crawler", "-action", "run", "-source-config", config, "-db", filepath.Join(t.TempDir(), "review.db"), "-mode", "live", "-model-mode", "fixture"}
	command := exec.Command("go", append(args, flags...)...)
	command.Dir = filepath.Join("..", "..")
	return command.CombinedOutput()
}

func TestCLICrawlAppliesFetchDelayAndRetries(t *testing.T) {
	var mu sync.Mutex
	var requests []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests = append(requests, time.Now())
		count := len(requests)
		mu.Unlock()
		if count == 1 {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "<main><p>Source content</p></main>")
	}))
	defer server.Close()
	output, err := runTestCLI(t, liveTestConfig(t, server.URL), "-fetch-delay", "80ms", "-fetch-retries", "1", "-fetch-retry-delay", "1ms")
	if err != nil {
		t.Fatalf("crawl did not recover: %v\n%s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[1].Sub(requests[0]) < 80*time.Millisecond {
		t.Fatalf("fetch controls were ignored: request times=%v", requests)
	}
}

func TestCLICrawlFailsWhenPagesCouldNotBeProcessed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	output, err := runTestCLI(t, liveTestConfig(t, server.URL), "-fetch-retries", "0")
	if err == nil || !strings.Contains(string(output), `"Failed": 1`) {
		t.Fatalf("failed crawl reported success: %v\n%s", err, output)
	}
}

func TestCLICrawlRejectsUnknownModeBeforeFetching(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "<main><p>Source content</p></main>")
	}))
	defer server.Close()
	output, err := runTestCLI(t, liveTestConfig(t, server.URL), "-mode", "snapshots")
	if err == nil || !strings.Contains(string(output), "mode") || requests.Load() != 0 {
		t.Fatalf("invalid mode must fail before network access: err=%v requests=%d output=%s", err, requests.Load(), output)
	}
}
