package atomicfile

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePreservesModeAndCreatesBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	result, err := Write(path, []byte("after"), Options{
		Backup:               true,
		DefaultMode:          0o600,
		PreserveExistingMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %#o", info.Mode().Perm())
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "before" {
		t.Fatalf("backup = %q", backup)
	}
}

func TestWriteUsesRequestedPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.json")
	if _, err := Write(path, []byte("{}"), Options{DefaultMode: 0o600}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %#o", info.Mode().Perm())
	}
}

func TestWriteRejectsExistingSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "config.json")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := Write(link, []byte("replacement"), Options{
		Backup:               true,
		DefaultMode:          0o600,
		PreserveExistingMode: true,
	}); err == nil {
		t.Fatal("symlink write unexpectedly succeeded")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside" {
		t.Fatalf("symlink target changed to %q", data)
	}
}

func FuzzAtomicWriteRoundTrip(f *testing.F) {
	f.Add([]byte("fixture"), uint32(0o600))
	f.Add([]byte{}, uint32(0o644))
	f.Fuzz(func(t *testing.T, data []byte, rawMode uint32) {
		if len(data) > 64*1024 {
			return
		}
		mode := os.FileMode(rawMode&0o0777) | 0o600
		sum := sha256.Sum256(data)
		path := filepath.Join(t.TempDir(), fmt.Sprintf("%x-%03o", sum[:], mode.Perm()))
		if _, err := Write(path, data, Options{DefaultMode: mode}); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("atomic round trip changed %d-byte payload", len(data))
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode.Perm() {
			t.Fatalf("mode = %o, want %o", info.Mode().Perm(), mode.Perm())
		}
	})
}
