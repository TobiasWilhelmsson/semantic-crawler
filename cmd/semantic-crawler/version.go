package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Overridden by the source archive's Make build, without requiring .git at runtime.
var buildVersion = "dev"
var buildRevision string

func versionDescription() string {
	revision := buildRevision
	modified := false
	if info, ok := debug.ReadBuildInfo(); ok && revision == "" {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	if revision == "" {
		revision = "unknown"
	}
	if modified {
		revision += " (modified)"
	}
	return fmt.Sprintf("semantic-crawler %s\nrevision %s\nGo %s\nplatform %s/%s\n", buildVersion, revision, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
