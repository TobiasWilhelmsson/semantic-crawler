package main

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewCLILoadsCSRFTokenBeforeApplyingFlagDefaults(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "semantic-crawler")
	build := exec.Command("go", "build", "-o", binary, "./cmd/semantic-crawler")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build test CLI: %v\n%s", err, output)
	}
	for _, test := range []struct {
		name, token string
		flags       []string
	}{
		{"default env file", "from-file", nil},
		{"explicit env file", "from-file", []string{"-env-file", ".env"}},
		{"explicit token flag", "from-flag", []string{"-csrf-token", "from-flag"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("SEMANTIC_REVIEW_CSRF_TOKEN=from-file\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			config := liveTestConfig(t, "https://example.test/source/")
			args := []string{"-action", "serve", "-source-config", config, "-db", filepath.Join(directory, "review.db"), "-addr", address}
			command := exec.Command(binary, append(args, test.flags...)...)
			command.Dir = directory
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "SEMANTIC_REVIEW_CSRF_TOKEN=") {
					command.Env = append(command.Env, entry)
				}
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			client := &http.Client{Timeout: time.Second}
			endpoint := "http://" + address
			deadline := time.Now().Add(5 * time.Second)
			for {
				response, err := client.Get(endpoint + "/semantic")
				if err == nil {
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("review CLI did not start: %v", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			response, err := client.PostForm(endpoint+"/semantic/review/999/approve", url.Values{"csrf_token": {test.token}})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("configured token did not pass CSRF validation: HTTP %d", response.StatusCode)
			}
		})
	}
}
