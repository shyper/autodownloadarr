package fetch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func prepareDirs(root, work, folder string) error {
	hidden := filepath.Join(root, WorkDirName)
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		return err
	}
	// Keep media servers out of incomplete downloads.
	for name, content := range map[string]string{".plexignore": "*\n", ".ignore": ""} {
		p := filepath.Join(hidden, name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(folder, 0o755)
}

// Cleanup removes what a failed attempt left behind.
func Cleanup(work, folder string, extracted []string) {
	_ = os.RemoveAll(work)
	for _, f := range extracted {
		_ = os.Remove(f)
	}
	if partials, _ := filepath.Glob(filepath.Join(folder, "*.partial")); len(partials) > 0 {
		for _, p := range partials {
			_ = os.Remove(p)
		}
	}
	_ = os.Remove(folder) // only if empty
}

var videoExtensions = []string{".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".ts", ".m2ts", ".webm", ".mpg", ".mpeg"}

func IsVideo(name string) bool {
	return slices.Contains(videoExtensions, strings.ToLower(filepath.Ext(name)))
}

func IsSample(name string) bool {
	lower := strings.ToLower(filepath.ToSlash(name))
	for _, part := range strings.FieldsFunc(lower, func(r rune) bool { return r == '/' || r == '.' || r == '-' || r == '_' || r == ' ' }) {
		if part == "sample" {
			return true
		}
	}
	return false
}

func LargestVideo(files []string) string {
	var best string
	var size int64 = -1
	for _, f := range files {
		if !IsVideo(f) {
			continue
		}
		if info, err := os.Stat(f); err == nil && info.Size() > size {
			best, size = f, info.Size()
		}
	}
	return best
}
