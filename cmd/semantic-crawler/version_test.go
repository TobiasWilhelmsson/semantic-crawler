package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionCLIWorksWithoutConfiguration(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "semantic-crawler")
	build := exec.Command("go", "build", "-o", binary, "./cmd/semantic-crawler")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	for _, malformed := range []bool{false, true} {
		root := t.TempDir()
		if malformed {
			if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PRIVATE_KEY=\"unterminated-secret\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		command := exec.Command(binary, "-version", "-source", "missing-source", "-env-file", "missing.env", "-max-page-bytes", "0")
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("version required runtime configuration: %v: %s", err, output)
		}
		for _, want := range []string{"semantic-crawler dev", "revision", "go1.", runtime.GOOS + "/" + runtime.GOARCH} {
			if !strings.Contains(string(output), want) {
				t.Fatalf("version missing %q: %s", want, output)
			}
		}
		if strings.Contains(string(output), "secret") {
			t.Fatal("version disclosed environment content")
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		wantCount := 0
		if malformed {
			wantCount = 1
		}
		if len(entries) != wantCount {
			t.Fatal("version created runtime files")
		}
	}
}
