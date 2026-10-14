package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"semantic-crawler/internal/semantic"
)

func TestWebsiteCLIManagedScanReuseAndBusyPort(t *testing.T) {
	var modelCalls, outsideCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if !strings.HasPrefix(r.URL.Path, "/docs/") {
				outsideCalls.Add(1)
			}
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<main><h1>Website protocol test</h1><p>Source content.</p><a href="/docs/next/">Next</a><a href="/outside/">Outside scope</a></main>`)
			return
		}
		modelCalls.Add(1)
		var request struct {
			ResponseFormat struct {
				JSONSchema struct {
					Name string `json:"name"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		content := `{"action":"extract","confidence":1,"reason":"Synthetic protocol test"}`
		if request.ResponseFormat.JSONSchema.Name != "semantic_route" {
			content = `{"page_kind":"article","blocks":[{"block_id":"b0001","include":true,"role":"header","type":"none","reason":"Source heading"}],"uncertainties":[]}`
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
	}))
	defer provider.Close()
	project, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "semantic-crawler")
	build := exec.Command("go", "build", "-o", binary, "./cmd/semantic-crawler")
	build.Dir = project
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	sourceRoot := filepath.Join(directory, "sources")
	t.Setenv("SEMANTIC_LLM_PROVIDER", "custom")
	t.Setenv("SEMANTIC_LLM_BASE_URL", provider.URL+"/v1")
	t.Setenv("SEMANTIC_LLM_API_KEY", "synthetic-local-key")
	t.Setenv("SEMANTIC_LLM_MODEL", "website-protocol-model")
	t.Setenv("SEMANTIC_LLM_RETRIES", "0")
	client := &http.Client{Timeout: 2 * time.Second}
	get := func(url string) (int, []byte, error) {
		r, err := client.Get(url)
		if err != nil {
			return 0, nil, err
		}
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		return r.StatusCode, body, err
	}
	source := semantic.SourceEntry{}
	var savedInstructions []byte
	for cycle := 1; cycle <= 3; cycle++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		logfile, err := os.Create(filepath.Join(directory, "cycle-"+strconv.Itoa(cycle)+".log"))
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(binary, "-action", "crawl-site", "-website", provider.URL+"/docs/", "-source-config-root", sourceRoot, "-scan-pages", "10", "-scan-depth", "2", "-fetch-delay", "1ms", "-addr", address)
		command.Dir = directory
		command.Stdout = logfile
		command.Stderr = logfile
		if err = command.Start(); err != nil {
			logfile.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if command.ProcessState == nil {
				command.Process.Kill()
				command.Wait()
			}
			logfile.Close()
		})
		base := "http://" + address
		deadline := time.Now().Add(8 * time.Second)
		for {
			status, _, err := get(base + "/semantic")
			if err == nil && status == 200 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("website UI did not start")
			}
			time.Sleep(20 * time.Millisecond)
		}
		entries, err := semantic.NewSourceRegistry(sourceRoot).List()
		if err != nil || len(entries) != 1 {
			t.Fatalf("source was duplicated: %#v %v", entries, err)
		}
		if cycle == 2 && (entries[0].Slug != source.Slug || entries[0].Config.SourceID != source.Config.SourceID) {
			t.Fatal("source identity changed on repeat")
		}
		source = entries[0]
		deadline = time.Now().Add(8 * time.Second)
		var progress semantic.ScanProgress
		for {
			status, body, err := get(base + "/semantic/sources/" + source.Slug + "/scans/" + strconv.Itoa(cycle) + "/status")
			if err != nil || status != 200 {
				t.Fatalf("scan status: %d %v %s", status, err, body)
			}
			if err = json.Unmarshal(body, &progress); err != nil {
				t.Fatal(err)
			}
			if !progress.State.Active() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("website scan did not finish")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if progress.State != semantic.ScanCompleted || progress.Discovered != 2 {
			t.Fatalf("website result: %#v", progress)
		}
		if cycle == 1 && (progress.Candidates != 2 || modelCalls.Load() != 4) {
			t.Fatalf("first extraction: %#v calls=%d", progress, modelCalls.Load())
		}
		if cycle == 2 && (progress.Unchanged != 0 || progress.Candidates != 2 || modelCalls.Load() != 8) {
			t.Fatalf("changed instructions did not trigger extraction: %#v calls=%d", progress, modelCalls.Load())
		}
		if cycle == 3 && (progress.Unchanged != 2 || progress.Candidates != 0 || modelCalls.Load() != 8) {
			t.Fatalf("equivalent extraction was not cached: %#v calls=%d", progress, modelCalls.Load())
		}
		if err = command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if err = command.Wait(); err != nil {
			t.Fatal(err)
		}
		logfile.Close()
		if cycle == 1 {
			savedInstructions = []byte("# Custom website instructions\nKeep source warning tables.\n")
			if err = os.WriteFile(source.Config.SourceSkillPath, savedInstructions, 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			data, _ := os.ReadFile(source.Config.SourceSkillPath)
			if string(data) != string(savedInstructions) {
				t.Fatal("shortcut overwrote source instructions")
			}
		}
	}
	if outsideCalls.Load() != 0 {
		t.Fatalf("fetched outside source path: %d", outsideCalls.Load())
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	command := exec.Command(binary, "-action", "crawl-site", "-website", provider.URL+"/docs/", "-source-config-root", sourceRoot, "-addr", listener.Addr().String())
	command.Dir = directory
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("busy UI port succeeded: %s", output)
	}
	if modelCalls.Load() != 8 {
		t.Fatal("busy port made model requests")
	}
	store, err := semantic.OpenReviewStore(context.Background(), filepath.Join(directory, "data", "semantic-"+source.Slug+".db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runs, err := store.ScanRuns(context.Background(), source.Config.SourceID, 100)
	if err != nil || len(runs) != 3 || runs[0].ModelMode != "openai" {
		t.Fatalf("website scan history: %#v %v", runs, err)
	}
}
