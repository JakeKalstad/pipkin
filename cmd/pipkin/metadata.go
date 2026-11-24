package main

import (
	"os"
	"runtime"
	"runtime/debug"
)

type buildInfo struct {
	Version, Commit, Platform string
}

func buildMetadata() buildInfo {
	build := buildInfo{
		Version:  os.Getenv("PIPKIN_VERSION"),
		Commit:   os.Getenv("PIPKIN_COMMIT"),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if build.Version == "" {
			build.Version = info.Main.Version
		}
		if build.Commit == "" {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					build.Commit = setting.Value
					break
				}
			}
		}
	}
	if build.Version == "" || build.Version == "(devel)" {
		build.Version = "dev"
	}
	return build
}
