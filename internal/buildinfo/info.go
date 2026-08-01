package buildinfo

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
	Dirty     = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Dirty     string `json:"dirty"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

func Current() Info {
	info := Info{
		Version:   normalized(Version, "dev"),
		Commit:    normalized(Commit, "unknown"),
		BuildDate: normalized(BuildDate, "unknown"),
		Dirty:     normalized(Dirty, "unknown"),
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			info.Version = build.Main.Version
		}
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "unknown" && setting.Value != "" {
					info.Commit = setting.Value
				}
			case "vcs.time":
				if info.BuildDate == "unknown" && setting.Value != "" {
					info.BuildDate = setting.Value
				}
			case "vcs.modified":
				if info.Dirty == "unknown" && setting.Value != "" {
					info.Dirty = setting.Value
				}
			}
		}
	}
	return info
}

func ReproducibleDate() string {
	if value := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
			return time.Unix(seconds, 0).UTC().Format("2006-01-02")
		}
	}
	if value := strings.TrimSpace(Current().BuildDate); value != "" && value != "unknown" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC().Format("2006-01-02")
		}
	}
	return "1970-01-01"
}

func normalized(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
