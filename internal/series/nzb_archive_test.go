package series

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shyper/autodownloadarr/internal/store"
)

func TestEpisodeAndSeasonPackArchiveNZB(t *testing.T) {
	for _, pack := range []bool{false, true} {
		name := "episode"
		key := 1
		if pack {
			name = "season pack"
			key = 0
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			w := newWorld(t)
			if pack {
				w.postPack(t, "Show.S01", []int{1, 2})
			} else {
				w.post(t, 1)
			}
			m, st, root := setup(ctx, t, w, nil)
			m.svc.NZBDir = filepath.Join(t.TempDir(), "nzbs")
			g, err := st.General(ctx)
			must(t, err)
			g.SaveNZBs = true
			indexers, err := st.Indexers(ctx)
			must(t, err)
			rel := store.Release{IndexerID: indexers[0].ID, GUID: name, Title: "Show.S01E01.German.720p.WEB.h264-GRP", NZBURL: indexers[0].URL + "/getnzb/" + strconv.Itoa(key)}
			result, kind, err := m.fetchRelease(ctx, g, store.Series{Title: "Show"}, rel, filepath.Join(root, "output"), "", "archive-test", nil, nil)
			if err != nil || kind != "" || len(result.Videos) == 0 {
				t.Fatalf("fetch: %+v %s %v", result, kind, err)
			}
			files, err := filepath.Glob(filepath.Join(m.svc.NZBDir, "*.nzb"))
			must(t, err)
			if len(files) != 1 {
				t.Fatalf("archive files: %v", files)
			}
			data, err := os.ReadFile(files[0])
			must(t, err)
			if !bytes.Equal(data, w.nzbs[key]) {
				t.Fatal("saved NZB differs from the original")
			}
		})
	}
}
