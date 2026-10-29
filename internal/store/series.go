package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Episode states beyond the movie states (searching, waiting, queued,
// downloading, available, watched, deleted, failed).
const (
	EpisodePending = "pending" // not (yet) in the window
	EpisodeSkipped = "skipped" // before the starting point or jumped over
)

// Series delete triggers.
const (
	TriggerProgress = "progress" // TriggerValue percent of the next episode watched
	TriggerMinutes  = "minutes"  // TriggerValue minutes after the next episode started
	TriggerWatched  = "watched"  // the episode itself was marked as watched
)

// Series is a requested TV series.
type Series struct {
	ID            int64      `json:"id"`
	TMDBID        int        `json:"tmdbId"`
	TVDBID        int        `json:"tvdbId"`
	IMDbID        string     `json:"imdbId"`
	Title         string     `json:"title"`
	OriginalTitle string     `json:"originalTitle"`
	Year          int        `json:"year"`
	Overview      string     `json:"overview"`
	PosterPath    string     `json:"posterPath"`
	Status        string     `json:"status"`
	Runtime       int        `json:"runtime"`
	AltTitles     []string   `json:"altTitles"`
	ProfileID     int64      `json:"profileId"`
	Seasons       []int      `json:"seasons"`       // empty: all seasons
	FutureSeasons bool       `json:"futureSeasons"` // also seasons after KnownSeasons
	KnownSeasons  int        `json:"knownSeasons"`  // highest season when the selection was made
	StartSeason   int        `json:"startSeason"`
	StartEpisode  int        `json:"startEpisode"`
	WindowSize    int        `json:"windowSize"`
	DeleteTrigger string     `json:"deleteTrigger"`
	TriggerValue  int        `json:"triggerValue"`
	Keep          bool       `json:"keep"`
	Paused        bool       `json:"paused"`
	Folder        string     `json:"folder"`
	RefreshedAt   *time.Time `json:"refreshedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// Wants reports whether a season is part of the selection.
func (s Series) Wants(season int) bool {
	if season == 0 {
		return containsInt(s.Seasons, 0) // specials only when chosen
	}
	return len(s.Seasons) == 0 || containsInt(s.Seasons, season) || (s.FutureSeasons && season > s.KnownSeasons)
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Episode is one episode of a series.
type Episode struct {
	ID           int64      `json:"id"`
	SeriesID     int64      `json:"seriesId"`
	Season       int        `json:"season"`
	Episode      int        `json:"episode"`
	Title        string     `json:"title"`
	AirDate      string     `json:"airDate"`
	Runtime      int        `json:"runtime"`
	State        string     `json:"state"`
	Attempts     int        `json:"attempts"`
	LastError    string     `json:"lastError"`
	NextSearchAt *time.Time `json:"nextSearchAt,omitempty"`
	ReleaseTitle string     `json:"releaseTitle"`
	File         string     `json:"file"`
	MediaKey     string     `json:"mediaKey"`
	Progress     float64    `json:"progress"` // 0..1
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	AvailableAt  *time.Time `json:"availableAt,omitempty"`
	WatchedAt    *time.Time `json:"watchedAt,omitempty"`
	DeleteAt     *time.Time `json:"deleteAt,omitempty"`
	Base         WatchBase  `json:"-"`
}

// Aired reports whether the episode has been broadcast by now (unknown
// dates count as not aired).
func (e Episode) Aired(now time.Time) bool {
	if e.AirDate == "" {
		return false
	}
	d, err := time.ParseInLocation(time.DateOnly, e.AirDate, now.Location())
	return err == nil && !d.After(now)
}

// Before orders episodes.
func (e Episode) Before(o Episode) bool {
	return e.Season < o.Season || (e.Season == o.Season && e.Episode < o.Episode)
}

const seriesColumns = `id, tmdb_id, tvdb_id, imdb_id, title, original_title, year, overview, poster_path, status, runtime, alt_titles,
	COALESCE(profile_id, 0), seasons, future_seasons, known_seasons, start_season, start_episode, window_size, delete_trigger, trigger_value, keep, paused,
	folder, refreshed_at, created_at`

func scanSeries(row interface{ Scan(...any) error }) (Series, error) {
	var s Series
	var alt, seasons string
	var refreshed sql.NullInt64
	var created int64
	err := row.Scan(&s.ID, &s.TMDBID, &s.TVDBID, &s.IMDbID, &s.Title, &s.OriginalTitle, &s.Year, &s.Overview, &s.PosterPath,
		&s.Status, &s.Runtime, &alt, &s.ProfileID, &seasons, &s.FutureSeasons, &s.KnownSeasons, &s.StartSeason, &s.StartEpisode, &s.WindowSize, &s.DeleteTrigger,
		&s.TriggerValue, &s.Keep, &s.Paused, &s.Folder, &refreshed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Series{}, ErrNotFound
	}
	if err != nil {
		return Series{}, err
	}
	_ = json.Unmarshal([]byte(alt), &s.AltTitles)
	_ = json.Unmarshal([]byte(seasons), &s.Seasons)
	if s.AltTitles == nil {
		s.AltTitles = []string{}
	}
	if s.Seasons == nil {
		s.Seasons = []int{}
	}
	s.RefreshedAt = nullTime(refreshed)
	s.CreatedAt = time.UnixMilli(created)
	return s, nil
}

// CreateSeries stores a new series request.
func (s *Store) CreateSeries(ctx context.Context, se Series, requestedBy int64) (Series, error) {
	alt, _ := json.Marshal(se.AltTitles)
	seasons, _ := json.Marshal(se.Seasons)
	now := s.ms()
	var user, profile any
	if requestedBy > 0 {
		user = requestedBy
	}
	if se.ProfileID > 0 {
		profile = se.ProfileID
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO series (tmdb_id, tvdb_id, imdb_id, title, original_title, year, overview, poster_path, status, runtime, alt_titles,
			profile_id, requested_by, seasons, future_seasons, known_seasons, start_season, start_episode, window_size, delete_trigger,
			trigger_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		se.TMDBID, se.TVDBID, se.IMDbID, se.Title, se.OriginalTitle, se.Year, se.Overview, se.PosterPath, se.Status, se.Runtime,
		string(alt), profile, user, string(seasons), se.FutureSeasons, se.KnownSeasons, se.StartSeason, se.StartEpisode, se.WindowSize, se.DeleteTrigger, se.TriggerValue,
		now, now)
	if err != nil {
		return Series{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Series{}, err
	}
	return s.Series(ctx, id)
}

// Series returns one series.
func (s *Store) Series(ctx context.Context, id int64) (Series, error) {
	return scanSeries(s.db.QueryRowContext(ctx, `SELECT `+seriesColumns+` FROM series WHERE id = ?`, id))
}

// SeriesByTMDB returns the series with a TMDB ID.
func (s *Store) SeriesByTMDB(ctx context.Context, tmdbID int) (Series, error) {
	return scanSeries(s.db.QueryRowContext(ctx, `SELECT `+seriesColumns+` FROM series WHERE tmdb_id = ?`, tmdbID))
}

// SeriesList lists all series, newest first.
func (s *Store) SeriesList(ctx context.Context) ([]Series, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+seriesColumns+` FROM series ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Series{}
	for rows.Next() {
		se, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, se)
	}
	return out, rows.Err()
}

// UpdateSeries writes the settings and metadata of a series.
func (s *Store) UpdateSeries(ctx context.Context, se Series) error {
	alt, _ := json.Marshal(se.AltTitles)
	seasons, _ := json.Marshal(se.Seasons)
	return affected(s.db.ExecContext(ctx, `
		UPDATE series SET tvdb_id = ?, imdb_id = ?, title = ?, original_title = ?, overview = ?, poster_path = ?, status = ?,
			runtime = ?, alt_titles = ?, seasons = ?, future_seasons = ?, known_seasons = ?, start_season = ?, start_episode = ?, window_size = ?, delete_trigger = ?,
			trigger_value = ?, keep = ?, paused = ?, folder = ?, refreshed_at = ?, updated_at = ?
		WHERE id = ?`,
		se.TVDBID, se.IMDbID, se.Title, se.OriginalTitle, se.Overview, se.PosterPath, se.Status, se.Runtime, string(alt),
		string(seasons), se.FutureSeasons, se.KnownSeasons, se.StartSeason, se.StartEpisode, se.WindowSize, se.DeleteTrigger, se.TriggerValue, se.Keep, se.Paused,
		se.Folder, timeArg(se.RefreshedAt), s.ms(), se.ID))
}

// DeleteSeries removes a series with its episodes and history.
func (s *Store) DeleteSeries(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM series WHERE id = ?`, id))
}

// UpsertEpisodes adds new episodes and refreshes title, air date and
// runtime of known ones. State and progress are kept.
func (s *Store) UpsertEpisodes(ctx context.Context, seriesID int64, eps []Episode) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.ms()
	for _, e := range eps {
		state := e.State
		if state == "" {
			state = EpisodePending
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO episodes (series_id, season, episode, title, air_date, runtime, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (series_id, season, episode) DO UPDATE SET
				title = excluded.title, air_date = excluded.air_date, runtime = excluded.runtime, updated_at = excluded.updated_at`,
			seriesID, e.Season, e.Episode, e.Title, e.AirDate, e.Runtime, state, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const episodeColumns = `id, series_id, season, episode, title, air_date, runtime, state, attempts, last_error, next_search_at,
	release_title, file, media_key, progress, started_at, available_at, watched_at, delete_at, base_set, base_views, base_offset`

func scanEpisode(row interface{ Scan(...any) error }) (Episode, error) {
	var e Episode
	var next, started, avail, watched, del sql.NullInt64
	err := row.Scan(&e.ID, &e.SeriesID, &e.Season, &e.Episode, &e.Title, &e.AirDate, &e.Runtime, &e.State, &e.Attempts, &e.LastError,
		&next, &e.ReleaseTitle, &e.File, &e.MediaKey, &e.Progress, &started, &avail, &watched, &del, &e.Base.Set, &e.Base.Views,
		&e.Base.Offset)
	if errors.Is(err, sql.ErrNoRows) {
		return Episode{}, ErrNotFound
	}
	e.NextSearchAt, e.StartedAt, e.AvailableAt, e.WatchedAt, e.DeleteAt = nullTime(next), nullTime(started), nullTime(avail), nullTime(watched), nullTime(del)
	return e, err
}

// Episodes returns the episodes of a series in broadcast order.
func (s *Store) Episodes(ctx context.Context, seriesID int64) ([]Episode, error) {
	return s.queryEpisodes(ctx, `SELECT `+episodeColumns+` FROM episodes WHERE series_id = ? ORDER BY season, episode`, seriesID)
}

// EpisodesInState returns the episodes of all series in the given states.
func (s *Store) EpisodesInState(ctx context.Context, states ...string) ([]Episode, error) {
	q := `SELECT ` + episodeColumns + ` FROM episodes WHERE state IN (?` + strings.Repeat(`, ?`, len(states)-1) + `) ORDER BY series_id, season, episode`
	args := make([]any, len(states))
	for i, st := range states {
		args[i] = st
	}
	return s.queryEpisodes(ctx, q, args...)
}

func (s *Store) queryEpisodes(ctx context.Context, q string, args ...any) ([]Episode, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Episode{}
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Episode returns one episode.
func (s *Store) Episode(ctx context.Context, id int64) (Episode, error) {
	return scanEpisode(s.db.QueryRowContext(ctx, `SELECT `+episodeColumns+` FROM episodes WHERE id = ?`, id))
}

// UpdateEpisode writes the mutable fields of an episode.
func (s *Store) UpdateEpisode(ctx context.Context, e Episode) error {
	return affected(s.db.ExecContext(ctx, `
		UPDATE episodes SET state = ?, attempts = ?, last_error = ?, next_search_at = ?, release_title = ?, file = ?, media_key = ?,
			progress = ?, started_at = ?, available_at = ?, watched_at = ?, delete_at = ?, base_set = ?, base_views = ?, base_offset = ?,
			updated_at = ?
		WHERE id = ?`,
		e.State, e.Attempts, e.LastError, timeArg(e.NextSearchAt), e.ReleaseTitle, e.File, e.MediaKey, e.Progress,
		timeArg(e.StartedAt), timeArg(e.AvailableAt), timeArg(e.WatchedAt), timeArg(e.DeleteAt), e.Base.Set, e.Base.Views,
		e.Base.Offset, s.ms(), e.ID))
}
