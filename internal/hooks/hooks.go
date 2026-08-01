package hooks

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
)

var managedHooks = []string{"pre-commit", "pre-push"}

type CommandRunner func(args ...string) (output string, exitCode int)

type InstallPlan struct {
	Hook       string      `json:"hook"`
	Path       string      `json:"path"`
	Action     string      `json:"action"`
	BackupPath string      `json:"backup_path,omitempty"`
	Body       string      `json:"-"`
	BackupBody []byte      `json:"-"`
	BackupMode os.FileMode `json:"-"`

	existingBody []byte
	existingMode os.FileMode
	hadExisting  bool
}

type Inspection struct {
	Hook       string `json:"hook"`
	Path       string `json:"path"`
	State      string `json:"state"`
	BackupPath string `json:"backup_path,omitempty"`
	Digest     string `json:"sha256,omitempty"`
}

func ResolveDir(cwd string, run CommandRunner) (string, error) {
	root, code := run("rev-parse", "--show-toplevel")
	if code != 0 || strings.TrimSpace(root) == "" {
		return "", errors.New("not inside a git repository")
	}
	root = strings.TrimSpace(root)
	if configured, code := run("config", "--path", "--get", "core.hooksPath"); code == 0 {
		configured = strings.TrimSpace(configured)
		if configured != "" {
			if !filepath.IsAbs(configured) {
				configured = filepath.Join(root, configured)
			}
			return filepath.Clean(configured), nil
		}
	}
	resolved, code := run("rev-parse", "--git-path", "hooks")
	if code != 0 {
		message := strings.TrimSpace(resolved)
		if message == "" {
			message = "unable to resolve Git hooks path"
		}
		return "", errors.New(message)
	}
	resolved = strings.TrimSpace(resolved)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	return filepath.Clean(resolved), nil
}

func PlanInstall(hookDir string, chain bool) ([]InstallPlan, error) {
	plans := make([]InstallPlan, 0, len(managedHooks))
	for _, hook := range managedHooks {
		path := filepath.Join(hookDir, hook)
		backupPath := path + ".vigil-original"
		plan := InstallPlan{
			Hook:   hook,
			Path:   path,
			Action: "install",
			Body:   Body(hook, ""),
		}
		existing, existingMode, err := readRegularFile(path)
		switch {
		case err == nil:
			plan.hadExisting = true
			plan.existingBody = append([]byte(nil), existing...)
			plan.existingMode = existingMode
			if IsManaged(existing) {
				plan.Action = "unchanged"
				plan.Body = string(existing)
			} else if !chain {
				return nil, fmt.Errorf("existing hook differs, refusing to overwrite: %s", path)
			} else {
				if fileExists(backupPath) {
					return nil, fmt.Errorf("existing chain backup blocks install: %s", backupPath)
				}
				plan.Action = "chain"
				plan.BackupPath = backupPath
				plan.BackupBody = append([]byte(nil), existing...)
				plan.BackupMode = existingMode
				plan.Body = Body(hook, backupPath)
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func ApplyInstall(hookDir string, plans []InstallPlan) error {
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	applied := make([]InstallPlan, 0, len(plans))
	for _, plan := range plans {
		if plan.Action == "unchanged" {
			continue
		}
		if plan.BackupPath != "" {
			mode := plan.BackupMode
			if mode == 0 {
				mode = 0o700
			}
			if _, err := atomicfile.Write(plan.BackupPath, plan.BackupBody, atomicfile.Options{DefaultMode: mode}); err != nil {
				rollbackInstall(applied)
				return err
			}
		}
		if _, err := atomicfile.Write(plan.Path, []byte(plan.Body), atomicfile.Options{DefaultMode: 0o755}); err != nil {
			if plan.BackupPath != "" {
				_ = os.Remove(plan.BackupPath)
			}
			rollbackInstall(applied)
			return err
		}
		applied = append(applied, plan)
	}
	return nil
}

func Inspect(hookDir string) []Inspection {
	inspections := make([]Inspection, 0, len(managedHooks))
	for _, hook := range managedHooks {
		path := filepath.Join(hookDir, hook)
		inspection := Inspection{Hook: hook, Path: path, State: "missing"}
		data, _, err := readRegularFile(path)
		if err == nil {
			sum := sha256.Sum256(data)
			inspection.Digest = fmt.Sprintf("%x", sum[:8])
			if IsManaged(data) {
				inspection.State = "managed"
			} else {
				inspection.State = "foreign"
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			inspection.State = "unreadable"
		}
		backupPath := path + ".vigil-original"
		if _, _, backupErr := readRegularFile(backupPath); backupErr == nil {
			inspection.BackupPath = backupPath
		} else if !errors.Is(backupErr, os.ErrNotExist) {
			inspection.State = "unreadable"
		}
		inspections = append(inspections, inspection)
	}
	return inspections
}

func ApplyUninstall(inspections []Inspection) error {
	type restoration struct {
		inspection Inspection
		body       []byte
		mode       os.FileMode
	}
	restorations := make([]restoration, 0, len(inspections))
	for _, inspection := range inspections {
		if inspection.State == "foreign" || inspection.State == "unreadable" {
			return fmt.Errorf("refusing to remove %s hook: %s", inspection.State, inspection.Path)
		}
		if inspection.State != "managed" || inspection.BackupPath == "" {
			continue
		}
		body, mode, err := readRegularFile(inspection.BackupPath)
		if err != nil {
			return err
		}
		restorations = append(restorations, restoration{
			inspection: inspection,
			body:       body,
			mode:       mode,
		})
	}
	restorationByPath := make(map[string]restoration, len(restorations))
	for _, restoration := range restorations {
		restorationByPath[restoration.inspection.Path] = restoration
	}
	for _, inspection := range inspections {
		if inspection.State != "managed" {
			continue
		}
		if restoration, ok := restorationByPath[inspection.Path]; ok {
			if _, err := atomicfile.Write(inspection.Path, restoration.body, atomicfile.Options{DefaultMode: restoration.mode}); err != nil {
				return err
			}
			if err := os.Remove(inspection.BackupPath); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(inspection.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func Body(hook, chainedHook string) string {
	var body strings.Builder
	body.WriteString("#!/usr/bin/env sh\n")
	body.WriteString("# Managed by Vigil. Use `vigil hooks:uninstall` to restore or remove.\n")
	if chainedHook != "" {
		body.WriteString(shellQuote(chainedHook) + " \"$@\" || exit $?\n")
	}
	body.WriteString("exec vigil hooks:" + hook + " \"$@\"\n")
	return body.String()
}

func IsManaged(data []byte) bool {
	return bytes.Contains(data, []byte("# Managed by Vigil."))
}

func rollbackInstall(plans []InstallPlan) {
	for i := len(plans) - 1; i >= 0; i-- {
		plan := plans[i]
		if plan.hadExisting {
			mode := plan.existingMode
			if mode == 0 {
				mode = 0o700
			}
			_, _ = atomicfile.Write(plan.Path, plan.existingBody, atomicfile.Options{DefaultMode: mode})
		} else {
			_ = os.Remove(plan.Path)
		}
		if plan.BackupPath != "" {
			_ = os.Remove(plan.BackupPath)
		}
	}
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func fileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && !info.IsDir()
}

func readRegularFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("hook path must be a regular non-symlink file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}
