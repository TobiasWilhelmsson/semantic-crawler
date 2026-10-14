package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise Make's expansion and the actual shell boundary. A harmless argument
// capture program replaces compilation and crawling; no website is contacted.
func TestWebsiteMakePreservesInputsWithoutExecutingThem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Make recipes require a POSIX shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	project, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	write := func(name, contents string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Makefile", "go.mod", "scripts/build-flags.sh"} {
		contents, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Fatal(err)
		}
		write(name, string(contents), 0600)
	}
	write("go-check", `#!/bin/sh
case "$1:$2" in
  env:GOVERSION) printf '%s\n' go1.26.9 ;;
  env:CGO_ENABLED) printf '%s\n' 1 ;;
  env:CC) printf '%s\n' sh ;;
  build:*) mkdir -p bin; cp "$CAPTURE_PROGRAM" bin/semantic-crawler ;;
  run:*) shift 2; exec "$CAPTURE_PROGRAM" "$@" ;;
  *) exit 2 ;;
esac
`, 0700)
	write("capture-program", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGUMENTS\"\n", 0700)
	argumentsPath := filepath.Join(directory, "arguments.txt")
	t.Setenv("CAPTURE_PROGRAM", filepath.Join(directory, "capture-program"))
	t.Setenv("CAPTURE_ARGUMENTS", argumentsPath)
	marker := filepath.Join(directory, "executed-input")
	payloads := []string{
		"example.com/$(shell touch " + marker + ")/docs/",
		"example.com/$(touch " + marker + ")/docs/",
		"example.com/`touch " + marker + "`/docs/",
		"example.com/'; touch " + marker + "; printf '/docs/",
	}
	for _, style := range []string{"variable", "target", "SOURCE", "DB", "REPORT_ROOT", "BASELINE", "MODEL_MODE", "ADDR", "MAX_PAGES", "MAX_PAGE_BYTES"} {
		for _, payload := range payloads {
			args := []string{"--no-print-directory", "GO=" + filepath.Join(directory, "go-check")}
			parameter := "-website"
			if style == "variable" {
				args = append(args, "website", "URL="+payload)
			} else if style == "target" {
				args = append(args, payload)
			} else {
				target := "start"
				switch style {
				case "SOURCE":
					parameter = "-source"
				case "DB":
					parameter = "-db"
				case "REPORT_ROOT":
					parameter = "-report-dir"
				case "BASELINE":
					target, parameter = "benchmark", "-baseline"
				case "MODEL_MODE":
					parameter = "-model-mode"
				case "ADDR":
					parameter = "-addr"
				case "MAX_PAGES":
					target, parameter = "benchmark", "-benchmark-max-pages"
				case "MAX_PAGE_BYTES":
					target, parameter = "crawl", "-max-page-bytes"
				}
				args = append(args, target, style+"="+payload)
			}
			command := exec.Command(makePath, args...)
			command.Dir = directory
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v %s", style, err, output)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("%s executed website input %q", style, payload)
			}
			captured, err := os.ReadFile(argumentsPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(captured), parameter+"\n"+payload+"\n") {
				t.Fatalf("%s changed website input: %s", style, captured)
			}
		}
	}
}
