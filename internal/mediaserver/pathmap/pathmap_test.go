package pathmap

import (
	"slices"
	"testing"
)

func TestMapping(t *testing.T) {
	m := New([]Mapping{
		{Local: "/filme/", Remote: "/mnt/hdd/filme/"},
		{Local: "/filme/4k", Remote: "/mnt/fast/4k"},
		{Local: "/serien", Remote: `D:\Medien\Serien\`},
	})
	cases := []struct{ local, remote string }{
		{"/filme/Dune (2024)/Dune (2024).mkv", "/mnt/hdd/filme/Dune (2024)/Dune (2024).mkv"},
		{"/filme/4k/Movie/m.mkv", "/mnt/fast/4k/Movie/m.mkv"},
		{"/filme", "/mnt/hdd/filme"},
		{"/serien/Show/Season 01/e.mkv", `D:\Medien\Serien\Show\Season 01\e.mkv`},
		{"/filme-alt/x.mkv", "/filme-alt/x.mkv"}, // no match at a non-boundary
		{"/other/x.mkv", "/other/x.mkv"},
	}
	for _, c := range cases {
		if got := m.ToRemote(c.local); got != c.remote {
			t.Errorf("ToRemote(%q) = %q, want %q", c.local, got, c.remote)
		}
		if got := m.ToLocal(c.remote); got != c.local {
			t.Errorf("ToLocal(%q) = %q, want %q", c.remote, got, c.local)
		}
	}
	if got := m.ToLocal(`d:\medien\serien\Show\e.mkv`); got != "/serien/Show/e.mkv" {
		t.Errorf("Windows paths should match case-insensitively, got %q", got)
	}
}

func TestNoMappingsPassThrough(t *testing.T) {
	m := New(nil)
	if m.ToRemote("/movies/a.mkv") != "/movies/a.mkv" || m.ToLocal("/movies/a.mkv") != "/movies/a.mkv" {
		t.Error("paths changed without mappings")
	}
}

func TestSuggestAndContains(t *testing.T) {
	got := Suggest([]string{"/filme", "/serien"}, []string{"/mnt/hdd/Filme", `D:\Serien`, "/data/music"})
	want := []Mapping{{"/filme", "/mnt/hdd/Filme"}, {"/serien", `D:\Serien`}}
	if !slices.Equal(got, want) {
		t.Errorf("Suggest = %v, want %v", got, want)
	}
	if !Contains([]string{"/mnt/hdd/filme"}, "/mnt/hdd/filme/Dune/x.mkv") || Contains([]string{"/mnt/hdd/filme"}, "/mnt/hdd/filme2/x") {
		t.Error("Contains")
	}
}
