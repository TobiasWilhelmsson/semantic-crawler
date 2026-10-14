// Run from the root of an extracted source archive after make build.
// Only synthetic fixtures and the owned loopback server are used.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	if err := smoke(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func smoke() (result error) {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(os.Getenv("RELEASE_CHECK_WORK"), "semantic-smoke-")
	if err != nil {
		return err
	}
	defer func() {
		if result == nil {
			_ = os.RemoveAll(work)
			return
		}
		if log, err := os.Open(filepath.Join(work, "server.log")); err == nil {
			data, _ := io.ReadAll(io.LimitReader(log, 32<<10))
			_ = log.Close()
			if len(data) > 0 {
				result = fmt.Errorf("%w\nServer diagnostics:\n%s", result, data)
			}
		}
		result = fmt.Errorf("%w\nSmoke state retained at %s", result, work)
	}()
	emptyEnv := filepath.Join(work, "empty.env")
	if err := os.WriteFile(emptyEnv, nil, 0600); err != nil {
		return err
	}
	binary := filepath.Join(root, "bin", "semantic-crawler")
	base := []string{"-source", "generic-health-sample", "-db", filepath.Join(work, "sample.db"), "-env-file", emptyEnv}
	environment := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SEMANTIC_") && !strings.HasPrefix(entry, "SOURCE=") {
			environment = append(environment, entry)
		}
	}
	run := func(args ...string) ([]byte, error) {
		command := exec.Command(binary, append(base, args...)...)
		command.Env = environment
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("CLI %v: %w: %s", args, err, output)
		}
		return output, nil
	}
	for scan := 0; scan < 2; scan++ {
		output, err := run("-action", "start-demo")
		if err != nil {
			return err
		}
		var summary struct{ Discovered, Unchanged, PendingReview, Failed int }
		if err := json.NewDecoder(bytes.NewReader(output)).Decode(&summary); err != nil {
			return err
		}
		if summary.Discovered != 4 || summary.Failed != 0 || (scan == 0 && summary.PendingReview != 3) || (scan == 1 && summary.Unchanged != 4) {
			return fmt.Errorf("unexpected synthetic scan %d: %+v", scan, summary)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return err
	}
	server := exec.Command(binary, append(base, "-action", "serve", "-addr", address)...)
	server.Env = environment
	serverLog, err := os.Create(filepath.Join(work, "server.log"))
	if err != nil {
		return err
	}
	defer serverLog.Close()
	server.Stdout, server.Stderr = serverLog, serverLog
	if err := server.Start(); err != nil {
		return err
	}
	defer func() {
		_ = server.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- server.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = server.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := "http://" + address
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := client.Get(endpoint + "/semantic")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("owned review server did not become ready: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, route := range []string{"/semantic/guides", "/semantic/guides/quick-start", "/semantic/guides/reviewer-handbook", "/semantic/guides/semantic-readme", "/semantic/guides/crawler", "/semantic/guides/next-source-onboarding", "/semantic/guides/providers", "/semantic/guides/output-formats"} {
		response, err := client.Get(endpoint + route)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("archive guide %s returned %d", route, response.StatusCode)
		}
	}
	response, err := client.Get(endpoint + "/semantic/review/1")
	if err != nil {
		return err
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		return readErr
	}
	token := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(body)
	if len(token) != 2 {
		return fmt.Errorf("review page has no CSRF form token")
	}
	for id := 1; id <= 3; id++ {
		response, err := client.PostForm(fmt.Sprintf("%s/semantic/review/%d/approve", endpoint, id), url.Values{"csrf_token": {string(token[1])}, "reviewer": {"Synthetic release check"}})
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusSeeOther && response.StatusCode != http.StatusFound {
			return fmt.Errorf("synthetic approval %d returned %d", id, response.StatusCode)
		}
	}
	for _, format := range []string{"json", "jsonl", "markdown", "text"} {
		destination := filepath.Join(work, "export-"+format)
		_, err := run("-action", "export", "-format", format, "-output", destination)
		if err != nil {
			return err
		}
		if err := checkExport(destination, format); err != nil {
			return err
		}
	}
	fmt.Println("Archive smoke passed: 4/4 fixture pages, unchanged second scan, review approvals, all guides and four approved export formats.")
	return nil
}

func checkExport(destination, format string) error {
	path := destination
	if format == "markdown" || format == "text" {
		path = filepath.Join(destination, "manifest.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	count := 0
	if format == "jsonl" {
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var document struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(line, &document); err != nil {
				return err
			}
			if document.Status != "approved" {
				return fmt.Errorf("export contains unapproved document")
			}
			count++
		}
	} else {
		var envelope struct {
			Documents []json.RawMessage `json:"documents"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		count = len(envelope.Documents)
	}
	if count != 3 {
		return fmt.Errorf("%s export contained %d documents; want 3", format, count)
	}
	return nil
}
