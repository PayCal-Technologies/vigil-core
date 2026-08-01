package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Result struct {
	BackupPath string `json:"backup_path,omitempty"`
}

type Options struct {
	Backup               bool
	DefaultMode          os.FileMode
	PreserveExistingMode bool
}

func Write(path string, data []byte, options Options) (Result, error) {
	result := Result{}
	dir := filepath.Dir(path)
	info, statErr := os.Lstat(path)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("refusing to replace symlink: %s", path)
	}
	if statErr != nil && !os.IsNotExist(statErr) {
		return result, statErr
	}
	mode := options.DefaultMode
	if mode == 0 {
		mode = 0o644
	}
	if options.PreserveExistingMode && statErr == nil {
		mode = info.Mode().Perm()
	}
	if options.Backup && statErr == nil {
		backupPath := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102T150405.000000000Z"))
		existing, err := os.ReadFile(path)
		if err != nil {
			return result, err
		}
		if err := os.WriteFile(backupPath, existing, mode); err != nil {
			return result, err
		}
		result.BackupPath = backupPath
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return result, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return result, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return result, err
	}
	if err := tmp.Close(); err != nil {
		return result, err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return result, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return result, err
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return result, nil
}
