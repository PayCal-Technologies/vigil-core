package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChainInstallAndUninstallRestoreExactHook(t *testing.T) {
	hookDir := t.TempDir()
	hookPath := filepath.Join(hookDir, "pre-commit")
	original := []byte("#!/bin/sh\nprintf original\n")
	if err := os.WriteFile(hookPath, original, 0o744); err != nil {
		t.Fatal(err)
	}
	plans, err := PlanInstall(hookDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyInstall(hookDir, plans); err != nil {
		t.Fatal(err)
	}
	managed, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if !IsManaged(managed) || !strings.Contains(string(managed), ".vigil-original") {
		t.Fatalf("managed hook = %q", managed)
	}
	inspections := Inspect(hookDir)
	if err := ApplyUninstall(inspections); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(original) {
		t.Fatalf("restored hook = %q", restored)
	}
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o744 {
		t.Fatalf("restored mode = %#o", info.Mode().Perm())
	}
}

func TestApplyInstallRollsBackEarlierWrites(t *testing.T) {
	hookDir := t.TempDir()
	first := filepath.Join(hookDir, "pre-commit")
	plans := []InstallPlan{
		{Hook: "pre-commit", Path: first, Action: "install", Body: Body("pre-commit", "")},
		{Hook: "pre-push", Path: filepath.Join(hookDir, "missing", "pre-push"), Action: "install", Body: Body("pre-push", "")},
	}
	if err := ApplyInstall(hookDir, plans); err == nil {
		t.Fatal("expected second write to fail")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("first hook survived rollback: %v", err)
	}
}

func TestPlanInstallRejectsHookSymlink(t *testing.T) {
	hookDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "external-hook")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nprintf external\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(hookDir, "pre-commit")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := PlanInstall(hookDir, true); err == nil ||
		!strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("PlanInstall symlink error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#!/bin/sh\nprintf external\n" {
		t.Fatalf("external hook changed to %q", data)
	}
}
