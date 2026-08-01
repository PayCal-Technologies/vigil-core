package packs

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	vigilcache "github.com/PayCal-Technologies/vigil-public/internal/cache"
)

const maxCachedReports = 32

var discoveryReports = vigilcache.NewLRU[string, Report](maxCachedReports)

type discoverySnapshot struct {
	SchemaVersion      string          `json:"schema_version"`
	HostAPIVersion     string          `json:"host_api_version"`
	RepositoryRoot     string          `json:"repository_root"`
	RepositoryBoundary string          `json:"repository_boundary"`
	UserRoot           string          `json:"user_root"`
	Settings           Settings        `json:"settings"`
	Layers             []layerSnapshot `json:"layers"`
}

type layerSnapshot struct {
	Name    string          `json:"name"`
	Root    string          `json:"root"`
	State   string          `json:"state"`
	Error   string          `json:"error,omitempty"`
	Entries []entrySnapshot `json:"entries,omitempty"`
}

type entrySnapshot struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
	Error          string `json:"error,omitempty"`
}

func LoadCached(options Options) Report {
	key, cacheable := discoveryKey(options)
	if cacheable {
		if report, ok := discoveryReports.Get(key); ok {
			return cloneReport(report)
		}
	}
	report := Load(options)
	if cacheable {
		discoveryReports.Put(key, cloneReport(report))
	}
	return report
}

func discoveryKey(options Options) (string, bool) {
	if options.OfficialFS != nil {
		return "", false
	}
	userRoot := options.UserRoot
	if userRoot == "" {
		userRoot = UserRoot()
	}
	snapshot := discoverySnapshot{
		SchemaVersion:      SchemaVersion,
		HostAPIVersion:     HostAPIVersion,
		RepositoryRoot:     cleanAbsolute(options.RepositoryRoot),
		RepositoryBoundary: cleanAbsolute(options.RepositoryBoundary),
		UserRoot:           cleanAbsolute(userRoot),
		Settings:           options.Settings,
	}
	if options.Settings.Enabled {
		snapshot.Layers = append(snapshot.Layers, snapshotLayer("user", userRoot))
		repositoryRoot, err := ConfineRepositoryRoot(options.RepositoryRoot, options.RepositoryBoundary)
		if err != nil {
			snapshot.Layers = append(snapshot.Layers, layerSnapshot{
				Name:  "repository",
				Root:  cleanAbsolute(options.RepositoryRoot),
				State: "invalid",
				Error: err.Error(),
			})
		} else {
			snapshot.Layers = append(snapshot.Layers, snapshotLayer("repository", repositoryRoot))
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:]), true
}

func snapshotLayer(name, root string) layerSnapshot {
	snapshot := layerSnapshot{Name: name, Root: cleanAbsolute(root), State: "missing"}
	if root == "" {
		return snapshot
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot
	}
	if err != nil {
		snapshot.State = "invalid"
		snapshot.Error = err.Error()
		return snapshot
	}
	snapshot.State = "present"
	if len(entries) > maxLayerEntries {
		snapshot.State = "invalid"
		snapshot.Error = fmt.Sprintf("pack layer exceeds %d entries", maxLayerEntries)
		return snapshot
	}
	snapshot.Entries = make([]entrySnapshot, 0, len(entries))
	for _, entry := range entries {
		entryState := entrySnapshot{Name: entry.Name(), Type: entry.Type().String()}
		if entry.IsDir() {
			manifestPath := filepath.Join(root, entry.Name(), "extension.json")
			if err := ensurePathInsideRoot(root, manifestPath); err != nil {
				entryState.Error = err.Error()
			} else if data, err := readBoundedManifest(manifestPath); err != nil {
				entryState.Error = err.Error()
			} else {
				sum := sha256.Sum256(data)
				entryState.ManifestDigest = fmt.Sprintf("sha256:%x", sum[:])
			}
		}
		snapshot.Entries = append(snapshot.Entries, entryState)
	}
	return snapshot
}

func cleanAbsolute(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return absolute
}

func cloneReport(report Report) Report {
	data, err := json.Marshal(report)
	if err != nil {
		return report
	}
	var cloned Report
	if err := json.Unmarshal(data, &cloned); err != nil {
		return report
	}
	return cloned
}
