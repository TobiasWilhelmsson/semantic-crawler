package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"semantic-crawler/internal/semantic"
)

func TestWebsiteSourceCreatesBoundaryAndPreservesExistingInstructions(t *testing.T) {
	root := t.TempDir()
	source, err := prepareWebsiteSource(root, "Example.COM", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if source.Config.SeedURL != "https://example.com/" || source.Config.AllowedHost != "example.com" || source.Config.AllowedPathPrefix != "/" || source.Config.MaxPages != 10 || source.Config.SourceID <= 0 {
		t.Fatalf("website config: %#v", source)
	}
	source.Config.Scope = "Keep only product manuals"
	source.Config.SkillVersion = 7
	encoded, err := semantic.MarshalSourceConfig(source.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source.ConfigPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	instructions := []byte("# Product manuals\nKeep warning tables.\n")
	if err = os.WriteFile(source.Config.SourceSkillPath, instructions, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := prepareWebsiteSource(root, "https://example.com/", 25, 3)
	if err != nil {
		t.Fatal(err)
	}
	configAfter, _ := os.ReadFile(source.ConfigPath)
	skillAfter, _ := os.ReadFile(source.Config.SourceSkillPath)
	if again.Slug != source.Slug || again.Config.SourceID != source.Config.SourceID || string(configAfter) != string(encoded) || string(skillAfter) != string(instructions) {
		t.Fatal("repeat command replaced source identity or instructions")
	}
}

func TestWebsiteRejectsInvalidInputBeforeWriting(t *testing.T) {
	for _, raw := range []string{"typo", "ftp://example.com/", "https://user:pass@example.com/", "https://example.com/?token=secret", "https://example.com/#section", "https://bad host.example/", "-bad.example", "//example.com/", "https://example.com:bad/"} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			if _, err := prepareWebsiteSource(root, raw, 10, 2); err == nil {
				t.Fatalf("accepted %q", raw)
			}
			files, err := os.ReadDir(root)
			if err != nil || len(files) != 0 {
				t.Fatalf("invalid input wrote source files: %v %v", files, err)
			}
		})
	}
	for _, limits := range [][2]int{{0, 2}, {1001, 2}, {10, -1}, {10, 11}} {
		root := t.TempDir()
		if _, err := prepareWebsiteSource(root, "example.com", limits[0], limits[1]); err == nil {
			t.Fatalf("invalid limits accepted: %v", limits)
		}
		files, _ := os.ReadDir(root)
		if len(files) != 0 {
			t.Fatal("invalid limits wrote source")
		}
	}
}

func TestWebsitePathAndPortRemainScoped(t *testing.T) {
	root := t.TempDir()
	source, err := prepareWebsiteSource(root, "http://127.0.0.1:18080/docs/", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if source.Config.SeedURL != "http://127.0.0.1:18080/docs/" || source.Config.AllowedHost != "127.0.0.1" || source.Config.AllowedPathPrefix != "/docs/" || source.Config.MaxDepth != 0 {
		t.Fatalf("scope widened: %#v", source.Config)
	}
	if _, err = os.Stat(filepath.Join(root, "source-skills", source.Slug+".md")); err != nil {
		t.Fatal(err)
	}
}

func TestServeReviewBusyPortDoesNotStartWebsiteWork(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	started, stopped := false, false
	err = serveReview(context.Background(), listener.Addr().String(), http.NotFoundHandler(), func(context.Context) error { stopped = true; return nil }, func() error { started = true; return nil })
	if err == nil || started || !stopped {
		t.Fatalf("bind failure started work or skipped shutdown: %v started=%v stopped=%v", err, started, stopped)
	}
}

func TestServeReviewReadyFailureStillShutsDown(t *testing.T) {
	stopped := false
	cause := errors.New("website startup unavailable")
	err := serveReview(context.Background(), "127.0.0.1:0", http.NotFoundHandler(), func(context.Context) error { stopped = true; return nil }, func() error { return cause })
	if !errors.Is(err, cause) || !stopped {
		t.Fatalf("ready failure cleanup: %v stopped=%v", err, stopped)
	}
}

func TestWebsiteSupportsValidDNSHyphensAndIPv6(t *testing.T) {
	for _, raw := range []string{"xn--bcher-kva.example", "my--site.example", "http://[2001:db8::1]:8080/docs/"} {
		t.Run(raw, func(t *testing.T) {
			source, err := prepareWebsiteSource(t.TempDir(), raw, 10, 2)
			if err != nil || source.Config.SourceID <= 0 {
				t.Fatalf("valid website cannot create source: %#v %v", source, err)
			}
		})
	}
}
