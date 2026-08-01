package main

import (
	"encoding/json"
	"path/filepath"
	"strings"

	vigilpacks "github.com/PayCal-Technologies/vigil-public/internal/packs"
)

type extensionManifest = vigilpacks.Manifest
type extensionCommandContract = vigilpacks.CommandContract
type extensionReport = vigilpacks.Report

func loadExtensions(root string) extensionReport {
	return loadExtensionsForConfig("", root)
}

func loadExtensionsForConfig(configPath, root string) extensionReport {
	return vigilpacks.LoadCached(vigilpacks.Options{
		RepositoryRoot:     root,
		RepositoryBoundary: repositoryPackBoundaryForConfig(configPath),
		UserRoot:           userExtensionRoot(),
		Settings:           extensionSettingsForConfig(configPath),
	})
}

func userExtensionRoot() string {
	return vigilpacks.UserRoot()
}

func repositoryPackBoundaryForConfig(configPath string) string {
	configPath = resolvedConfigPath(configPath)
	if fileExists(configPath) {
		return filepath.Dir(configPath)
	}
	if root := gitRoot(); root != "" {
		return root
	}
	return mustGetwd()
}

func extensionSettingsForConfig(configPath string) extensionsConfig {
	settings := extensionsConfig{Enabled: true}
	path := resolvedConfigPath(configPath)
	data, err := readConfigFile(path)
	if err != nil {
		return settings
	}
	var raw struct {
		Extensions extensionsConfig `json:"extensions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return settings
	}
	settings = raw.Extensions
	if strings.TrimSpace(settings.ManifestRoot) == "" {
		settings.ManifestRoot = "extensions"
	}
	return settings
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func extensionRoot() string {
	return extensionRootForConfig("")
}

func extensionRootForConfig(configPath string) string {
	settings := extensionSettingsForConfig(configPath)
	name := strings.TrimSpace(settings.ManifestRoot)
	if name == "" {
		name = "extensions"
	}
	resolvedConfig := resolvedConfigPath(configPath)
	if fileExists(resolvedConfig) {
		return filepath.Join(filepath.Dir(resolvedConfig), name)
	}
	if root := vigilpacks.FindRootUpward(mustGetwd(), name); root != "" {
		return root
	}
	return name
}

func validateExtension(ext extensionManifest) []string {
	return vigilpacks.Validate(ext)
}
