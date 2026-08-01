package releasearchive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Options struct {
	Source      string
	ArchiveRoot string
	Output      string
	ModTime     time.Time
}

func Write(options Options) error {
	if strings.TrimSpace(options.ArchiveRoot) == "" || strings.Contains(options.ArchiveRoot, "/") || strings.Contains(options.ArchiveRoot, `\`) {
		return fmt.Errorf("archive root must be one path segment")
	}
	info, err := os.Stat(options.Source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("archive source is not a directory: %s", options.Source)
	}
	modTime := options.ModTime.UTC()
	if modTime.IsZero() {
		return fmt.Errorf("archive modification time is required")
	}

	var paths []string
	err = filepath.WalkDir(options.Source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != options.Source {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)

	outputDir := filepath.Dir(options.Output)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	output, err := os.CreateTemp(outputDir, "."+filepath.Base(options.Output)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := output.Name()
	defer func() {
		_ = output.Close()
		_ = os.Remove(tempPath)
	}()

	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = modTime
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)

	if err := writeHeader(tarWriter, options.ArchiveRoot+"/", info, modTime, true); err != nil {
		return closeWriters(tarWriter, gzipWriter, output, err)
	}
	for _, path := range paths {
		relative, err := filepath.Rel(options.Source, path)
		if err != nil {
			return closeWriters(tarWriter, gzipWriter, output, err)
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return closeWriters(tarWriter, gzipWriter, output, err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return closeWriters(tarWriter, gzipWriter, output, fmt.Errorf("release archives do not accept symlinks: %s", relative))
		}
		name := filepath.ToSlash(filepath.Join(options.ArchiveRoot, relative))
		if entryInfo.IsDir() {
			name += "/"
		}
		if err := writeHeader(tarWriter, name, entryInfo, modTime, entryInfo.IsDir()); err != nil {
			return closeWriters(tarWriter, gzipWriter, output, err)
		}
		if !entryInfo.Mode().IsRegular() {
			if !entryInfo.IsDir() {
				return closeWriters(tarWriter, gzipWriter, output, fmt.Errorf("unsupported release archive entry: %s", relative))
			}
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return closeWriters(tarWriter, gzipWriter, output, err)
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		if copyErr != nil {
			return closeWriters(tarWriter, gzipWriter, output, copyErr)
		}
		if closeErr != nil {
			return closeWriters(tarWriter, gzipWriter, output, closeErr)
		}
	}
	if err := closeWriters(tarWriter, gzipWriter, output, nil); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return err
	}
	return os.Rename(tempPath, options.Output)
}

func writeHeader(writer *tar.Writer, name string, info os.FileInfo, modTime time.Time, directory bool) error {
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name
	header.ModTime = modTime
	header.AccessTime = time.Time{}
	header.ChangeTime = time.Time{}
	header.Uid = 0
	header.Gid = 0
	header.Uname = ""
	header.Gname = ""
	header.Format = tar.FormatUSTAR
	if directory {
		header.Mode = 0o755
	} else if info.Mode()&0o111 != 0 {
		header.Mode = 0o755
	} else {
		header.Mode = 0o644
	}
	return writer.WriteHeader(header)
}

func closeWriters(tarWriter *tar.Writer, gzipWriter *gzip.Writer, output *os.File, prior error) error {
	tarErr := tarWriter.Close()
	gzipErr := gzipWriter.Close()
	syncErr := output.Sync()
	closeErr := output.Close()
	for _, err := range []error{prior, tarErr, gzipErr, syncErr, closeErr} {
		if err != nil {
			return err
		}
	}
	return nil
}
