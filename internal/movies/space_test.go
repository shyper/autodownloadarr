package movies

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/shyper/autodownloadarr/internal/store"
)

func TestSpaceBudgetUnlimited(t *testing.T) {
	dir := t.TempDir()
	g := store.DefaultGeneral()
	g.MoviesPath, g.SeriesPath = dir, t.TempDir()
	svc := &Services{}
	if got := svc.SpaceBudget(g, dir); got != math.MaxInt64 {
		t.Errorf("budget without limits = %d, want MaxInt64", got)
	}
}

func TestSpaceBudgetLibraryCap(t *testing.T) {
	dir, series := t.TempDir(), t.TempDir()
	g := store.DefaultGeneral()
	g.MoviesPath, g.SeriesPath = dir, series
	svc := &Services{}

	// A cap of one byte per byte: the files below use more than that.
	g.MaxLibraryGB = 1 / float64(gib)
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(series, "episode.mkv"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := svc.SpaceBudget(g, dir); got != 0 {
		t.Errorf("budget with a full library = %d, want 0", got)
	}

	g.MaxLibraryGB = 100 / float64(gib) // 100 bytes, 20 in use
	if got, want := svc.SpaceBudget(g, dir), int64(80); got != want {
		t.Errorf("budget = %d, want %d", got, want)
	}
}

func TestSpaceBudgetMinFree(t *testing.T) {
	dir := t.TempDir()
	g := store.DefaultGeneral()
	g.MoviesPath = dir
	svc := &Services{}
	// More free space than any disk has: no download fits.
	g.MinFreeGB = 1e9
	if got := svc.SpaceBudget(g, dir); got != 0 {
		t.Errorf("budget with an unreachable reserve = %d, want 0", got)
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{
		0:        "0 KB",
		5 << 10:  "5 KB",
		42 << 20: "42 MB",
		3 * gib:  "3.0 GB",
	}
	for n, want := range cases {
		if got := FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", n, got, want)
		}
	}
}
