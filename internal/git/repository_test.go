package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMutationFingerprintTracksUntrackedContent(t *testing.T) {
	repository := t.TempDir()
	run := commandRunner(repository)
	if output, code := run("init"); code != 0 {
		t.Fatalf("git init: %s", output)
	}
	path := filepath.Join(repository, "untracked.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, ok := MutationFingerprint(run)
	if !ok {
		t.Fatal("before fingerprint unavailable")
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, ok := MutationFingerprint(run)
	if !ok {
		t.Fatal("after fingerprint unavailable")
	}
	if before.Hash == after.Hash {
		t.Fatal("untracked content change did not alter fingerprint")
	}
}

func TestMutationFingerprintDoesNotFollowUntrackedSymlink(t *testing.T) {
	repository := t.TempDir()
	run := commandRunner(repository)
	if output, code := run("init"); code != 0 {
		t.Fatalf("git init: %s", output)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	before, ok := MutationFingerprint(run)
	if !ok {
		t.Fatal("before fingerprint unavailable")
	}
	if err := os.WriteFile(outside, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, ok := MutationFingerprint(run)
	if !ok {
		t.Fatal("after fingerprint unavailable")
	}
	if before.Hash != after.Hash {
		t.Fatal("fingerprint followed an untracked symlink outside the repository")
	}
}

func TestMutationFingerprintWorksFromRealGitWorktree(t *testing.T) {
	repository := t.TempDir()
	run := commandRunner(repository)
	if output, code := run("init"); code != 0 {
		t.Fatalf("git init: %s", output)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, run, "base")
	worktree := filepath.Join(t.TempDir(), "worktree")
	if output, code := run("worktree", "add", "-b", "fixture-worktree", worktree); code != 0 {
		t.Fatalf("git worktree add: %s", output)
	}
	worktreeRun := commandRunner(worktree)
	before, ok := MutationFingerprint(worktreeRun)
	if !ok {
		t.Fatal("worktree fingerprint unavailable")
	}
	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, ok := MutationFingerprint(worktreeRun)
	if !ok || before.Hash == after.Hash {
		t.Fatalf("worktree mutation was not detected: before=%#v after=%#v", before, after)
	}
}

func TestMutationFingerprintDetectsDirtySubmodule(t *testing.T) {
	source := t.TempDir()
	sourceRun := commandRunner(source)
	if output, code := sourceRun("init"); code != 0 {
		t.Fatalf("source git init: %s", output)
	}
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, sourceRun, "source")

	repository := t.TempDir()
	run := commandRunner(repository)
	if output, code := run("init"); code != 0 {
		t.Fatalf("parent git init: %s", output)
	}
	if output, code := run("-c", "protocol.file.allow=always", "submodule", "add", source, "module"); code != 0 {
		t.Fatalf("git submodule add: %s", output)
	}
	commitAll(t, run, "submodule")
	before, ok := MutationFingerprint(run)
	if !ok {
		t.Fatal("parent fingerprint unavailable")
	}
	if err := os.WriteFile(filepath.Join(repository, "module", "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, ok := MutationFingerprint(run)
	if !ok || before.Hash == after.Hash {
		t.Fatalf("dirty submodule was not detected: before=%#v after=%#v", before, after)
	}
}

func TestMutationFingerprintScalesToLargeDirtyRepositoryFixture(t *testing.T) {
	repository := t.TempDir()
	run := commandRunner(repository)
	if output, code := run("init"); code != 0 {
		t.Fatalf("git init: %s", output)
	}
	const fileCount = 2000
	generated := filepath.Join(repository, "generated")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < fileCount; index++ {
		name := filepath.Join(generated, strconv.Itoa(index)+".txt")
		if err := os.WriteFile(name, []byte("fixture payload\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	before, ok := MutationFingerprint(run)
	if !ok {
		t.Fatal("large-repository fingerprint unavailable")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("large-repository fingerprint took %s", elapsed)
	}
	changed := filepath.Join(repository, "generated", strconv.Itoa(fileCount/2)+".txt")
	if err := os.WriteFile(changed, []byte("changed payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, ok := MutationFingerprint(run)
	if !ok || before.Hash == after.Hash {
		t.Fatal("large dirty repository content change was not detected")
	}
}

func TestMutationFingerprintFailsClosedForOversizedUntrackedContent(t *testing.T) {
	repository := t.TempDir()
	run := commandRunner(repository)
	if output, code := run("init"); code != 0 {
		t.Fatalf("git init: %s", output)
	}
	path := filepath.Join(repository, "oversized.bin")
	if err := os.WriteFile(path, []byte{0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, MaxUntrackedBytes+1); err != nil {
		t.Skipf("sparse file unsupported: %v", err)
	}
	started := time.Now()
	if _, ok := MutationFingerprint(run); ok {
		t.Fatal("oversized untracked content was accepted")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("oversized content was not rejected before hashing: %s", elapsed)
	}
}

func TestLinesResultDistinguishesEmptyOutputFromCommandFailure(t *testing.T) {
	empty, ok := LinesResult(func(args ...string) (string, int) {
		return "", 0
	}, "status", "--short")
	if !ok || empty == nil || len(empty) != 0 {
		t.Fatalf("empty result = %#v, ok=%v", empty, ok)
	}
	failed, ok := LinesResult(func(args ...string) (string, int) {
		return "[truncated]", 7
	}, "status", "--short")
	if ok || failed != nil {
		t.Fatalf("failed result = %#v, ok=%v", failed, ok)
	}
}

func TestRootResultPreservesCommandFailureClass(t *testing.T) {
	root, code := RootResult(func(args ...string) (string, int) {
		return "[truncated]", 7
	})
	if root != "" || code != 7 {
		t.Fatalf("root = %q, code = %d", root, code)
	}
	root, code = RootResult(func(args ...string) (string, int) {
		return " /repo/root \n", 0
	})
	if root != "/repo/root" || code != 0 {
		t.Fatalf("root = %q, code = %d", root, code)
	}
}

func TestPathsResultPreservesWhitespaceAndNewlines(t *testing.T) {
	paths, ok := PathsResult(func(args ...string) (string, int) {
		return " leading.txt\x00line\nbreak.txt\x00", 0
	}, "ls-files", "-z")
	if !ok || len(paths) != 2 || paths[0] != " leading.txt" || paths[1] != "line\nbreak.txt" {
		t.Fatalf("paths = %#v, ok = %v", paths, ok)
	}
	failed, ok := PathsResult(func(args ...string) (string, int) {
		return "[truncated]", 7
	}, "ls-files", "-z")
	if ok || failed != nil {
		t.Fatalf("failed paths = %#v, ok = %v", failed, ok)
	}
	incomplete, ok := PathsResult(func(args ...string) (string, int) {
		return "missing-terminator", 0
	}, "ls-files", "-z")
	if ok || incomplete != nil {
		t.Fatalf("incomplete paths = %#v, ok = %v", incomplete, ok)
	}
}

func TestStatusResultPreservesPathsAndRenamePairs(t *testing.T) {
	status, ok := StatusResult(func(args ...string) (string, int) {
		return "?? line\nbreak.txt\x00R  new name.txt\x00 old name.txt\x00", 0
	})
	if !ok || len(status) != 2 {
		t.Fatalf("status = %#v, ok = %v", status, ok)
	}
	if status[0] != (StatusEntry{Status: "??", Path: "line\nbreak.txt"}) {
		t.Fatalf("ordinary status = %#v", status[0])
	}
	if status[1] != (StatusEntry{Status: "R ", Path: "new name.txt", OriginalPath: " old name.txt"}) {
		t.Fatalf("rename status = %#v", status[1])
	}
}

func TestStatusResultRejectsMalformedOrIncompleteRecords(t *testing.T) {
	for _, output := range []string{
		"?? missing-terminator",
		"invalid\x00",
		"R  new.txt\x00",
	} {
		status, ok := StatusResult(func(args ...string) (string, int) {
			return output, 0
		})
		if ok || status != nil {
			t.Fatalf("output %q produced %#v, ok = %v", output, status, ok)
		}
	}
}

func commitAll(t *testing.T, run CommandRunner, message string) {
	t.Helper()
	if output, code := run("add", "."); code != 0 {
		t.Fatalf("git add: %s", output)
	}
	if output, code := run("-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", message); code != 0 {
		t.Fatalf("git commit: %s", output)
	}
}

func commandRunner(dir string) CommandRunner {
	return func(args ...string) (string, int) {
		command := exec.Command("git", args...)
		command.Dir = dir
		output, err := command.CombinedOutput()
		if err == nil {
			return string(output), 0
		}
		if exitError, ok := err.(*exec.ExitError); ok {
			return string(output), exitError.ExitCode()
		}
		return string(output) + err.Error(), 1
	}
}
