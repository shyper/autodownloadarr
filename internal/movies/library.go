package movies

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/mediaserver"
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
	mv.Progress, mv.Base = 0, store.WatchBase{} // a new file: the next poll takes the baseline
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.log.Info("movie available", "movie", mv.Title, "file", video)
	m.publish(mv)
	m.notify(events.Notification{Event: "movie.available", Title: "Movie ready: " + mv.Title, Body: rel.Title})
	m.scan(ctx, mv.Folder)
	return m.scheduleDeletion(ctx, mv)
}

// Observe compares a media server item with the baseline of a file. On the
// first sighting the baseline is taken, unless the item was played after
// since (when the file appeared; it is playable while downloading): then
// that playback counts. Marking the item unwatched in Plex lowers the
// baseline again. It returns whether the file was watched since and how
// far (0..1).
func Observe(base *store.WatchBase, it mediaserver.Item, since time.Time) (watched bool, progress float64) {
	switch {
	case !base.Set && !since.IsZero() && !it.LastViewedAt.Before(since.Truncate(time.Second)): // Plex has seconds
		views := it.ViewCount
		if views > 0 && it.ViewOffset == 0 {
			views-- // just watched to the end
		}
		*base = store.WatchBase{Set: true, Views: views}
	case !base.Set || it.ViewCount < base.Views:
		*base = store.WatchBase{Set: true, Views: it.ViewCount, Offset: it.ViewOffset.Milliseconds()}
	}
	b := mediaserver.Baseline{Views: base.Views, Offset: time.Duration(base.Offset) * time.Millisecond}
	return it.WatchedSince(b), it.ProgressSince(b)
}

// fileSince is when the current file of a movie or episode started to
// appear (the start of its last download), needed only while no baseline
// has been taken.
func (m *Manager) fileSince(ctx context.Context, o store.Owner, base store.WatchBase) time.Time {
	return FileSince(ctx, m.store, o, base)
}

// FileSince is fileSince for other packages.
func FileSince(ctx context.Context, st *store.Store, o store.Owner, base store.WatchBase) time.Time {
	if base.Set {
		return time.Time{}
	}
	attempts, err := st.AttemptsOf(ctx, o)
	if err != nil || len(attempts) == 0 {
		return time.Time{}
	}
	return attempts[0].StartedAt
}

// scan asks the media server to look at a folder; failures are retried.
func (m *Manager) scan(ctx context.Context, folder string) { m.svc.Scan(ctx, false, folder) }

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

// pollWatched reads the watch state from the media server. It runs every minute and
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

// PollWatched compares the available and watched movies with the media server now.
func (m *Manager) PollWatched(ctx context.Context) error {
	target, err := m.svc.Media(ctx)
	if err != nil || target == nil {
		return err
	}
	list, err := m.store.Movies(ctx, store.MovieAvailable, store.MovieWatched)
	if err != nil || len(list) == 0 {
		return err
	}
	items, err := target.Server.Movies(ctx, target.Library)
	if err != nil {
		return err
	}
	// Matched by our own file only: another copy of the movie (e.g. from
	// Radarr) has a watch state of its own.
	byKey, byDir := map[string]mediaserver.Item{}, map[string]mediaserver.Item{}
	for _, it := range items {
		byKey[it.Key] = it
		for _, f := range it.Files {
			byDir[filepath.Dir(target.Mapper.ToLocal(f))] = it
		}
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}

	for _, mv := range list {
		it, ok := byDir[filepath.Clean(mv.Folder)]
		if !ok {
			it, ok = byKey[mv.MediaKey]
		}
		if !ok {
			continue // not scanned yet
		}
		base := mv.Base
		watched, progress := Observe(&mv.Base, it, m.fileSince(ctx, store.ForMovie(mv.ID), mv.Base))
		changed := mv.MediaKey != it.Key || mv.Base != base || math.Abs(progress-mv.Progress) >= 0.01 ||
			(progress != mv.Progress && (progress == 0 || progress == 1))
		mv.MediaKey, mv.Progress = it.Key, progress
		switch {
		case watched && mv.State == store.MovieAvailable:
			now := m.now()
			deleteAt := now.Add(time.Duration(g.DeleteAfterHours) * time.Hour)
			mv.State, mv.WatchedAt, mv.DeleteAt = store.MovieWatched, &now, &deleteAt
			m.log.Info("movie watched", "movie", mv.Title, "delete_at", deleteAt)
			changed = true
		case !watched && mv.State == store.MovieWatched:
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
