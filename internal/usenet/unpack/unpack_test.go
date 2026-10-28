package unpack

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
)

func movie() []byte {
	b := make([]byte, 50_000)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestExtractSingleAndMultiVolume(t *testing.T) {
	files := []rar5.File{
		{Name: "Sub", Dir: true},
		{Name: "Sub/movie.mkv", Data: movie()},
		{Name: "readme.txt", Data: []byte("hello\n")},
	}
	for _, volumeSize := range []int{0, 12_000} {
		src, dest := t.TempDir(), t.TempDir()
		volumes, err := rar5.Write(src, "Movie.2024", files, volumeSize)
		if err != nil {
			t.Fatal(err)
		}

		found, err := FindRARArchives(src)
		if err != nil || len(found) != 1 || found[0] != volumes[0] {
			t.Fatalf("FindRARArchives = %v, %v; want [%s]", found, err, volumes[0])
		}

		res, err := ExtractRAR(context.Background(), found[0], dest, "")
		if err != nil {
			t.Fatalf("volume size %d: %v", volumeSize, err)
		}
		got, _ := os.ReadFile(filepath.Join(dest, "Sub", "movie.mkv"))
		if !bytes.Equal(got, files[1].Data) {
			t.Errorf("volume size %d: movie differs", volumeSize)
		}
		want := []string{filepath.Join("Sub", "movie.mkv"), "readme.txt"}
		if !slices.Equal(res.Files, want) {
			t.Errorf("Files = %v, want %v", res.Files, want)
		}
		if !slices.Equal(res.Volumes, volumes) {
			t.Errorf("Volumes = %v, want %v", res.Volumes, volumes)
		}
	}
}

func TestFirstVolumes(t *testing.T) {
	got := FirstVolumes([]string{"b.rar", "a.part02.rar", "a.part01.rar", "b.r00", "c.PART001.RAR", "d.part10.rar", "x.mkv"})
	if want := []string{"a.part01.rar", "b.rar", "c.PART001.RAR"}; !slices.Equal(got, want) {
		t.Errorf("FirstVolumes = %v, want %v", got, want)
	}
}

func TestFindRARArchivesNaming(t *testing.T) {
	dir := t.TempDir()
	rar := []byte("Rar!\x1a\x07\x01\x00rest")
	for _, name := range []string{"a.part01.rar", "a.part02.rar", "b.rar", "b.r00", "c.part1.rar", "fake.rar", "notes.txt"} {
		data := rar
		if name == "fake.rar" || name == "notes.txt" {
			data = []byte("not an archive")
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	found, err := FindRARArchives(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range found {
		names = append(names, filepath.Base(f))
	}
	if want := []string{"a.part01.rar", "b.rar", "c.part1.rar"}; !slices.Equal(names, want) {
		t.Errorf("found %v, want %v", names, want)
	}
}

func TestUnsafePathsAreRejected(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	volumes, err := rar5.Write(src, "evil", []rar5.File{{Name: "../escape.txt", Data: []byte("x")}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractRAR(context.Background(), volumes[0], dest, ""); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); !os.IsNotExist(err) {
		t.Error("file written outside dest")
	}
}

func TestCorruptArchive(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	volumes, _ := rar5.Write(src, "x", []rar5.File{{Name: "f.bin", Data: movie()}}, 0)
	data, _ := os.ReadFile(volumes[0])
	data[len(data)-100] ^= 0xff // flip a byte inside the stored file data
	_ = os.WriteFile(volumes[0], data, 0o644)

	if _, err := ExtractRAR(context.Background(), volumes[0], dest, ""); err == nil {
		t.Fatal("corrupt archive extracted without error")
	}
	if _, err := os.Stat(filepath.Join(dest, "f.bin")); !os.IsNotExist(err) {
		t.Error("partial file left behind")
	}
}

func TestCancel(t *testing.T) {
	src := t.TempDir()
	volumes, _ := rar5.Write(src, "x", []rar5.File{{Name: "f.bin", Data: movie()}}, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExtractRAR(ctx, volumes[0], t.TempDir(), ""); err == nil {
		t.Error("cancelled extraction succeeded")
	}
}
