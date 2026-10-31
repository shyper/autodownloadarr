// Package watchlist imports the Plex account's watchlist. Movies and series
// on it are requested automatically, so adding a title in Plex is enough to
// get it.
package watchlist

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shyper/autodownloadarr/internal/mediaserver"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

// Manager polls the watchlist and requests new titles.
type Manager struct {
	store  *store.Store
	svc    *movies.Services
	movies *movies.Manager
	series *series.Manager
	log    *slog.Logger
	now    func() time.Time

	mu   sync.Mutex
	last time.Time
}

// NewManager returns a watchlist poller.
func NewManager(st *store.Store, svc *movies.Services, mv *movies.Manager, se *series.Manager, log *slog.Logger) *Manager {
	return &Manager{store: st, svc: svc, movies: mv, series: se, log: log, now: time.Now}
}

// Poll checks the watchlist if the configured interval has passed.
func (m *Manager) Poll(ctx context.Context) error {
	return m.poll(ctx, false)
}

// PollNow checks the watchlist regardless of the interval.
func (m *Manager) PollNow(ctx context.Context) error {
	return m.poll(ctx, true)
}

func (m *Manager) poll(ctx context.Context, force bool) error {
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	if !g.Watchlist {
		return nil
	}
	interval := time.Duration(max(g.WatchlistPollMinutes, 1)) * time.Minute
	m.mu.Lock()
	due := force || m.last.IsZero() || m.now().Sub(m.last) >= interval-time.Second
	if due {
		m.last = m.now()
	}
	m.mu.Unlock()
	if !due {
		return nil
	}

	ms, found, err := m.store.MediaServer(ctx)
	if err != nil {
		return err
	}
	if !found || mediaserver.Kind(ms.Kind) != mediaserver.Plex || ms.AccountToken == "" {
		return nil
	}
	acc, err := m.svc.PlexAccount(ctx)
	if err != nil {
		return err
	}
	items, err := acc.Watchlist(ctx, ms.AccountToken)
	if err != nil {
		return err
	}
	for _, it := range items {
		id := tmdbID(it.GUIDs)
		if id == 0 {
			m.log.Debug("watchlist item without TMDB id", "title", it.Title, "guids", it.GUIDs)
			continue
		}
		switch it.Type {
		case "movie":
			mv, lookupErr := m.store.MovieByTMDB(ctx, id)
			if lookupErr == nil && mv.WatchedAt != nil {
				key := it.Key
				if it.PlexGUID != "" {
					key = it.PlexGUID[strings.LastIndex(it.PlexGUID, "/")+1:]
				}
				if key == "" {
					continue
				}
				if err := acc.RemoveFromWatchlist(ctx, ms.AccountToken, key); err != nil {
					return err // the next scheduled poll retries the removal
				}
				m.log.Info("watched movie removed from Plex watchlist", "movie", mv.Title)
				continue
			}
			m.requestMovie(ctx, id, it.Title)
		case "show":
			m.requestSeries(ctx, id, it.Title)
		}
	}
	return nil
}

func (m *Manager) requestMovie(ctx context.Context, tmdbID int, title string) {
	if _, err := m.store.MovieByTMDB(ctx, tmdbID); err == nil || !errors.Is(err, store.ErrNotFound) {
		return
	}
	mv, err := m.movies.RequestFromWatchlist(ctx, tmdbID)
	switch {
	case err == nil:
		m.log.Info("movie requested from the Plex watchlist", "movie", mv.Title)
	case errors.Is(err, movies.ErrAlreadyRequested), errors.Is(err, movies.ErrWatchlistExcluded):
	default:
		m.log.Warn("request movie from the Plex watchlist failed", "title", title, "tmdb", tmdbID, "err", err)
	}
}

func (m *Manager) requestSeries(ctx context.Context, tmdbID int, title string) {
	if _, err := m.store.SeriesByTMDB(ctx, tmdbID); err == nil || !errors.Is(err, store.ErrNotFound) {
		return
	}
	se, err := m.series.Request(ctx, tmdbID, 0, series.RequestOptions{})
	switch {
	case err == nil:
		m.log.Info("series requested from the Plex watchlist", "series", se.Title)
	case errors.Is(err, series.ErrAlreadyRequested):
	default:
		m.log.Warn("request series from the Plex watchlist failed", "title", title, "tmdb", tmdbID, "err", err)
	}
}

// tmdbID extracts the TMDB ID from Plex GUIDs (tmdb://693134).
func tmdbID(guids []string) int {
	for _, g := range guids {
		if strings.HasPrefix(g, "tmdb://") {
			id, err := strconv.Atoi(strings.TrimPrefix(g, "tmdb://"))
			if err == nil {
				return id
			}
		}
	}
	return 0
}
