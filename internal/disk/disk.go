// Package disk reports how much space the temporary download folders use and
// how much is left on their file system.
package disk

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Size returns the total size of the regular files below root. A missing
// folder counts as empty, so callers can use it before the folder exists.
func Size(root string) int64 {
	if root == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// EnsureDir creates dir if it does not exist yet.
func EnsureDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
