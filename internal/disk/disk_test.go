package disk

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSize(t *testing.T) {
	root := t.TempDir()
	if got := Size(root); got != 0 {
		t.Errorf("size of an empty folder = %d", got)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int{"a.mkv": 10, "sub/b.srt": 5} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := Size(root), int64(15); got != want {
		t.Errorf("size = %d, want %d", got, want)
	}
	if got := Size(filepath.Join(root, "missing")); got != 0 {
		t.Errorf("size of a missing folder = %d", got)
	}
}

func TestFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("free space is not available on Windows")
	}
	free, err := Free(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Error("free space = 0")
	}
	if _, err := Free(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("free space of a missing folder did not fail")
	}
}
