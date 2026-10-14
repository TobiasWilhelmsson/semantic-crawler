package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func distributionCommand(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	command := exec.Command(args[0], args[1:]...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", args[0], err, output)
	}
	return output
}

func distributionRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("distribution regression requires git")
	}
	project, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source tree with spaces")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	// Use the tracked inventory plus this change's required files. The production
	// script still packages a committed tree, never the developer's workspace.
	inventory := distributionTestInventory(t, project)
	for _, name := range inventory {
		data, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".env", "data/private.db", "artifacts/private-report.json", "configs/sources/private-source.json", "configs/sources/source-skills/private-source.md", "docs/superpowers/plans/private.md"} {
		destination := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte("PRIVATE SENTINEL"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	distributionCommand(t, root, "git", "init", "--quiet")
	distributionCommand(t, root, "git", "add", "--force", ".")
	distributionCommand(t, root, "git", "-c", "user.name=Distribution test", "-c", "user.email=test@example.test", "commit", "--quiet", "-m", "Synthetic distribution fixture")
	return root
}

func distributionTestInventory(t *testing.T, project string) []string {
	t.Helper()
	git := exec.Command("git", "rev-parse", "--show-toplevel")
	git.Dir = project
	gitRoot, err := git.Output()
	if err == nil && strings.TrimSpace(string(gitRoot)) == project {
		inventory := strings.Split(strings.TrimSuffix(string(distributionCommand(t, project, "git", "ls-files", "-z")), "\x00"), "\x00")
		return append(inventory, ".gitattributes", "scripts/package-source.sh", "scripts/build-flags.sh", "scripts/fmt-check.sh", "cmd/semantic-crawler/version.go", "cmd/semantic-crawler/version_test.go")
	}
	// Downloaded source has no .git. Copy only the distribution's public support
	// files into the disposable Git fixture, excluding any operator-created data.
	inventory := []string{".gitignore", ".gitattributes", ".env.example", "LICENSE", "README.md", "CONTRIBUTING.md", "SECURITY.md", "CHANGELOG.md", "go.mod", "go.sum", "Makefile", "configs/sources/generic-health-sample.json", "configs/sources/source-skills/generic-health-sample.md"}
	for _, directory := range []string{"cmd", "internal", "scripts", "docs", "tests/fixtures", ".github"} {
		err := filepath.WalkDir(filepath.Join(project, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			relative, err := filepath.Rel(project, path)
			if err != nil {
				return err
			}
			inventory = append(inventory, relative)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return inventory
}

func TestDistributionArchiveAndIdentity(t *testing.T) {
	root := distributionRepo(t)
	distributionCommand(t, root, "sh", "scripts/package-source.sh", "0.1.0")
	archivePath := filepath.Join(root, "dist", "semantic-crawler-0.1.0-source.tar.gz")
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	zipped, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer zipped.Close()
	reader := tar.NewReader(zipped)
	extracted := t.TempDir()
	files := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(header.Name, "semantic-crawler-0.1.0/")
		if strings.HasPrefix(filepath.Base(header.Name), "._") {
			t.Fatalf("archive exposed filesystem metadata in %q", header.Name)
		}
		for key := range header.PAXRecords {
			if strings.HasPrefix(key, "SCHILY.xattr.") || strings.HasPrefix(key, "LIBARCHIVE.xattr.") {
				t.Fatalf("archive exposed extended attribute %q in %q", key, header.Name)
			}
		}
		if strings.Contains(name, "private") || strings.HasPrefix(name, ".git/") || strings.HasPrefix(name, "docs/superpowers/") || name == ".env" {
			t.Fatalf("archive exposed private/workspace file %q", name)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		files[name] = true
		destination := filepath.Join(extracted, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, required := range []string{"LICENSE", "go.mod", "go.sum", "Makefile", ".env.example", "VERSION", "REVISION", "configs/sources/generic-health-sample.json", "configs/sources/source-skills/generic-health-sample.md", "docs/output-formats.md", "tests/fixtures/generic-health-sample/baseline.json"} {
		if !files[required] {
			t.Errorf("archive missing %s", required)
		}
	}
	distributionCommand(t, extracted, "make", "build")
	distributionCommand(t, extracted, "go", "test", "./cmd/semantic-crawler", "-run", "^TestDistributionChecksumFailureLeavesNoPackage$", "-count=1")
	output := distributionCommand(t, extracted, "./bin/semantic-crawler", "-version")
	revision := strings.TrimSpace(string(distributionCommand(t, root, "git", "rev-parse", "HEAD")))
	if !strings.Contains(string(output), "semantic-crawler 0.1.0") || !strings.Contains(string(output), revision) {
		t.Fatalf("archive lost identity without .git: %s", output)
	}
	before, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "scripts/package-source.sh", "0.1.0")
	command.Dir = root
	if err := command.Run(); err == nil {
		t.Fatal("existing archive was accepted")
	}
	after, err := os.ReadFile(archivePath)
	if err != nil || string(before) != string(after) {
		t.Fatal("existing archive changed")
	}
}

func TestDistributionRejectsUnsafeMetadataAndDirtyTree(t *testing.T) {
	root := distributionRepo(t)
	marker := filepath.Join(root, "executed-input")
	for _, version := range []string{"../private", "", "0.1.0;touch " + marker, "$(shell touch " + marker + ")", "`touch " + marker + "`", "0.1.0\nsecret"} {
		command := exec.Command("make", "dist", "VERSION="+version)
		command.Dir = root
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("unsafe version accepted %q: %s", version, output)
		}
	}
	for _, assignment := range []string{"VERSION=$(shell touch " + marker + ")", "REVISION=$(shell touch " + marker + ")", "REVISION=not-a-revision"} {
		command := exec.Command("make", "build", assignment)
		command.Dir = root
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("unsafe build metadata accepted: %s", output)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Make executed metadata as code")
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "scripts/package-source.sh", "0.2.0")
	command.Dir = root
	if err := command.Run(); err == nil {
		t.Fatal("dirty committed tree was packaged")
	}
	archives, _ := filepath.Glob(filepath.Join(root, "dist", "*"))
	if len(archives) != 0 {
		t.Fatalf("failure left completed-looking artifacts: %v", archives)
	}
}

func TestDistributionChecksumFailureLeavesNoPackage(t *testing.T) {
	root := distributionRepo(t)
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "sha256sum"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "scripts/package-source.sh", "0.3.0")
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("checksum failure published a package: %s", output)
	}
	files, err := os.ReadDir(filepath.Join(root, "dist"))
	if err != nil || len(files) != 0 {
		t.Fatalf("checksum failure left package files: %v, %v", files, err)
	}
}

// Missing ignore rules would make a locally generated private source eligible
// for ordinary staging. Exercise Git's interpretation in a disposable repo.
func TestDistributionIgnoresLocalSources(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	if output, err := exec.Command(git, "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("init: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path    string
		ignored bool
	}{
		{"configs/sources/private-source.json", true},
		{"configs/sources/private source.json", true},
		{"configs/sources/source-skills/private-source.md", true},
		{"configs/sources/source-skills/private instructions.md", true},
		{"configs/sources/generic-health-sample.json", false},
		{"configs/sources/source-skills/generic-health-sample.md", false},
		{".env.example", false},
		{"internal/semantic/crawler.go", false},
		{"tests/fixtures/generic-health-sample/page.html", false},
		{"dist/semantic-crawler-0.1.0-source.tar.gz", true},
	} {
		t.Run(item.path, func(t *testing.T) {
			command := exec.Command(git, "check-ignore", "--no-index", "--quiet", item.path)
			command.Dir = root
			err := command.Run()
			if err != nil {
				if status, ok := err.(*exec.ExitError); !ok || status.ExitCode() != 1 {
					t.Fatalf("check-ignore: %v", err)
				}
			}
			if got := err == nil; got != item.ignored {
				t.Fatalf("ignored = %v; want %v", got, item.ignored)
			}
		})
	}
}
