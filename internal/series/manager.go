// Package series implements the rolling window for TV series: only the next
// few episodes are kept ready; when the next episode has been watched far
// enough, the previous one is deleted and another one is downloaded
// (docs/PLAN.md section 5.3).
package series

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/metadata/tmdb"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/download"
)

// Task types.
const (
	taskSearch  = "episode.search"
	taskRefresh = "series.refresh"
	taskCheck   = "series.check"
)

// Events published on the bus.
const (
	EventSeriesUpdated  = "series.updated"
	EventEpisodeUpdated = "episode.updated"
	EventSeriesRemoved  = "series.removed"
)

// Codes stored in Episode.LastError besides the fetch kinds.
const (
	CodeNoRelease        = movies.CodeNoRelease
	CodeNoIndexer        = movies.CodeNoIndexer
	CodeIndexerError     = movies.CodeIndexerError
	CodeNoUsenet         = movies.CodeNoUsenet
	CodeNoSeriesPath     = "no_series_path"
	CodeSourcesExhausted = movies.CodeSourcesExhausted
)

// ErrAlreadyRequested is returned when a series is requested twice.
var ErrAlreadyRequested = errors.New("series already requested")

// Manager runs all series.
type Manager struct {
	store   *store.Store
	svc     *movies.Services
	sched   *scheduler.Scheduler
	bus     *events.Bus
	log     *slog.Logger
	limiter *download.RateLimiter
	fetcher *fetch.Fetcher
	now     func() time.Time
	ctx     context.Context

	mu     sync.Mutex
	active map[int64]*activeDownload // episode → running download
	forced map[int64]int64           // episode → release chosen by the user
	// deleting holds the series that are being removed. No new downloads
	// start for them and Delete waits for the running ones.
	deleting map[int64]bool
	// reconciling serialises window updates per series.
	reconciling sync.Mutex
}

type activeDownload struct {
	episode  store.Episode
	cancel   context.CancelCauseFunc
	release  store.Release
	progress download.Progress
	// playable is set once the video can be read while the download runs.
	playable bool
	// done is closed when the download goroutine has finished and removed
	// itself from active.
	done chan struct{}
}

// NewManager creates the manager and registers its scheduler handlers.
// limiter is shared with the movie downloads.
func NewManager(st *store.Store, svc *movies.Services, sched *scheduler.Scheduler, bus *events.Bus, log *slog.Logger,
	limiter *download.RateLimiter) *Manager {
	m := &Manager{
		store: st, svc: svc, sched: sched, bus: bus, log: log.With("component", "series"), limiter: limiter,
		now: time.Now, ctx: context.Background(),
		active: map[int64]*activeDownload{}, forced: map[int64]int64{}, deleting: map[int64]bool{},
	}
	m.fetcher = &fetch.Fetcher{Log: m.log}
	sched.Handle(taskSearch, m.handleSearch)
	sched.Handle(taskRefresh, m.handleRefresh)
	sched.Handle(taskCheck, m.handleCheck)
	sched.Every("plex-episode-poll", time.Minute, m.pollWatched)
	sched.Handle(taskSpace, func(ctx context.Context, _ scheduler.Task) error {
		m.startQueued() // episodes that waited for space
		return nil
	})
	svc.HandleScanRetries(sched)
	return m
}

// Start recovers interrupted downloads after a restart.
func (m *Manager) Start(ctx context.Context) error {
	m.ctx = ctx
	eps, err := m.store.EpisodesInState(ctx, store.MovieDownloading, store.MovieQueued, store.MovieSearching)
	if err != nil {
		return err
	}
	for _, e := range eps {
		e.State = store.MovieSearching
		if err := m.store.UpdateEpisode(ctx, e); err != nil {
			return err
		}
		if err := m.scheduleSearch(ctx, e.ID, 0, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

// RequestOptions configure a new series.
type RequestOptions struct {
	ProfileID     int64  `json:"profileId"`
	Seasons       []int  `json:"seasons"`       // empty: all
	FutureSeasons bool   `json:"futureSeasons"` // also seasons that appear later
	StartSeason   int    `json:"startSeason"`
	StartEpisode  int    `json:"startEpisode"`
	WindowSize    int    `json:"windowSize"`
	Trigger       string `json:"deleteTrigger"`
	TriggerValue  int    `json:"triggerValue"`
}

// Request adds a series by its TMDB ID.
func (m *Manager) Request(ctx context.Context, tmdbID int, userID int64, opts RequestOptions) (store.Series, error) {
	if existing, err := m.store.SeriesByTMDB(ctx, tmdbID); err == nil {
		return existing, ErrAlreadyRequested
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Series{}, err
	}
	client, err := m.svc.TMDB(ctx)
	if err != nil {
		return store.Series{}, err
	}
	show, err := client.Show(ctx, tmdbID)
	if err != nil {
		return store.Series{}, err
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return store.Series{}, err
	}
	if opts.ProfileID == 0 {
		p, err := m.store.DefaultProfile(ctx)
		if err != nil {
			return store.Series{}, err
		}
		opts.ProfileID = p.ID
	}
	if opts.WindowSize <= 0 {
		opts.WindowSize = max(g.SeriesWindow, 1)
	}
	if opts.Trigger == "" {
		opts.Trigger, opts.TriggerValue = g.SeriesTrigger, g.SeriesTriggerValue
	}
	if err := validateTrigger(opts.Trigger, opts.TriggerValue); err != nil {
		return store.Series{}, err
	}
	if opts.StartSeason <= 0 {
		opts.StartSeason = 1
		if len(opts.Seasons) > 0 {
			opts.StartSeason = slices.Min(opts.Seasons)
		}
	}
	opts.StartEpisode = max(opts.StartEpisode, 1)

	se, err := m.store.CreateSeries(ctx, store.Series{
		TMDBID: show.ID, TVDBID: show.TVDBID, IMDbID: show.IMDbID, Title: show.Title, OriginalTitle: show.OriginalTitle,
		Year: show.Year, Overview: show.Overview, PosterPath: show.PosterPath, Status: show.Status, Runtime: show.Runtime,
		AltTitles: show.AltTitles, ProfileID: opts.ProfileID, Seasons: opts.Seasons, FutureSeasons: opts.FutureSeasons,
		KnownSeasons: lastSeason(show, m.now()), StartSeason: opts.StartSeason,
		StartEpisode: opts.StartEpisode, WindowSize: opts.WindowSize, DeleteTrigger: opts.Trigger, TriggerValue: opts.TriggerValue,
	}, userID)
	if err != nil {
		return store.Series{}, err
	}
	if err := m.loadEpisodes(ctx, client, &se, show); err != nil {
		return se, err
	}
	m.log.Info("series requested", "series", se.Title)
	m.bus.Publish(EventSeriesUpdated, se)
	if err := m.Reconcile(ctx, se.ID); err != nil {
		return se, err
	}
	return se, m.scheduleRefresh(ctx, se.ID)
}

// lastSeason is the highest season of a show that has started airing by
// now. Seasons after it, announced or not, count as future seasons.
func lastSeason(show tmdb.Show, now time.Time) int {
	n := 0
	for _, s := range show.Seasons {
		if d, err := time.ParseInLocation(time.DateOnly, s.AirDate, now.Location()); err == nil && !d.After(now) {
			n = max(n, s.Number)
		}
	}
	return n
}

func validateTrigger(trigger string, value int) error {
	switch trigger {
	case store.TriggerProgress:
		if value < 1 || value > 100 {
			return fmt.Errorf("%w: progress trigger must be 1-100 %%", store.ErrInvalid)
		}
	case store.TriggerMinutes, store.TriggerWatched:
		if value < 0 {
			return fmt.Errorf("%w: minutes must not be negative", store.ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown delete trigger %q", store.ErrInvalid, trigger)
	}
	return nil
}

// loadEpisodes fetches the episode lists of the wanted seasons and stores
// them; episodes before the starting point are marked as skipped.
func (m *Manager) loadEpisodes(ctx context.Context, client *tmdb.Client, se *store.Series, show tmdb.Show) error {
	var eps []store.Episode
	for _, season := range show.Seasons {
		if !se.Wants(season.Number) {
			continue
		}
		list, err := client.Season(ctx, show.ID, season.Number)
		if err != nil {
			return err
		}
		for _, e := range list {
			ep := store.Episode{Season: e.Season, Episode: e.Number, Title: e.Title, AirDate: e.AirDate, Runtime: e.Runtime}
			if ep.Season < se.StartSeason || (ep.Season == se.StartSeason && ep.Episode < se.StartEpisode) {
				ep.State = store.EpisodeSkipped
			}
			eps = append(eps, ep)
		}
	}
	if err := m.store.UpsertEpisodes(ctx, se.ID, eps); err != nil {
		return err
	}
	now := m.now()
	se.RefreshedAt = &now
	se.Status = show.Status
	return m.store.UpdateSeries(ctx, *se)
}

// Settings are the user-changeable options of a series.
type Settings struct {
	Seasons       []int  `json:"seasons"`
	FutureSeasons bool   `json:"futureSeasons"`
	WindowSize    int    `json:"windowSize"`
	Trigger       string `json:"deleteTrigger"`
	TriggerValue  int    `json:"triggerValue"`
	Keep          bool   `json:"keep"`
	Paused        bool   `json:"paused"`
}

// Update changes the settings of a series and adapts the window.
func (m *Manager) Update(ctx context.Context, id int64, st Settings) (store.Series, error) {
	se, err := m.store.Series(ctx, id)
	if err != nil {
		return store.Series{}, err
	}
	if st.WindowSize < 1 || st.WindowSize > 50 {
		return se, fmt.Errorf("%w: window must be 1-50 episodes", store.ErrInvalid)
	}
	if err := validateTrigger(st.Trigger, st.TriggerValue); err != nil {
		return se, err
	}
	seasonsChanged := !slices.Equal(se.Seasons, st.Seasons) || se.FutureSeasons != st.FutureSeasons
	if seasonsChanged && st.FutureSeasons {
		// Seasons that exist now are covered by the selection; only later
		// ones count as future.
		client, err := m.svc.TMDB(ctx)
		if err != nil {
			return se, err
		}
		show, err := client.Show(ctx, se.TMDBID)
		if err != nil {
			return se, err
		}
		se.KnownSeasons = lastSeason(show, m.now())
	}
	se.FutureSeasons = st.FutureSeasons
	se.Seasons, se.WindowSize, se.DeleteTrigger, se.TriggerValue, se.Keep, se.Paused = st.Seasons, st.WindowSize, st.Trigger, st.TriggerValue, st.Keep, st.Paused
	if err := m.store.UpdateSeries(ctx, se); err != nil {
		return se, err
	}
	if seasonsChanged {
		if _, err := m.sched.Schedule(ctx, scheduler.Spec{Type: taskRefresh, Key: refreshKey(id), Payload: idPayload{ID: id}}); err != nil {
			return se, err
		}
	}
	m.bus.Publish(EventSeriesUpdated, se)
	return se, m.Reconcile(ctx, id)
}

// Delete removes a series with all its files.
func (m *Manager) Delete(ctx context.Context, id int64) error {
	eps, err := m.store.Episodes(ctx, id)
	if err != nil {
		return err
	}
	se, err := m.store.Series(ctx, id)
	if err != nil {
		return err
	}

	// Mark the series as deleting before looking at the running downloads:
	// a download that has just been queued either registers before this
	// lock (and is cancelled below) or sees the flag and does not start.
	m.mu.Lock()
	if m.deleting == nil {
		m.deleting = map[int64]bool{}
	}
	m.deleting[id] = true
	var running []chan struct{}
	for _, a := range m.active {
		if a.episode.SeriesID == id {
			a.cancel(errDeleted)
			if a.done != nil {
				running = append(running, a.done)
			}
		}
	}
	m.mu.Unlock()
	removed := false
	defer func() {
		m.mu.Lock()
		delete(m.deleting, id)
		m.mu.Unlock()
		if !removed {
			m.startQueued() // the deletion failed, let the series continue
		}
	}()

	// Wait until the downloads have stopped. Otherwise they keep writing
	// into the folder while it is removed and can leave a file behind.
	for _, done := range running {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	for _, e := range eps {
		_ = m.sched.Cancel(ctx, searchKey(e.ID))
		if err := m.removeFiles(ctx, se, &e); err != nil {
			return err
		}
	}
	_ = m.sched.Cancel(ctx, refreshKey(id))
	_ = m.sched.Cancel(ctx, checkKey(id))
	if err := m.store.DeleteSeries(ctx, id); err != nil {
		return err
	}
	m.cleanupFolder(ctx, se)
	removed = true
	m.bus.Publish(EventSeriesRemoved, map[string]int64{"id": id})
	return nil
}

// SearchEpisode searches an episode again, e.g. after it failed.
func (m *Manager) SearchEpisode(ctx context.Context, episodeID int64) (store.Episode, error) {
	e, err := m.store.Episode(ctx, episodeID)
	if err != nil {
		return e, err
	}
	switch e.State {
	case store.MovieWaiting, store.MovieFailed, store.MovieSearching, store.EpisodePending, store.MovieDeleted, store.EpisodeSkipped:
	default:
		return e, fmt.Errorf("cannot search an episode that is %s", e.State)
	}
	e.State, e.Attempts, e.LastError, e.NextSearchAt = store.MovieSearching, 0, "", nil
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return e, err
	}
	m.publishEpisode(e)
	return e, m.scheduleSearch(ctx, e.ID, 0, time.Time{})
}

// SkipRelease abandons the running download of an episode and tries the
// next release.
func (m *Manager) SkipRelease(_ context.Context, episodeID int64) error {
	if !m.cancelDownload(episodeID, errSkipped) {
		return errors.New("no download running for this episode")
	}
	return nil
}

// DownloadRelease downloads a specific release of an episode.
func (m *Manager) DownloadRelease(ctx context.Context, episodeID, releaseID int64) (store.Episode, error) {
	rel, err := m.store.Release(ctx, releaseID)
	if err != nil {
		return store.Episode{}, err
	}
	if rel.EpisodeID != episodeID {
		return store.Episode{}, store.ErrNotFound
	}
	m.cancelDownload(episodeID, errSuperseded)
	m.mu.Lock()
	m.forced[episodeID] = releaseID
	m.mu.Unlock()
	e, err := m.store.Episode(ctx, episodeID)
	if err != nil {
		return e, err
	}
	return e, m.enqueue(ctx, e)
}

// Download is a running episode download for the UI.
type Download struct {
	EpisodeID    int64             `json:"episodeId"`
	SeriesID     int64             `json:"seriesId"`
	Season       int               `json:"season"`
	Episode      int               `json:"episode"`
	ReleaseTitle string            `json:"releaseTitle"`
	Progress     download.Progress `json:"progress"`
	// Playable is set once the episode can be watched while the download
	// continues.
	Playable bool `json:"playable"`
}

// Downloads lists the running downloads.
func (m *Manager) Downloads() []Download {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Download{}
	for id, a := range m.active {
		out = append(out, Download{EpisodeID: id, SeriesID: a.episode.SeriesID, Season: a.episode.Season, Episode: a.episode.Episode,
			ReleaseTitle: a.release.Title, Progress: a.progress, Playable: a.playable})
	}
	return out
}

func (m *Manager) publishEpisode(e store.Episode) { m.bus.Publish(EventEpisodeUpdated, e) }

type searchPayload struct {
	EpisodeID int64 `json:"episodeId"`
	Round     int   `json:"round"`
}

type idPayload struct {
	ID int64 `json:"id"`
}

func searchKey(id int64) string  { return "episode:" + strconv.FormatInt(id, 10) + ":search" }
func refreshKey(id int64) string { return "series:" + strconv.FormatInt(id, 10) + ":refresh" }
func checkKey(id int64) string   { return "series:" + strconv.FormatInt(id, 10) + ":check" }

func (m *Manager) scheduleSearch(ctx context.Context, id int64, round int, at time.Time) error {
	_, err := m.sched.Schedule(ctx, scheduler.Spec{Type: taskSearch, Key: searchKey(id), RunAt: at,
		Payload: searchPayload{EpisodeID: id, Round: round}})
	return err
}

// scheduleRefresh plans the daily update of the episode list (new episodes,
// changed air dates).
func (m *Manager) scheduleRefresh(ctx context.Context, id int64) error {
	_, err := m.sched.Schedule(ctx, scheduler.Spec{Type: taskRefresh, Key: refreshKey(id), RunAt: m.now().Add(24 * time.Hour),
		Payload: idPayload{ID: id}})
	return err
}

func (m *Manager) handleRefresh(ctx context.Context, t scheduler.Task) error {
	p, err := decode[idPayload](t)
	if err != nil {
		return err
	}
	se, err := m.store.Series(ctx, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	client, err := m.svc.TMDB(ctx)
	if err != nil {
		return err
	}
	show, err := client.Show(ctx, se.TMDBID)
	if err != nil {
		return err
	}
	if err := m.loadEpisodes(ctx, client, &se, show); err != nil {
		return err
	}
	if err := m.Reconcile(ctx, se.ID); err != nil {
		return err
	}
	return m.scheduleRefresh(ctx, se.ID)
}

// handleCheck re-evaluates a series at a planned time (e.g. a minutes
// trigger or a deletion delay running out).
func (m *Manager) handleCheck(ctx context.Context, t scheduler.Task) error {
	p, err := decode[idPayload](t)
	if err != nil {
		return err
	}
	if err := m.Reconcile(ctx, p.ID); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	return nil
}

func decode[T any](t scheduler.Task) (T, error) {
	var v T
	if err := json.Unmarshal(t.Payload, &v); err != nil {
		return v, scheduler.Permanent(fmt.Errorf("invalid payload: %w", err))
	}
	return v, nil
}
