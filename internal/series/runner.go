package series

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/download"
)

var (
	errSkipped    = errors.New("skipped by user")
	errSuperseded = errors.New("replaced by another release")
	errDeleted    = errors.New("series deleted")
)

const kindNZBUnavailable = "nzb_unavailable"

func (m *Manager) cancelDownload(id int64, cause error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.active[id]
	if ok {
		a.cancel(cause)
	}
	return ok
}

func (m *Manager) runDownload(ctx context.Context, e store.Episode, rel store.Release, done chan struct{}) {
	defer func() {
		m.mu.Lock()
		delete(m.active, e.ID)
		m.mu.Unlock()
		close(done)
		m.startQueued()
	}()
	bg := context.WithoutCancel(ctx)
	se, err := m.store.Series(bg, e.SeriesID)
	if err != nil {
		m.log.Error("read series", "err", err)
		return
	}
	attemptID, err := m.store.StartAttemptOf(bg, store.ForEpisode(e.ID), rel)
	if err != nil {
		m.log.Error("record attempt", "err", err)
		return
	}
	e.State, e.ReleaseTitle = store.MovieDownloading, rel.Title
	if err := m.store.UpdateEpisode(bg, e); err != nil {
		m.log.Error("update episode", "err", err)
		return
	}
	m.publishEpisode(e)
	m.log.Info("episode download started", "series", se.Title, "season", e.Season, "episode", e.Episode, "release", rel.Title)

	path, kind, dlErr := m.download(ctx, se, e, rel)
	if err := m.finish(bg, ctx, se, e, rel, attemptID, path, kind, dlErr); err != nil {
		m.log.Error("finish download", "err", err)
	}
}

// download fetches and extracts one episode and returns the final path.
func (m *Manager) download(ctx context.Context, se store.Series, e store.Episode, rel store.Release) (string, string, error) {
	g, err := m.store.General(ctx)
	if err != nil {
		return "", fetch.KindDiskError, err
	}
	if g.SeriesPath == "" {
		return "", CodeNoSeriesPath, nil
	}
	pool, err := m.svc.Pool(ctx)
	if errors.Is(err, movies.ErrNotConfigured) {
		return "", CodeNoUsenet, nil
	} else if err != nil {
		return "", fetch.KindDownloadError, err
	}
	ic, err := m.svc.Indexer(ctx, rel.IndexerID)
	if err != nil {
		return "", kindNZBUnavailable, err
	}
	data, nzbName, err := ic.Client.FetchNZB(ctx, rel.NZBURL)
	switch {
	case ctx.Err() != nil:
		return "", fetch.KindCancelled, context.Cause(ctx)
	case errors.Is(err, httpx.ErrRateLimited), errors.Is(err, httpx.ErrUnavailable), errors.Is(err, httpx.ErrUnauthorized):
		return "", CodeIndexerError, err
	case err != nil:
		return "", kindNZBUnavailable, err
	}
	list, err := m.store.ArchivePasswords(ctx)
	if err != nil {
		return "", fetch.KindDiskError, err
	}
	passwords := make([]string, len(list))
	for i, p := range list {
		passwords[i] = p.Password
	}
	m.limiter.SetRate(int64(g.MaxDownloadMBps * (1 << 20)))

	root := g.SeriesPath
	folder := filepath.Join(root, SeriesFolder(se), SeasonFolder(e.Season))
	// Plex learns about the episode as soon as the first part is extracted,
	// so that it can be watched while the rest is still downloading.
	var visible atomic.Bool
	res, err := m.fetcher.Fetch(ctx, fetch.Request{
		Pool: pool, NZB: data, NZBName: nzbName, ReleaseTitle: rel.Title, Root: root,
		WorkDir:   filepath.Join(root, fetch.WorkDirName, "incomplete", "e"+strconv.FormatInt(e.ID, 10)),
		Folder:    folder,
		VideoName: EpisodeFileName(se, e),
		FullCheck: m.svc.FullCheck(ctx),
		OnVisible: func(path string) {
			visible.Store(true)
			m.markPlayable(e.ID)
			m.log.Info("episode playable while downloading", "series", se.Title, "file", path)
			go m.scan(context.WithoutCancel(ctx), folder)
		},
		Passwords: passwords,
		Limiter:   m.limiter,
		OnProgress: func(p download.Progress) {
			m.progress(e, p)
		},
	})
	if err != nil && visible.Load() {
		m.scan(context.WithoutCancel(ctx), filepath.Join(root, SeriesFolder(se))) // the file is gone again
	}
	var fe *fetch.Error
	if errors.As(err, &fe) {
		return "", fe.Kind, fe.Err
	} else if err != nil {
		return "", fetch.KindDiskError, err
	}
	if res.Password != "" {
		_ = m.store.ArchivePasswordHit(ctx, res.Password)
	}
	main := fetch.LargestVideo(res.Videos)
	final := filepath.Join(folder, EpisodeFileName(se, e)+strings.ToLower(filepath.Ext(main)))
	if main != final {
		if err := os.Rename(main, final); err != nil {
			fetch.Cleanup("", folder, res.Videos)
			return "", fetch.KindDiskError, err
		}
	}
	for _, f := range res.Videos {
		if f != main {
			_ = os.Remove(f)
		}
	}
	return final, "", nil
}

func (m *Manager) progress(e store.Episode, p download.Progress) {
	m.mu.Lock()
	d := Download{EpisodeID: e.ID, SeriesID: e.SeriesID, Season: e.Season, Episode: e.Episode, Progress: p}
	if a, ok := m.active[e.ID]; ok {
		a.progress = p
		d.ReleaseTitle, d.Playable = a.release.Title, a.playable
	}
	m.mu.Unlock()
	m.bus.Publish(movies.EventDownloadProgress, d)
}

// markPlayable records that the episode can be watched while its download
// continues and tells the UI right away instead of waiting for the next
// progress tick.
func (m *Manager) markPlayable(episodeID int64) {
	m.mu.Lock()
	a, ok := m.active[episodeID]
	if !ok {
		m.mu.Unlock()
		return
	}
	a.playable = true
	d := Download{EpisodeID: episodeID, SeriesID: a.episode.SeriesID, Season: a.episode.Season, Episode: a.episode.Episode,
		ReleaseTitle: a.release.Title, Progress: a.progress, Playable: true}
	m.mu.Unlock()
	m.bus.Publish(movies.EventDownloadProgress, d)
}

func (m *Manager) finish(ctx, dctx context.Context, se store.Series, e store.Episode, rel store.Release, attemptID int64,
	path, kind string, dlErr error) error {
	msg := ""
	if dlErr != nil {
		msg = dlErr.Error()
	}
	cur, err := m.store.Episode(ctx, e.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // series deleted
	}
	if err != nil {
		return err
	}
	e = cur

	switch {
	case kind == "":
		if err := m.store.FinishAttempt(ctx, attemptID, "success", "", ""); err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := m.store.TrackFileOf(ctx, store.ForEpisode(e.ID), path, info.Size()); err != nil {
			return err
		}
		now := m.now()
		e.State, e.File, e.AvailableAt, e.Attempts, e.LastError = store.MovieAvailable, path, &now, 0, ""
		// A new file: earlier viewing does not count, the next poll takes
		// the baseline.
		e.Progress, e.StartedAt, e.WatchedAt, e.DeleteAt, e.Base = 0, nil, nil, nil, store.WatchBase{}
		if err := m.store.UpdateEpisode(ctx, e); err != nil {
			return err
		}
		m.log.Info("episode available", "series", se.Title, "file", path)
		m.publishEpisode(e)
		m.notify(events.Notification{
			Event: "episode.available",
			Title: fmt.Sprintf("%s S%02dE%02d is ready", se.Title, e.Season, e.Episode),
			Body:  rel.Title,
		})
		m.scan(ctx, filepath.Dir(path))
		return m.Reconcile(ctx, se.ID)

	case kind == fetch.KindCancelled && !errors.Is(context.Cause(dctx), errSkipped):
		return m.store.FinishAttempt(ctx, attemptID, "cancelled", kind, msg)

	case kind == CodeNoSeriesPath || kind == CodeNoUsenet || kind == fetch.KindUsenetAuth || kind == fetch.KindDiskError:
		if err := m.store.FinishAttempt(ctx, attemptID, "failed", kind, msg); err != nil {
			return err
		}
		return m.fail(ctx, e, kind, msg)

	case kind == fetch.KindDownloadError || kind == CodeIndexerError:
		if err := m.store.FinishAttempt(ctx, attemptID, "failed", kind, msg); err != nil {
			return err
		}
		return m.wait(ctx, e, 0, kind, msg)
	}

	if kind == fetch.KindCancelled {
		kind = "skipped"
	}
	m.log.Warn("episode release failed", "release", rel.Title, "kind", kind, "err", dlErr)
	if err := m.store.FinishAttempt(ctx, attemptID, "failed", kind, msg); err != nil {
		return err
	}
	if err := m.store.Blocklist(ctx, store.BlocklistEntry{EpisodeID: e.ID, Fingerprint: rel.Fingerprint, Title: rel.Title,
		Reason: kind, Message: msg}); err != nil {
		return err
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	e.Attempts++
	if e.Attempts >= max(g.MaxSources, 1) {
		return m.fail(ctx, e, CodeSourcesExhausted, strconv.Itoa(e.Attempts))
	}
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return err
	}
	if _, ok, err := m.chooseRelease(ctx, e.ID, 0); err != nil {
		return err
	} else if ok {
		return m.enqueue(ctx, e)
	}
	e.State = store.MovieSearching
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return err
	}
	m.publishEpisode(e)
	return m.scheduleSearch(ctx, e.ID, 1, m.now())
}

// SeriesFolder is the library folder of a series, with the TVDB (or TMDB)
// ID as a hint for Plex and Jellyfin.
func SeriesFolder(se store.Series) string {
	name := se.Title
	if se.Year > 0 {
		name += " (" + strconv.Itoa(se.Year) + ")"
	}
	if se.TVDBID > 0 {
		name += " {tvdb-" + strconv.Itoa(se.TVDBID) + "}"
	} else {
		name += " {tmdb-" + strconv.Itoa(se.TMDBID) + "}"
	}
	return sanitize(name)
}

// SeasonFolder is "Season 01" (specials: "Season 00").
func SeasonFolder(season int) string { return fmt.Sprintf("Season %02d", season) }

// EpisodeFileName is "Show (Year) - S01E02 - Title" without extension.
func EpisodeFileName(se store.Series, e store.Episode) string {
	name := se.Title
	if se.Year > 0 {
		name += " (" + strconv.Itoa(se.Year) + ")"
	}
	name += fmt.Sprintf(" - S%02dE%02d", e.Season, e.Episode)
	if e.Title != "" {
		name += " - " + e.Title
	}
	return sanitize(name)
}

func sanitize(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return -1
		}
		if r < 32 {
			return -1
		}
		return r
	}, name)
	if len(name) > 180 {
		name = name[:180]
	}
	return strings.TrimSpace(name)
}
