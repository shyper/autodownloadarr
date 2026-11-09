package movies

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
)

func TestSaveNZB(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nzbs")
	svc := &Services{NZBDir: dir}
	rel := store.Release{IndexerID: 1, GUID: "first", Title: "fallback"}
	raw := []byte("<?xml version=\"1.0\"?><nzb><!-- original bytes --></nzb>")
	must(t, svc.SaveNZB(rel, `../../Original{{password}}.nzb`, raw))
	must(t, svc.SaveNZB(rel, `../../Original{{password}}.nzb`, raw))
	second := rel
	second.GUID = "second"
	must(t, svc.SaveNZB(second, `C:\folder\Original{{password}}.nzb`, raw))
	files, err := filepath.Glob(filepath.Join(dir, "*.nzb"))
	must(t, err)
	if len(files) != 2 {
		t.Fatalf("archive collision or duplicate retry: %v", files)
	}
	for _, file := range files {
		if !strings.HasSuffix(file, "-Original{{password}}.nzb") {
			t.Errorf("original name lost: %s", file)
		}
		saved, err := os.ReadFile(file)
		must(t, err)
		if !bytes.Equal(saved, raw) {
			t.Errorf("document changed: %q", saved)
		}
		info, err := os.Stat(file)
		must(t, err)
		if info.Mode().Perm() != 0600 {
			t.Errorf("NZB permissions: %v", info.Mode())
		}
	}
	entries, err := os.ReadDir(root)
	must(t, err)
	if len(entries) != 1 || entries[0].Name() != "nzbs" {
		t.Fatalf("wrote outside archive: %v", entries)
	}
	// A long, non-ASCII title must still be a valid single filename.
	rel.GUID = "unicode"
	must(t, svc.SaveNZB(rel, strings.Repeat("Ä", 300)+".nzb", raw))
	rel.GUID = "fallback"
	must(t, svc.SaveNZB(rel, "", raw))
}

func TestSaveNZBWriteFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	must(t, os.WriteFile(path, []byte("occupied"), 0600))
	for _, dir := range []string{"", filepath.Join(path, "nzbs")} {
		svc := &Services{NZBDir: dir}
		if err := svc.SaveNZB(store.Release{GUID: "x"}, "x.nzb", []byte("data")); err == nil {
			t.Fatalf("archive path %q unexpectedly succeeded", dir)
		}
	}
}

func TestDownloadArchivesNZBWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "off"
		if enabled {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
			world.post(t, "broken", []rar5.File{{Name: "film.mkv", Data: []byte("movie fixture")}})
			world.items = item("broken", "Film.2024.German.720p.WEB.h264-GRP")
			h := newHarness(t, world)
			h.m.svc.NZBDir = filepath.Join(t.TempDir(), "nzbs")
			g, err := h.st.General(h.ctx)
			must(t, err)
			g.SaveNZBs = enabled
			must(t, h.st.SetGeneral(h.ctx, g))
			// Reject the finished file: the fetched NZB must survive the failed attempt.
			h.m.fetcher.Probe = func(_ context.Context, _ string) error {
				return &fetch.Error{Kind: fetch.KindInvalidVideo, Err: errors.New("invalid fixture")}
			}
			mv := h.requestAndWait(t)
			if mv.State == store.MovieAvailable {
				t.Fatal("expected failed video validation")
			}
			files, err := filepath.Glob(filepath.Join(h.m.svc.NZBDir, "*.nzb"))
			must(t, err)
			if !enabled {
				if len(files) != 0 {
					t.Fatalf("NZBs saved while disabled: %v", files)
				}
				return
			}
			if len(files) != 1 {
				t.Fatalf("NZB not retained after failure: %v", files)
			}
			data, err := os.ReadFile(files[0])
			must(t, err)
			if !bytes.Equal(data, world.nzbs["broken"]) {
				t.Fatal("saved NZB does not match fetched document")
			}
			must(t, h.m.Delete(h.ctx, mv.ID, true))
			if _, err := os.Stat(files[0]); err != nil {
				t.Fatalf("media deletion removed archived NZB: %v", err)
			}
		})
	}
}
