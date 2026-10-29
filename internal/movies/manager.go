// Package movies implements the movie lifecycle: request → search → pick a
// release → download and extract → add to the media server → notice it was
// watched → delete it. Failures switch to the next release automatically
// (docs/PLAN.md sections 5 and 9).
package movies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/download"
)

// Task types handled by the manager.
const (
	taskSearch = "movie.search"
	taskDelete = "movie.delete"
)

// Event types published on the bus.
const (
	EventMovieUpdated     = "movie.updated"
	EventMovieRemoved     = "movie.removed"
	EventDownloadProgress = "download.progress"
)

// Error codes stored in Movie.LastError; the web UI translates them.
const (
	CodeNoRelease        = "no_release"
	CodeNoIndexer        = "no_indexer"
	CodeIndexerError     = "indexer_error"
	CodeNoUsenet         = "no_usenet"
	CodeUsenetAuth       = "usenet_auth"
	CodeNoMoviesPath     = "no_movies_path"
	CodeSourcesExhausted = "sources_exhausted"
	CodeDownloadError    = "download_error"
	CodeDiskError        = "disk_error"
)

// ErrAlreadyRequested is returned when a movie is requested twice.
var ErrAlreadyRequested = errors.New("movie already requested")

// Manager runs the lifecycle of all movies.
type Manager struct {
	store *store.Store
	svc   *Services
	sched *scheduler.Scheduler
	bus   *events.Bus
	log   *slog.Logger
	now   func() time.Time

	// ctx is the lifetime of the manager; downloads run under it.
	ctx context.Context

	// limiter caps the speed of all downloads together.
	limiter *download.RateLimiter
	fetcher *fetch.Fetcher

	mu       sync.Mutex
	active   map[int64]*activeDownload
	forced   map[int64]int64 // movie → release chosen by the user
	lastPoll time.Time
}

type activeDownload struct {
	cancel   context.CancelCauseFunc
	release  store.Release
	progress download.Progress
}

// NewManager creates the manager and registers its scheduler handlers.
// Call Start before the scheduler runs.
func NewManager(st *store.Store, svc *Services, sched *scheduler.Scheduler, bus *events.Bus, log *slog.Logger) *Manager {
	m := &Manager{
		store:   st,
		svc:     svc,
		sched:   sched,
		bus:     bus,
		log:     log.With("component", "movies"),
		now:     time.Now,
		ctx:     context.Background(),
		active:  map[int64]*activeDownload{},
		forced:  map[int64]int64{},
		limiter: download.NewRateLimiter(0),
	}
	m.fetcher = &fetch.Fetcher{Log: m.log}
	sched.Handle(taskSearch, m.handleSearch)
	sched.Handle(taskDelete, m.handleDelete)
	sched.Every("plex-watch-poll", time.Minute, m.pollWatched)
	svc.HandleScanRetries(sched)
	return m
}

// Limiter is the download speed limiter shared with other managers.
func (m *Manager) Limiter() *download.RateLimiter { return m.limiter }

// Start recovers from a restart: interrupted downloads are searched again.
// ctx bounds all downloads.
func (m *Manager) Start(ctx context.Context) error {
	m.ctx = ctx
	if err := m.store.CloseRunningAttempts(ctx); err != nil {
		return err
	}
	list, err := m.store.Movies(ctx, store.MovieDownloading, store.MovieQueued, store.MovieSearching)
	if err != nil {
		return err
	}
	for _, mv := range list {
		mv.State = store.MovieSearching
		if err := m.store.UpdateMovie(ctx, mv); err != nil {
			return err
		}
		if err := m.scheduleSearch(ctx, mv.ID, 0, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

// Request adds a movie by its TMDB ID and starts searching. profileID 0
// uses the default profile.
func (m *Manager) Request(ctx context.Context, tmdbID int, userID, profileID int64) (store.Movie, error) {
	existing, err := m.store.MovieByTMDB(ctx, tmdbID)
	switch {
	case err == nil && existing.State != store.MovieDeleted:
		return existing, ErrAlreadyRequested
	case err == nil:
		existing.State = store.MovieSearching
		existing.Attempts, existing.LastError = 0, ""
		existing.WatchedAt, existing.DeleteAt, existing.AvailableAt, existing.NextSearchAt = nil, nil, nil, nil
		existing.MediaKey = ""
		if err := m.store.UpdateMovie(ctx, existing); err != nil {
			return store.Movie{}, err
		}
		m.publish(existing)
		return existing, m.scheduleSearch(ctx, existing.ID, 0, time.Time{})
	case !errors.Is(err, store.ErrNotFound):
		return store.Movie{}, err
	}

	client, err := m.svc.TMDB(ctx)
	if err != nil {
		return store.Movie{}, err
	}
	meta, err := client.Movie(ctx, tmdbID)
	if err != nil {
		return store.Movie{}, err
	}
	if profileID == 0 {
		p, err := m.store.DefaultProfile(ctx)
		if err != nil {
			return store.Movie{}, err
		}
		profileID = p.ID
	}
	mv, err := m.store.CreateMovie(ctx, store.Movie{
		TMDBID:        meta.ID,
		IMDbID:        meta.IMDbID,
		Title:         meta.Title,
		OriginalTitle: meta.OriginalTitle,
		Year:          meta.Year,
		Overview:      meta.Overview,
		PosterPath:    meta.PosterPath,
		Runtime:       meta.Runtime,
		AltTitles:     meta.AltTitles,
		ProfileID:     profileID,
		State:         store.MovieSearching,
	}, userID)
	if err != nil {
		return store.Movie{}, err
	}
	m.log.Info("movie requested", "movie", mv.Title, "year", mv.Year)
	m.publish(mv)
	return mv, m.scheduleSearch(ctx, mv.ID, 0, time.Time{})
}

// Search starts a new search for a movie that is waiting or failed.
func (m *Manager) Search(ctx context.Context, id int64) (store.Movie, error) {
	mv, err := m.store.Movie(ctx, id)
	if err != nil {
		return store.Movie{}, err
	}
	switch mv.State {
	case store.MovieWaiting, store.MovieFailed, store.MovieSearching:
	default:
		return mv, fmt.Errorf("cannot search a movie that is %s", mv.State)
	}
	mv.State, mv.Attempts, mv.LastError, mv.NextSearchAt = store.MovieSearching, 0, "", nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return store.Movie{}, err
	}
	m.publish(mv)
	return mv, m.scheduleSearch(ctx, mv.ID, 0, time.Time{})
}

// SetKeep pins a movie (no automatic deletion) or releases it again.
func (m *Manager) SetKeep(ctx context.Context, id int64, keep bool) (store.Movie, error) {
	mv, err := m.store.Movie(ctx, id)
	if err != nil {
		return store.Movie{}, err
	}
	mv.Keep = keep
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return store.Movie{}, err
	}
	if keep {
		err = m.sched.Cancel(ctx, deleteKey(id))
	} else {
		err = m.scheduleDeletion(ctx, mv)
	}
	m.publish(mv)
	return mv, err
}

// DownloadRelease downloads a specific release chosen by the user, even if
// the profile rejected it. A running download of the movie is cancelled.
func (m *Manager) DownloadRelease(ctx context.Context, id, releaseID int64) (store.Movie, error) {
	rel, err := m.store.Release(ctx, releaseID)
	if err != nil {
		return store.Movie{}, err
	}
	if rel.MovieID != id {
		return store.Movie{}, store.ErrNotFound
	}
	m.cancelDownload(id, errSuperseded)
	m.mu.Lock()
	m.forced[id] = releaseID
	m.mu.Unlock()
	mv, err := m.store.Movie(ctx, id)
	if err != nil {
		return store.Movie{}, err
	}
	return mv, m.enqueue(ctx, mv)
}

// SkipRelease abandons the running download, blocklists its release and
// continues with the next one.
func (m *Manager) SkipRelease(_ context.Context, id int64) error {
	if !m.cancelDownload(id, errSkipped) {
		return errors.New("no download running for this movie")
	}
	return nil
}

// Delete removes the movie's files now. With forget the movie itself is
// removed as well.
func (m *Manager) Delete(ctx context.Context, id int64, forget bool) error {
	m.cancelDownload(id, errDeleted)
	mv, err := m.store.Movie(ctx, id)
	if err != nil {
		return err
	}
	if err := m.sched.Cancel(ctx, searchKey(id)); err != nil {
		return err
	}
	if err := m.sched.Cancel(ctx, deleteKey(id)); err != nil {
		return err
	}
	if err := m.removeFiles(ctx, &mv); err != nil {
		return err
	}
	if forget {
		if err := m.store.DeleteMovie(ctx, id); err != nil {
			return err
		}
		m.bus.Publish(EventMovieRemoved, map[string]int64{"id": id})
		return nil
	}
	mv.State = store.MovieDeleted
	mv.DeleteAt = nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.publish(mv)
	return nil
}

// Download describes a running download for the UI.
type Download struct {
	MovieID      int64             `json:"movieId"`
	ReleaseTitle string            `json:"releaseTitle"`
	Progress     download.Progress `json:"progress"`
}

// Downloads lists the running downloads.
func (m *Manager) Downloads() []Download {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Download{}
	for id, a := range m.active {
		out = append(out, Download{MovieID: id, ReleaseTitle: a.release.Title, Progress: a.progress})
	}
	return out
}

func (m *Manager) publish(mv store.Movie) {
	m.bus.Publish(EventMovieUpdated, mv)
}

type searchPayload struct {
	MovieID int64 `json:"movieId"`
	Round   int   `json:"round"` // consecutive searches without result
}

type deletePayload struct {
	MovieID int64  `json:"movieId"`
	Reason  string `json:"reason"` // watched, expired
}

func searchKey(id int64) string { return "movie:" + strconv.FormatInt(id, 10) + ":search" }
func deleteKey(id int64) string { return "movie:" + strconv.FormatInt(id, 10) + ":delete" }

func (m *Manager) scheduleSearch(ctx context.Context, id int64, round int, at time.Time) error {
	_, err := m.sched.Schedule(ctx, scheduler.Spec{
		Type: taskSearch, Key: searchKey(id), RunAt: at,
		Payload: searchPayload{MovieID: id, Round: round},
	})
	return err
}

// scheduleDeletion plans the automatic deletion of an available or
// watched movie according to the settings.
func (m *Manager) scheduleDeletion(ctx context.Context, mv store.Movie) error {
	if mv.Keep {
		return m.sched.Cancel(ctx, deleteKey(mv.ID))
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	var at time.Time
	reason := "watched"
	switch {
	case mv.State == store.MovieWatched && mv.DeleteAt != nil:
		at = *mv.DeleteAt
	case mv.State == store.MovieAvailable && g.UnwatchedExpiryDays > 0 && mv.AvailableAt != nil:
		at = mv.AvailableAt.Add(time.Duration(g.UnwatchedExpiryDays) * 24 * time.Hour)
		reason = "expired"
	default:
		return m.sched.Cancel(ctx, deleteKey(mv.ID))
	}
	_, err = m.sched.Schedule(ctx, scheduler.Spec{
		Type: taskDelete, Key: deleteKey(mv.ID), RunAt: at,
		Payload: deletePayload{MovieID: mv.ID, Reason: reason},
	})
	return err
}

func decode[T any](t scheduler.Task) (T, error) {
	var v T
	if err := json.Unmarshal(t.Payload, &v); err != nil {
		return v, scheduler.Permanent(fmt.Errorf("invalid payload: %w", err))
	}
	return v, nil
}
