//go:build darwin || linux

package git

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMutationFingerprintRejectsUntrackedSpecialFileWithoutBlocking(t *testing.T) {
	repository := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(repository, "pipe"), 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	run := func(args ...string) (string, int) {
		return "pipe\x00", 0
	}
	started := time.Now()
	if _, ok := untrackedFingerprint(repository, run); ok {
		t.Fatal("untracked special file was accepted")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("special file rejection blocked for %s", elapsed)
	}
}
