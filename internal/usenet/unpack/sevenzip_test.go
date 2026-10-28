package unpack

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func make7z(t *testing.T, dir, name, password string) {
	t.Helper()
	tool, err := SevenZip()
	if err != nil {
		t.Skip("7-Zip not installed")
	}
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "Movie"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "Movie", "movie.mkv"), movie(), 0o644)
	args := []string{"a", "-bd", filepath.Join(dir, name), "Movie"}
	if password != "" {
		args = append(args, "-p"+password)
	}
	cmd := exec.CommandContext(t.Context(), tool, args...)
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("7z a: %v\n%s", err, out)
	}
}

func TestExtract7zAndZip(t *testing.T) {
	for _, name := range []string{"movie.7z", "movie.zip"} {
		dir, dest := t.TempDir(), t.TempDir()
		make7z(t, dir, name, "")
		found, err := Find7zArchives(dir)
		if err != nil || len(found) != 1 {
			t.Fatalf("%s: found %v %v", name, found, err)
		}
		res, err := Extract7z(context.Background(), found[0], dest, "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(res.Files, []string{filepath.Join("Movie", "movie.mkv")}) {
			t.Errorf("%s: files = %v", name, res.Files)
		}
	}
}

func TestExtract7zPassword(t *testing.T) {
	dir := t.TempDir()
	make7z(t, dir, "secret.7z", "geheim")
	archive := filepath.Join(dir, "secret.7z")

	if _, err := Extract7z(context.Background(), archive, t.TempDir(), ""); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("no password: %v", err)
	}
	if _, err := Extract7z(context.Background(), archive, t.TempDir(), "falsch"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := Extract7z(context.Background(), archive, t.TempDir(), "geheim"); err != nil {
		t.Errorf("right password: %v", err)
	}
}

func TestFind7zIgnoresOtherParts(t *testing.T) {
	dir := t.TempDir()
	sig := []byte("7z\xbc\xaf\x27\x1crest")
	for _, n := range []string{"a.7z.001", "a.7z.002", "fake.zip"} {
		data := sig
		if n == "fake.zip" {
			data = []byte("nope")
		}
		_ = os.WriteFile(filepath.Join(dir, n), data, 0o644)
	}
	found, _ := Find7zArchives(dir)
	if len(found) != 1 || filepath.Base(found[0]) != "a.7z.001" {
		t.Errorf("found = %v", found)
	}
}
