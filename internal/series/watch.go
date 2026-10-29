package series

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/mediaserver"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
)

// pollWatched reads progress and watch state of the episodes from the media server. It
// runs every minute so that the next episode is fetched soon after the
// current one was started.
func (m *Manager) pollWatched(ctx context.Context) error {
	return m.PollWatched(ctx)
}

// PollWatched compares the episodes on disk with the media server now.
func (m *Manager) PollWatched(ctx context.Context) error {
	target, err := m.svc.MediaTV(ctx)
	if err != nil || target == nil {
		return err
	}
	eps, err := m.store.EpisodesInState(ctx, store.MovieAvailable, store.MovieWatched)
	if err != nil || len(eps) == 0 {
		return err
	}
	items, err := target.Server.Episodes(ctx, target.Library)
	if err != nil {
		return err
	}
	byKey, byFile := map[string]mediaserver.Item{}, map[string]mediaserver.Item{}
	for _, it := range items {
		byKey[it.Key] = it
		for _, f := range it.Files {
			byFile[filepath.Clean(target.Mapper.ToLocal(f))] = it
		}
	}

	now := m.now()
	changedSeries := map[int64]bool{}
	for _, e := range eps {
		it, ok := byFile[filepath.Clean(e.File)]
		if !ok {
			it, ok = byKey[e.MediaKey]
		}
		if !ok {
			continue // not scanned yet
		}
		base := e.Base
		watched, progress := movies.Observe(&e.Base, it, movies.FileSince(ctx, m.store, store.ForEpisode(e.ID), e.Base))
		changed := e.MediaKey != it.Key || e.Base != base
		e.MediaKey = it.Key
		if progress != e.Progress {
			e.Progress, changed = progress, true
		}
		if e.Progress > 0 && e.StartedAt == nil {
			e.StartedAt, changed = &now, true
		}
		switch {
		case watched && e.WatchedAt == nil:
			e.WatchedAt, e.State, changed = &now, store.MovieWatched, true
		case !watched && e.State == store.MovieWatched:
			e.WatchedAt, e.State, e.DeleteAt, changed = nil, store.MovieAvailable, nil, true
		}
		if !changed {
			continue
		}
		if err := m.store.UpdateEpisode(ctx, e); err != nil {
			return err
		}
		m.publishEpisode(e)
		changedSeries[e.SeriesID] = true
	}
	for id := range changedSeries {
		if err := m.Reconcile(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// scan asks the media server to look at a folder of the TV library.
func (m *Manager) scan(ctx context.Context, folder string) { m.svc.Scan(ctx, true, folder) }

// removeFiles deletes the tracked files of an episode, only inside the
// series folder.
func (m *Manager) removeFiles(ctx context.Context, se store.Series, e *store.Episode) error {
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	files, err := m.store.FilesOf(ctx, store.ForEpisode(e.ID))
	if err != nil {
		return err
	}
	root := g.SeriesPath
	for _, f := range files {
		if root == "" || !within(root, f) {
			m.log.Error("refusing to delete a file outside the series folder", "file", f, "root", root)
			continue
		}
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := m.store.UntrackFile(ctx, f); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(f)) // season folder, if now empty
	}
	if root != "" {
		// The download may have been cancelled before it tracked a file.
		_ = os.RemoveAll(filepath.Join(root, fetch.WorkDirName, "incomplete", "e"+strconv.FormatInt(e.ID, 10)))
		if len(files) > 0 {
			m.scan(ctx, filepath.Join(root, SeriesFolder(se)))
		}
	}
	e.File, e.MediaKey = "", ""
	return nil
}

// cleanupFolder removes the empty folders of a deleted series.
func (m *Manager) cleanupFolder(ctx context.Context, se store.Series) {
	g, err := m.store.General(ctx)
	if err != nil || g.SeriesPath == "" {
		return
	}
	dir := filepath.Join(g.SeriesPath, SeriesFolder(se))
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	_ = os.Remove(dir)
	m.scan(ctx, g.SeriesPath)
}

func within(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != "." && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
