package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"semantic-crawler/internal/semantic"
)

func TestScanServerOfflineFlowAndSharedCLILock(t *testing.T) {
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
	config, err := semantic.LoadSourceConfigFile(filepath.Join(project, "configs/sources/generic-health-sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	config.SourceSkillPath = filepath.Join(project, config.SourceSkillPath)
	config.FixtureDirectory = filepath.Join(project, config.FixtureDirectory)
	sourceRoot := filepath.Join(directory, "sources")
	os.MkdirAll(sourceRoot, 0755)
	encoded, _ := json.Marshal(config)
	if err = os.WriteFile(filepath.Join(sourceRoot, "sample.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	dbPath := filepath.Join(directory, "sample.db")
	log, err := os.Create(filepath.Join(directory, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	command := exec.Command(binary, "-action", "serve", "-source", "sample", "-source-config-root", sourceRoot, "-db", dbPath, "-addr", address)
	command.Dir = directory
	command.Stdout = log
	command.Stderr = log
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	}()
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "http://" + address
	deadline := time.Now().Add(8 * time.Second)
	for {
		response, err := client.Get(base + "/semantic")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("scan server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	formResponse, err := client.Get(base + "/semantic/sources/sample/scans/new")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(formResponse.Body)
	formResponse.Body.Close()
	field := func(name string) string {
		match := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("missing %s: %s", name, body)
		}
		return string(match[1])
	}
	form := url.Values{"csrf_token": {field("csrf_token")}, "request_key": {field("request_key")}, "model_mode": {"fixture"}, "max_pages": {"10"}, "max_depth": {"2"}}
	post := func() string {
		response, err := client.PostForm(base+"/semantic/sources/sample/scans", form)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 303 {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("start: %d %s", response.StatusCode, body)
		}
		return response.Header.Get("Location")
	}
	path := post()
	if duplicate := post(); duplicate != path {
		t.Fatalf("duplicate run: %s vs %s", path, duplicate)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get(base + path + "/status")
		if err != nil {
			t.Fatal(err)
		}
		var status semantic.ScanProgress
		err = json.NewDecoder(response.Body).Decode(&status)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !status.State.Active() {
			if status.State != semantic.ScanCompleted || status.Discovered != 4 || status.Candidates != 3 {
				t.Fatalf("scan result %#v", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sample scan did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	lease, err := semantic.AcquireScanLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cli := exec.Command(binary, "-action", "start-demo", "-source", "sample", "-source-config-root", sourceRoot, "-db", dbPath)
	cli.Dir = directory
	output, err := cli.CombinedOutput()
	if err == nil || !strings.Contains(string(output), semantic.ErrScanBusy.Error()) {
		lease.Close()
		t.Fatalf("CLI bypassed shared lock: %v %s", err, output)
	}
	form.Set("request_key", "new-intent")
	response, err := client.PostForm(base+"/semantic/sources/sample/scans", form)
	if err != nil {
		lease.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 409 {
		lease.Close()
		t.Fatalf("UI bypassed external lock %d", response.StatusCode)
	}
	lease.Close()
	if err = command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err != nil {
		t.Fatalf("graceful shutdown: %v", err)
	}
	store, err := semantic.OpenReviewStore(context.Background(), dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runs, err := store.ScanRuns(context.Background(), config.SourceID, 100)
	if err != nil || len(runs) != 1 {
		t.Fatalf("unexpected durable runs: %#v %v", runs, err)
	}
}
