package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wrapper must not restore an operator's private baseline or page limits
// through Make's override channels when it enters the extracted archive.
func TestReleaseCheckClearsInheritedMakeOverrides(t *testing.T) {
	root := distributionRepo(t)
	makefile := `BASELINE ?= bundled-synthetic-baseline
MAX_PAGES ?= 0
.PHONY: setup build demo eval benchmark proof reviewer-learning
setup build demo eval proof reviewer-learning:
	@true
benchmark:
	@test '$(BASELINE)' = bundled-synthetic-baseline
	@test '$(MAX_PAGES)' = 0
`
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(makefile), 0600); err != nil {
		t.Fatal(err)
	}
	distributionCommand(t, root, "git", "add", "Makefile")
	distributionCommand(t, root, "git", "-c", "user.name=Distribution test", "-c", "user.email=test@example.test", "commit", "--quiet", "-m", "Isolated Make consumer")
	command := exec.Command("sh", "scripts/release-check.sh", "--skip-code-checks")
	command.Dir = root
	// Only the Go smoke runner is replaced here; actual child Make, packaging and
	// extraction run. The runner's behavior is covered independently.
	command.Env = append(os.Environ(), "GO=true", "MAKEFLAGS= -- BASELINE=operator-private-baseline MAX_PAGES=1", "MAKEOVERRIDES=BASELINE=operator-private-baseline MAX_PAGES=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("release-check inherited operator overrides: %v: %s", err, output)
	}
}

func TestReleaseSmokeRetainsServerFailureDiagnostics(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "smoke-check")
	build := exec.Command("go", "build", "-o", binary, "./scripts/release-smoke.go")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build smoke runner: %v: %s", err, output)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
case "$*" in
  *start-demo*)
    if [ -e "$SMOKE_SCAN_MARKER" ]; then printf '%s\n' '{"Discovered":4,"Unchanged":4,"PendingReview":0,"Failed":0}'
    else touch "$SMOKE_SCAN_MARKER"; printf '%s\n' '{"Discovered":4,"Unchanged":0,"PendingReview":3,"Failed":0}'; fi ;;
  *serve*) printf '%s\n' 'SYNTHETIC_SERVER_STARTUP_FAILURE' >&2; exit 1 ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "bin", "semantic-crawler"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary)
	command.Dir = root
	command.Env = append(os.Environ(), "SMOKE_SCAN_MARKER="+filepath.Join(root, "scanned"), "RELEASE_CHECK_WORK="+root)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "SYNTHETIC_SERVER_STARTUP_FAILURE") {
		t.Fatalf("server failure lost diagnostics: %v: %s", err, output)
	}
	logs, err := filepath.Glob(filepath.Join(root, "semantic-smoke-*", "server.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("failed smoke state was discarded: %v: %v", logs, err)
	}
}
