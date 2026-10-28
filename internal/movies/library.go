package movies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/mediaserver/plex"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
)

// available records a finished download and tells the media server.
func (m *Manager) available(ctx context.Context, mv store.Movie, rel store.Release, video string) error {
	info, err := os.Stat(video)
	if err != nil {
		return err
	}
	if err := m.store.TrackFile(ctx, mv.ID, video, info.Size()); err != nil {
		return err
	}
	now := m.now()
	mv.State = store.MovieAvailable
	mv.Folder = filepath.Dir(video)
	mv.ReleaseTitle = rel.Title
	mv.AvailableAt = &now
	mv.Attempts, mv.LastError, mv.NextSearchAt = 0, "", nil
	mv.WatchedAt, mv.DeleteAt = nil, nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.log.Info("movie available", "movie", mv.Title, "file", video)
	m.publish(mv)
	m.scan(ctx, mv.Folder)
	return m.scheduleDeletion(ctx, mv)
}

// scan asks the media server to look at a folder. Failures are logged:
// Plex finds the files on its next scheduled scan anyway.
func (m *Manager) scan(ctx context.Context, folder string) {
	target, err := m.svc.Plex(ctx)
	if err != nil {
		m.log.Warn("plex not usable", "err", err)
		return
	}
	if target == nil {
		return
	}
	remote := target.Mapper.ToRemote(folder)
	if err := target.Client.Scan(ctx, target.Library, remote); err != nil {
		m.log.Warn("plex scan failed", "path", remote, "err", err)
	}
}

func (m *Manager) handleDelete(ctx context.Context, t scheduler.Task) error {
	p, err := decode[deletePayload](t)
	if err != nil {
		return err
	}
	mv, err := m.store.Movie(ctx, p.MovieID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if mv.Keep {
		return nil
	}
	switch {
	case p.Reason == "watched" && mv.State == store.MovieWatched:
	case p.Reason == "expired" && mv.State == store.MovieAvailable:
	default:
		return nil // the state changed meanwhile
	}
	if err := m.removeFiles(ctx, &mv); err != nil {
		return err
	}
	mv.State, mv.DeleteAt = store.MovieDeleted, nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.log.Info("movie deleted", "movie", mv.Title, "reason", p.Reason)
	m.publish(mv)
	return nil
}

// removeFiles deletes the tracked files of a movie. Only files inside the
// movies folder are touched, so a wrong record can never delete anything
// else (e.g. the Radarr library).
func (m *Manager) removeFiles(ctx context.Context, mv *store.Movie) error {
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	files, err := m.store.Files(ctx, mv.ID)
	if err != nil {
		return err
	}
	root := g.MoviesPath
	for _, f := range files {
		if root == "" || !within(root, f) {
			m.log.Error("refusing to delete a file outside the movies folder", "file", f, "root", root)
			continue
		}
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := m.store.UntrackFile(ctx, f); err != nil {
			return err
		}
	}
	if mv.Folder != "" && root != "" && within(root, mv.Folder) {
		fetch.Cleanup(filepath.Join(root, fetch.WorkDirName, "incomplete", strconv.FormatInt(mv.ID, 10)), mv.Folder, nil)
		m.scan(ctx, root) // let Plex notice the removal
	}
	mv.Folder, mv.MediaKey = "", ""
	return nil
}

// within reports whether p lies strictly inside root.
func within(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != "." && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pollWatched reads the watch state from Plex. It runs every minute and
// does the actual work at the configured interval.
func (m *Manager) pollWatched(ctx context.Context) error {
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	due := m.now().Sub(m.lastPoll) >= time.Duration(max(g.WatchPollMinutes, 1))*time.Minute-time.Second
	if due {
		m.lastPoll = m.now()
	}
	m.mu.Unlock()
	if !due {
		return nil
	}
	return m.PollWatched(ctx)
}

// PollWatched compares the available and watched movies with Plex now.
func (m *Manager) PollWatched(ctx context.Context) error {
	target, err := m.svc.Plex(ctx)
	if err != nil || target == nil {
		return err
	}
	list, err := m.store.Movies(ctx, store.MovieAvailable, store.MovieWatched)
	if err != nil || len(list) == 0 {
		return err
	}
	items, err := target.Client.Movies(ctx, target.Library)
	if err != nil {
		return err
	}
	byKey, byDir, byTMDB := map[string]plex.Item{}, map[string]plex.Item{}, map[string]plex.Item{}
	for _, it := range items {
		byKey[it.RatingKey] = it
		for _, f := range it.Files {
			byDir[filepath.Dir(target.Mapper.ToLocal(f))] = it
		}
		for _, guid := range it.GUIDs {
			if id, ok := strings.CutPrefix(guid, "tmdb://"); ok {
				byTMDB[id] = it
			}
		}
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}

	for _, mv := range list {
		it, ok := byKey[mv.MediaKey]
		if !ok {
			it, ok = byDir[filepath.Clean(mv.Folder)]
		}
		if !ok {
			it, ok = byTMDB[strconv.Itoa(mv.TMDBID)]
		}
		if !ok {
			continue // not scanned yet
		}
		changed := mv.MediaKey != it.RatingKey
		mv.MediaKey = it.RatingKey
		switch {
		case it.Watched() && mv.State == store.MovieAvailable:
			watched := it.LastViewedAt
			if watched.IsZero() {
				watched = m.now()
			}
			deleteAt := watched.Add(time.Duration(g.DeleteAfterHours) * time.Hour)
			mv.State, mv.WatchedAt, mv.DeleteAt = store.MovieWatched, &watched, &deleteAt
			m.log.Info("movie watched", "movie", mv.Title, "delete_at", deleteAt)
			changed = true
		case !it.Watched() && mv.State == store.MovieWatched:
			// Marked as unwatched in Plex: keep it again.
			mv.State, mv.WatchedAt, mv.DeleteAt = store.MovieAvailable, nil, nil
			changed = true
		}
		if !changed {
			continue
		}
		if err := m.store.UpdateMovie(ctx, mv); err != nil {
			return err
		}
		if err := m.scheduleDeletion(ctx, mv); err != nil {
			return err
		}
		m.publish(mv)
	}
	return nil
}
