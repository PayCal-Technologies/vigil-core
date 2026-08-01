package releasearchive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteIsReproducibleAndNormalizesArchiveMetadata(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "completions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "vigil"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "completions", "vigil.bash"), []byte("completion"), 0o600); err != nil {
		t.Fatal(err)
	}
	modTime := time.Unix(1_700_000_000, 0).UTC()
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")
	options := Options{Source: source, ArchiveRoot: "vigil_1.0.0_linux_amd64", ModTime: modTime}
	options.Output = first
	if err := Write(options); err != nil {
		t.Fatal(err)
	}
	options.Output = second
	if err := Write(options); err != nil {
		t.Fatal(err)
	}
	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("archives differ for identical inputs")
	}

	gzipReader, err := gzip.NewReader(bytes.NewReader(firstData))
	if err != nil {
		t.Fatal(err)
	}
	tarReader := tar.NewReader(gzipReader)
	foundBinary := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !header.ModTime.Equal(modTime) || header.Uid != 0 || header.Gid != 0 {
			t.Fatalf("non-reproducible header metadata: %#v", header)
		}
		if header.Name == "vigil_1.0.0_linux_amd64/vigil" {
			foundBinary = true
			if header.Mode != 0o755 {
				t.Fatalf("binary mode = %#o", header.Mode)
			}
		}
	}
	if !foundBinary {
		t.Fatal("binary missing from archive")
	}
}

func TestWriteRejectsSymlinks(t *testing.T) {
	source := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(source, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	err := Write(Options{
		Source:      source,
		ArchiveRoot: "release",
		Output:      filepath.Join(t.TempDir(), "release.tar.gz"),
		ModTime:     time.Unix(1, 0),
	})
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
}
