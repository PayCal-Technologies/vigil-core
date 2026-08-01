package packs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadCachedInvalidatesOnManifestContentAndReturnsCopies(t *testing.T) {
	discoveryReports.Clear()
	boundary := t.TempDir()
	root := filepath.Join(boundary, "extensions")
	manifestPath := filepath.Join(root, "fixture", "extension.json")
	manifest := completeManifest()
	manifest.Path = ""
	writeCachedManifestFixture(t, manifestPath, manifest)
	options := Options{
		RepositoryRoot:     root,
		RepositoryBoundary: boundary,
		UserRoot:           filepath.Join(boundary, "missing-user"),
		Settings:           Settings{Enabled: true, AllowedKinds: []string{"custom"}},
	}

	first := LoadCached(options)
	fixture := cachedManifestByID(t, first, "fixture")
	if fixture.Description != "fixture" {
		t.Fatalf("first manifest = %#v", fixture)
	}
	for index := range first.Extensions {
		if first.Extensions[index].ID == "fixture" {
			first.Extensions[index].Description = "poisoned"
		}
	}
	second := LoadCached(options)
	if got := cachedManifestByID(t, second, "fixture").Description; got != "fixture" {
		t.Fatalf("cache value was mutable: %q", got)
	}
	if discoveryReports.Len() != 1 {
		t.Fatalf("cache size = %d", discoveryReports.Len())
	}

	manifest.Description = "changed without relying on file metadata"
	writeCachedManifestFixture(t, manifestPath, manifest)
	third := LoadCached(options)
	if got := cachedManifestByID(t, third, "fixture").Description; got != manifest.Description {
		t.Fatalf("stale manifest survived invalidation: %q", got)
	}
	if discoveryReports.Len() != 2 {
		t.Fatalf("cache size after invalidation = %d", discoveryReports.Len())
	}
}

func TestLoadCachedReportCountIsBounded(t *testing.T) {
	discoveryReports.Clear()
	for index := 0; index < maxCachedReports+10; index++ {
		LoadCached(Options{
			Settings: Settings{
				Enabled:      false,
				ManifestRoot: strconv.Itoa(index),
			},
		})
	}
	if size := discoveryReports.Len(); size != maxCachedReports {
		t.Fatalf("cache size = %d", size)
	}
}

func writeCachedManifestFixture(t *testing.T, path string, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func cachedManifestByID(t *testing.T, report Report, id string) Manifest {
	t.Helper()
	for _, manifest := range report.Extensions {
		if manifest.ID == id {
			return manifest
		}
	}
	t.Fatalf("manifest %q is missing from %#v", id, report)
	return Manifest{}
}
