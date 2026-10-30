package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Movie states.
const (
	MovieSearching   = "searching"   // looking for a release
	MovieWaiting     = "waiting"     // nothing acceptable found; searching again later
	MovieQueued      = "queued"      // release chosen, waiting for a download slot
	MovieDownloading = "downloading" // downloading and extracting
	MovieAvailable   = "available"   // in the library
	MovieWatched     = "watched"     // watched; deleted at DeleteAt
	MovieDeleted     = "deleted"     // files removed
	MovieFailed      = "failed"      // needs the user (no source left, configuration error)
)

// Movie is a requested movie.
type Movie struct {
	Genres          []string   `json:"genres"`
	ID              int64      `json:"id"`
	TMDBID          int        `json:"tmdbId"`
	IMDbID          string     `json:"imdbId"`
	Title           string     `json:"title"`
	OriginalTitle   string     `json:"originalTitle"`
	Year            int        `json:"year"`
	Overview        string     `json:"overview"`
	PosterPath      string     `json:"posterPath"`
	Runtime         int        `json:"runtime"`
	AltTitles       []string   `json:"altTitles"`
	ProfileID       int64      `json:"profileId"`
	RequestedBy     int64      `json:"requestedBy"`
	RequestedByName string     `json:"requestedByName,omitempty"`
	State           string     `json:"state"`
	Keep            bool       `json:"keep"`
	Attempts        int        `json:"attempts"`
	LastError       string     `json:"lastError"`
	NextSearchAt    *time.Time `json:"nextSearchAt,omitempty"`
	ReleaseKey      string     `json:"releaseKey"`
	ReleaseTitle    string     `json:"releaseTitle"`
	Folder          string     `json:"folder"`
	MediaKey        string     `json:"mediaKey"`
	AvailableAt     *time.Time `json:"availableAt,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	WatchedAt       *time.Time `json:"watchedAt,omitempty"`
	DeleteAt        *time.Time `json:"deleteAt,omitempty"`
	Progress        float64    `json:"progress"` // 0..1, watched since the file is there
	Base            WatchBase  `json:"-"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// WatchBase is the watch state the media server reported when the current
// file first showed up there. Plex keeps the history of a title across
// deletions, so only changes against it count as watching.
type WatchBase struct {
	Set    bool
	Views  int
	Offset int64 // resume position in ms
}

const movieColumns = `id, tmdb_id, imdb_id, title, original_title, year, overview, poster_path, runtime, genres, alt_titles,
	COALESCE(profile_id, 0), COALESCE(requested_by, 0), COALESCE((SELECT username FROM users WHERE id = movies.requested_by), ''),
	state, keep, attempts, last_error, next_search_at, release_key, release_title, folder, media_key,
	available_at, watched_at, delete_at, started_at, progress, base_set, base_views, base_offset, created_at, updated_at`

func scanMovie(row interface{ Scan(...any) error }) (Movie, error) {
	var m Movie
	var alt, genres string
	var next, avail, watched, del, started sql.NullInt64
	var created, updated int64
	err := row.Scan(&m.ID, &m.TMDBID, &m.IMDbID, &m.Title, &m.OriginalTitle, &m.Year, &m.Overview, &m.PosterPath, &m.Runtime, &genres, &alt,
		&m.ProfileID, &m.RequestedBy, &m.RequestedByName, &m.State, &m.Keep, &m.Attempts, &m.LastError, &next, &m.ReleaseKey, &m.ReleaseTitle, &m.Folder, &m.MediaKey,
		&avail, &watched, &del, &started, &m.Progress, &m.Base.Set, &m.Base.Views, &m.Base.Offset, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Movie{}, ErrNotFound
	}
	if err != nil {
		return Movie{}, err
	}
	_ = json.Unmarshal([]byte(genres), &m.Genres)
	_ = json.Unmarshal([]byte(alt), &m.AltTitles)
	if m.AltTitles == nil {
		m.AltTitles = []string{}
	}
	m.NextSearchAt, m.AvailableAt, m.WatchedAt, m.DeleteAt = nullTime(next), nullTime(avail), nullTime(watched), nullTime(del)
	m.StartedAt = nullTime(started)
	m.CreatedAt, m.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return m, nil
}

// CreateMovie stores a new request and returns it with its ID.
func (s *Store) CreateMovie(ctx context.Context, m Movie, requestedBy int64) (Movie, error) {
	alt, _ := json.Marshal(m.AltTitles)
	genres, _ := json.Marshal(m.Genres)
	now := s.ms()
	var user any
	if requestedBy > 0 {
		user = requestedBy
	}
	var profile any
	if m.ProfileID > 0 {
		profile = m.ProfileID
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO movies (tmdb_id, imdb_id, title, original_title, year, overview, poster_path, runtime, genres, alt_titles,
			profile_id, requested_by, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.TMDBID, m.IMDbID, m.Title, m.OriginalTitle, m.Year, m.Overview, m.PosterPath, m.Runtime, string(genres), string(alt),
		profile, user, m.State, now, now)
	if err != nil {
		return Movie{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Movie{}, err
	}
	return s.Movie(ctx, id)
}

// Movie returns one movie.
func (s *Store) Movie(ctx context.Context, id int64) (Movie, error) {
	return scanMovie(s.db.QueryRowContext(ctx, `SELECT `+movieColumns+` FROM movies WHERE id = ?`, id))
}

// MovieByTMDB returns the movie with a TMDB ID.
func (s *Store) MovieByTMDB(ctx context.Context, tmdbID int) (Movie, error) {
	return scanMovie(s.db.QueryRowContext(ctx, `SELECT `+movieColumns+` FROM movies WHERE tmdb_id = ?`, tmdbID))
}

// Movies lists movies, newest first. With states, only those states.
func (s *Store) Movies(ctx context.Context, states ...string) ([]Movie, error) {
	q := `SELECT ` + movieColumns + ` FROM movies`
	args := make([]any, len(states))
	if len(states) > 0 {
		q += ` WHERE state IN (?` + strings.Repeat(`, ?`, len(states)-1) + `)`
		for i, st := range states {
			args[i] = st
		}
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Movie{}
	for rows.Next() {
		m, err := scanMovie(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMovie writes the mutable fields of m.
func (s *Store) UpdateMovie(ctx context.Context, m Movie) error {
	return affected(s.db.ExecContext(ctx, `
		UPDATE movies SET state = ?, keep = ?, attempts = ?, last_error = ?, next_search_at = ?, release_key = ?, release_title = ?,
			folder = ?, media_key = ?, available_at = ?, watched_at = ?, delete_at = ?, started_at = ?, progress = ?, base_set = ?, base_views = ?,
			base_offset = ?, updated_at = ?
		WHERE id = ?`,
		m.State, m.Keep, m.Attempts, m.LastError, timeArg(m.NextSearchAt), m.ReleaseKey, m.ReleaseTitle,
		m.Folder, m.MediaKey, timeArg(m.AvailableAt), timeArg(m.WatchedAt), timeArg(m.DeleteAt), timeArg(m.StartedAt), m.Progress, m.Base.Set,
		m.Base.Views, m.Base.Offset, s.ms(), m.ID))
}

// DeleteMovie removes a movie and its history.
func (s *Store) DeleteMovie(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM movies WHERE id = ?`, id))
}

// Owner is the movie or episode that releases, attempts, blocklist entries
// and files belong to.
type Owner struct {
	col string
	ID  int64
}

// ForMovie and ForEpisode select the owner of a record.
func ForMovie(id int64) Owner   { return Owner{"movie_id", id} }
func ForEpisode(id int64) Owner { return Owner{"episode_id", id} }

// Release is a search result for a movie or episode, with the profile's
// verdict.
type Release struct {
	ReleaseKey  string     `json:"releaseKey"`
	ID          int64      `json:"id"`
	MovieID     int64      `json:"movieId,omitempty"`
	EpisodeID   int64      `json:"episodeId,omitempty"`
	IndexerID   int64      `json:"indexerId"`
	IndexerName string     `json:"indexerName"`
	GUID        string     `json:"guid"`
	Title       string     `json:"title"`
	Size        int64      `json:"size"`
	NZBURL      string     `json:"-"` // contains the indexer's API key
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	Grabs       int        `json:"grabs"`
	Passworded  bool       `json:"passworded"`
	Fingerprint string     `json:"fingerprint"`
	Score       int        `json:"score"`
	Rejected    string     `json:"rejected"`
	Blocklisted bool       `json:"blocklisted"`
}

// ReplaceReleases stores the results of a new search, replacing older ones.
func (s *Store) ReplaceReleases(ctx context.Context, movieID int64, rels []Release) error {
	return s.ReplaceReleasesOf(ctx, ForMovie(movieID), rels)
}

// ReplaceReleasesOf stores the search results of a movie or episode.
func (s *Store) ReplaceReleasesOf(ctx context.Context, o Owner, rels []Release) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM releases WHERE `+o.col+` = ?`, o.ID); err != nil {
		return err
	}
	now := s.ms()
	for _, r := range rels {
		var indexer any
		if r.IndexerID > 0 {
			indexer = r.IndexerID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO releases (`+o.col+`, indexer_id, indexer_name, guid, title, size, nzb_url, published_at, grabs, passworded,
				fingerprint, score, rejected, searched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			o.ID, indexer, r.IndexerName, r.GUID, r.Title, r.Size, r.NZBURL, timeArg(r.PublishedAt), r.Grabs, r.Passworded,
			r.Fingerprint, r.Score, r.Rejected, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const releaseColumns = `r.id, COALESCE(r.movie_id, 0), COALESCE(r.episode_id, 0), COALESCE(r.indexer_id, 0), r.indexer_name, r.guid, r.title, r.size, r.nzb_url,
	r.published_at, r.grabs, r.passworded, r.fingerprint, r.score, r.rejected,
	EXISTS (SELECT 1 FROM blocklist b WHERE b.fingerprint = r.fingerprint)`

func scanRelease(row interface{ Scan(...any) error }) (Release, error) {
	var r Release
	var pub sql.NullInt64
	err := row.Scan(&r.ID, &r.MovieID, &r.EpisodeID, &r.IndexerID, &r.IndexerName, &r.GUID, &r.Title, &r.Size, &r.NZBURL, &pub,
		&r.Grabs, &r.Passworded, &r.Fingerprint, &r.Score, &r.Rejected, &r.Blocklisted)
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	r.ReleaseKey = r.Identity()
	r.PublishedAt = nullTime(pub)
	return r, err
}

// Releases returns the stored results of a movie, best first, marking
// blocklisted ones.
func (s *Store) Releases(ctx context.Context, movieID int64) ([]Release, error) {
	return s.ReleasesOf(ctx, ForMovie(movieID))
}

// ReleasesOf returns the stored results of a movie or episode, best first:
// by score, then the newest upload (less likely taken down), then grabs.
func (s *Store) ReleasesOf(ctx context.Context, o Owner) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+releaseColumns+` FROM releases r WHERE r.`+o.col+` = ?
		ORDER BY r.rejected != '', r.score DESC, r.published_at IS NULL, r.published_at DESC, r.grabs DESC, r.id`, o.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Release returns one stored result.
func (s *Store) Release(ctx context.Context, id int64) (Release, error) {
	return scanRelease(s.db.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM releases r WHERE r.id = ?`, id))
}

// BlocklistEntry is a release that must not be downloaded again.
type BlocklistEntry struct {
	ID          int64     `json:"id"`
	MovieID     int64     `json:"movieId,omitempty"`
	EpisodeID   int64     `json:"episodeId,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	Title       string    `json:"title"`
	Reason      string    `json:"reason"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Blocklist records a broken release.
func (s *Store) Blocklist(ctx context.Context, e BlocklistEntry) error {
	var movie, episode any
	if e.MovieID > 0 {
		movie = e.MovieID
	}
	if e.EpisodeID > 0 {
		episode = e.EpisodeID
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO blocklist (movie_id, episode_id, fingerprint, title, reason, message, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		movie, episode, e.Fingerprint, e.Title, e.Reason, e.Message, s.ms())
	return err
}

// Blocked returns all blocklisted fingerprints.
func (s *Store) Blocked(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fingerprint FROM blocklist`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, err
		}
		out[fp] = true
	}
	return out, rows.Err()
}

// BlocklistEntries lists the blocklist of a movie (0: all).
func (s *Store) BlocklistEntries(ctx context.Context, movieID int64) ([]BlocklistEntry, error) {
	if movieID == 0 {
		return s.BlocklistOf(ctx, Owner{})
	}
	return s.BlocklistOf(ctx, ForMovie(movieID))
}

// BlocklistOf lists the blocklist of a movie or episode (zero Owner: all).
func (s *Store) BlocklistOf(ctx context.Context, o Owner) ([]BlocklistEntry, error) {
	q := `SELECT id, COALESCE(movie_id, 0), COALESCE(episode_id, 0), fingerprint, title, reason, message, created_at FROM blocklist`
	var args []any
	if o.col != "" {
		q += ` WHERE ` + o.col + ` = ?`
		args = append(args, o.ID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlocklistEntry{}
	for rows.Next() {
		var e BlocklistEntry
		var created int64
		if err := rows.Scan(&e.ID, &e.MovieID, &e.EpisodeID, &e.Fingerprint, &e.Title, &e.Reason, &e.Message, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = time.UnixMilli(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RemoveBlocklistEntry deletes an entry so the release may be tried again.
func (s *Store) RemoveBlocklistEntry(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM blocklist WHERE id = ?`, id))
}

// Attempt is one download try.
type Attempt struct {
	ReleaseKey   string     `json:"releaseKey"`
	ID           int64      `json:"id"`
	MovieID      int64      `json:"movieId,omitempty"`
	ReleaseTitle string     `json:"releaseTitle"`
	IndexerName  string     `json:"indexerName"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	Result       string     `json:"result"` // running, success, failed, cancelled
	ErrorKind    string     `json:"errorKind"`
	Message      string     `json:"message"`
}

// StartAttempt records the start of a download.
func (s *Store) StartAttempt(ctx context.Context, movieID int64, r Release) (int64, error) {
	return s.StartAttemptOf(ctx, ForMovie(movieID), r)
}

// StartAttemptOf records the start of a download of a movie or episode.
func (s *Store) StartAttemptOf(ctx context.Context, o Owner, r Release) (int64, error) {
	// The label and the series are copied so that the history keeps them
	// after the movie or series is removed.
	var q string
	if o.col == "movie_id" {
		q = `INSERT INTO attempts (movie_id, kind, label, size, release_key, release_title, indexer_name, started_at)
			SELECT id, 'movie', title || CASE WHEN year > 0 THEN ' (' || year || ')' ELSE '' END, ?, ?, ?, ?, ?
			FROM movies WHERE id = ?`
	} else {
		q = `INSERT INTO attempts (episode_id, series_id, kind, label, size, release_key, release_title, indexer_name, started_at)
			SELECT e.id, e.series_id, 'episode', s.title || ' · S' || printf('%02d', e.season) || 'E' || printf('%02d', e.episode),
				?, ?, ?, ?, ?
			FROM episodes e JOIN series s ON s.id = e.series_id WHERE e.id = ?`
	}
	res, err := s.db.ExecContext(ctx, q, r.Size, r.Identity(), r.Title, r.IndexerName, s.ms(), o.ID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishAttempt records the outcome of a download.
func (s *Store) FinishAttempt(ctx context.Context, id int64, result, kind, message string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE attempts SET finished_at = ?, result = ?, error_kind = ?, message = ? WHERE id = ?`,
		s.ms(), result, kind, message, id))
}

// Attempts lists the attempts of a movie, newest first.
func (s *Store) Attempts(ctx context.Context, movieID int64) ([]Attempt, error) {
	return s.AttemptsOf(ctx, ForMovie(movieID))
}

// AttemptsOf lists the attempts of a movie or episode, newest first.
func (s *Store) AttemptsOf(ctx context.Context, o Owner) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(movie_id, 0), release_key, release_title, indexer_name, started_at, finished_at, result, error_kind, message
		FROM attempts WHERE `+o.col+` = ? ORDER BY id DESC`, o.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var a Attempt
		var started int64
		var finished sql.NullInt64
		if err := rows.Scan(&a.ID, &a.MovieID, &a.ReleaseKey, &a.ReleaseTitle, &a.IndexerName, &started, &finished, &a.Result, &a.ErrorKind, &a.Message); err != nil {
			return nil, err
		}
		a.StartedAt, a.FinishedAt = time.UnixMilli(started), nullTime(finished)
		out = append(out, a)
	}
	return out, rows.Err()
}

// CloseRunningAttempts marks attempts interrupted by a restart.
func (s *Store) CloseRunningAttempts(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE attempts SET result = 'cancelled', message = 'interrupted by restart', finished_at = ? WHERE result = 'running'`, s.ms())
	return err
}

// TrackFile records a file created in a library.
func (s *Store) TrackFile(ctx context.Context, movieID int64, path string, size int64) error {
	return s.TrackFileOf(ctx, ForMovie(movieID), path, size)
}

// TrackFileOf records a file created for a movie or episode.
func (s *Store) TrackFileOf(ctx context.Context, o Owner, path string, size int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO files (`+o.col+`, path, size, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET `+o.col+` = excluded.`+o.col+`, size = excluded.size`,
		o.ID, path, size, s.ms())
	return err
}

// Files returns the tracked files of a movie.
func (s *Store) Files(ctx context.Context, movieID int64) ([]string, error) {
	return s.FilesOf(ctx, ForMovie(movieID))
}

// FilesOf returns the tracked files of a movie or episode.
func (s *Store) FilesOf(ctx context.Context, o Owner) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM files WHERE `+o.col+` = ? ORDER BY id`, o.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UntrackFile forgets a deleted file.
func (s *Store) UntrackFile(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path)
	return err
}

// HistoryEntry is one download attempt for the history page.
type HistoryEntry struct {
	Attempt
	EpisodeID int64  `json:"episodeId,omitempty"`
	SeriesID  int64  `json:"seriesId,omitempty"`
	Kind      string `json:"kind"` // movie, episode
	Label     string `json:"label"`
	Size      int64  `json:"size"`
}

// HistoryFilter selects history entries. Before (an entry ID) pages back.
type HistoryFilter struct {
	Kind, Result string
	Before       int64
	Limit        int
}

// History lists download attempts, newest first.
func (s *Store) History(ctx context.Context, f HistoryFilter) ([]HistoryEntry, error) {
	q := `SELECT id, COALESCE(movie_id, 0), COALESCE(episode_id, 0), COALESCE(series_id, 0), kind, label, size, release_title,
		indexer_name, started_at, finished_at, result, error_kind, message FROM attempts WHERE 1 = 1`
	var args []any
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Result != "" {
		q += ` AND result = ?`
		args = append(args, f.Result)
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var started int64
		var finished sql.NullInt64
		if err := rows.Scan(&h.ID, &h.MovieID, &h.EpisodeID, &h.SeriesID, &h.Kind, &h.Label, &h.Size, &h.ReleaseTitle, &h.IndexerName,
			&started, &finished, &h.Result, &h.ErrorKind, &h.Message); err != nil {
			return nil, err
		}
		h.StartedAt, h.FinishedAt = time.UnixMilli(started), nullTime(finished)
		out = append(out, h)
	}
	return out, rows.Err()
}

// QueueEntry is a movie or episode waiting for a free download slot.
type QueueEntry struct {
	Kind      string `json:"kind"`
	MovieID   int64  `json:"movieId,omitempty"`
	EpisodeID int64  `json:"episodeId,omitempty"`
	SeriesID  int64  `json:"seriesId,omitempty"`
	Label     string `json:"label"`
}

// Queue lists what waits for a download slot, movies first, episodes in
// broadcast order.
func (s *Store) Queue(ctx context.Context) ([]QueueEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT 'movie', id, 0, 0, title || CASE WHEN year > 0 THEN ' (' || year || ')' ELSE '' END
		FROM movies WHERE state = ?
		UNION ALL
		SELECT 'episode', 0, e.id, e.series_id, s.title || ' · S' || printf('%02d', e.season) || 'E' || printf('%02d', e.episode)
		FROM episodes e JOIN series s ON s.id = e.series_id WHERE e.state = ?
		ORDER BY 1 DESC, 5`, MovieQueued, MovieQueued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueueEntry{}
	for rows.Next() {
		var q QueueEntry
		if err := rows.Scan(&q.Kind, &q.MovieID, &q.EpisodeID, &q.SeriesID, &q.Label); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Identity survives search result replacement and distinguishes uploads with
// identical titles. Indexer GUIDs are scoped to their indexer.
func (r Release) Identity() string {
	prefix := strconv.FormatInt(r.IndexerID, 10)
	if r.GUID != "" {
		return prefix + ":guid:" + r.GUID
	}
	return prefix + ":fp:" + r.Fingerprint
}

// SetMovieGenres fills metadata without rewriting concurrently changing state.
func (s *Store) SetMovieGenres(ctx context.Context, id int64, genres []string) error {
	data, err := json.Marshal(genres)
	if err != nil {
		return err
	}
	return affected(s.db.ExecContext(ctx, `UPDATE movies SET genres = ? WHERE id = ?`, string(data), id))
}
