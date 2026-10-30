// Package control exposes media operations with a caller identity, shared by MCP and bots.
package control

import (
	"context"
	"errors"
	"strings"

	"github.com/shyper/autodownloadarr/internal/access"
	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

type Service struct {
	Store    *store.Store
	Services *movies.Services
	Movies   *movies.Manager
	Series   *series.Manager
}
type Input struct {
	ID        int64                 `json:"id,omitempty" jsonschema:"local movie series or episode ID for existing items"`
	TMDBID    int                   `json:"tmdbId,omitempty" jsonschema:"TMDB identifier for requesting a title or reading catalog details"`
	Query     string                `json:"query,omitempty"`
	ReleaseID int64                 `json:"releaseId,omitempty"`
	Keep      bool                  `json:"keep,omitempty"`
	Forget    bool                  `json:"forget,omitempty" jsonschema:"also remove request and history instead of only deleting movie files"`
	Options   series.RequestOptions `json:"options,omitempty"`
	Settings  *series.Settings      `json:"settings,omitempty"`
}
type Operation struct {
	Name        string
	Scope       string
	Description string
}

var Operations = []Operation{
	{"search_movies", "media:read", "Search TMDB movies by query."},
	{"search_series", "media:read", "Search TMDB TV series by query."},
	{"movie_catalog_details", "media:read", "Read TMDB movie details using tmdbId."},
	{"series_catalog_details", "media:read", "Read TMDB series details and seasons using tmdbId."},
	{"list_movies", "media:read", "List the shared movie library."},
	{"list_series", "media:read", "List the shared series library."},
	{"movie_details", "media:read", "Read a requested movie and its releases and attempts by local id."},
	{"series_details", "media:read", "Read a requested series and episodes by local id."},
	{"episode_details", "media:read", "Read an episode and its releases and attempts by local id."},
	{"downloads", "media:read", "Read current movie and episode downloads."},
	{"queue", "media:read", "Read the pending download queue."},
	{"history", "media:read", "Read the latest 50 download attempts."},
	{"profiles", "media:read", "List quality profiles."},
	{"request_movie", "media:request", "Request a movie using tmdbId. Existing requests are returned without changing ownership."},
	{"request_series", "media:request", "Request a series using tmdbId and options including seasons and rolling window."},
	{"keep_movie", "media:manage", "Set keep for a movie using local id and keep."},
	{"update_series", "media:manage", "Replace series settings by local id. Supply a complete settings object from series_details."},
	{"search_movie", "media:manage", "Retry a movie search by local id."},
	{"search_episode", "media:manage", "Retry an episode search by local id."},
	{"skip_movie", "media:manage", "Skip the current movie release by local id."},
	{"skip_episode", "media:manage", "Skip the current episode release by local id."},
	{"download_movie_release", "media:manage", "Download a selected movie release using local id and releaseId."},
	{"download_episode_release", "media:manage", "Download a selected episode release using local id and releaseId."},
	{"delete_movie", "media:delete", "Delete a movie's files using local id. forget also removes the request. Destructive."},
	{"delete_series", "media:delete", "Delete a series, its files and request using local id. Destructive."},
}

func (s *Service) Execute(ctx context.Context, u auth.User, name string, in Input) (any, error) {
	if u.ID <= 0 {
		return nil, access.ErrForbidden
	}
	switch name {
	case "search_movies", "search_series", "movie_catalog_details", "series_catalog_details":
		if strings.HasPrefix(name, "search_") && (len(strings.TrimSpace(in.Query)) < 2 || len(in.Query) > 200) {
			return nil, store.ErrInvalid
		}
		if strings.Contains(name, "catalog") && in.TMDBID <= 0 {
			return nil, store.ErrInvalid
		}
		c, err := s.Services.TMDB(ctx)
		if err != nil {
			return nil, err
		}
		switch name {
		case "search_movies":
			return c.SearchMovies(ctx, in.Query, 0)
		case "search_series":
			return c.SearchShows(ctx, in.Query)
		case "movie_catalog_details":
			return c.Movie(ctx, in.TMDBID)
		default:
			return c.Show(ctx, in.TMDBID)
		}
	case "list_movies":
		return s.Store.Movies(ctx)
	case "list_series":
		return s.Store.SeriesList(ctx)
	case "downloads":
		return map[string]any{"movies": s.Movies.Downloads(), "episodes": s.Series.Downloads()}, nil
	case "queue":
		return s.Store.Queue(ctx)
	case "history":
		return s.Store.History(ctx, store.HistoryFilter{Limit: 50})
	case "profiles":
		return s.Store.Profiles(ctx)
	case "request_movie":
		if in.TMDBID <= 0 {
			return nil, store.ErrInvalid
		}
		if old, err := s.Store.MovieByTMDB(ctx, in.TMDBID); err == nil && old.State == store.MovieDeleted && !access.CanManage(u, old.RequestedBy) {
			return nil, access.ErrForbidden
		}
		mv, err := s.Movies.Request(ctx, in.TMDBID, u.ID, in.Options.ProfileID)
		if errors.Is(err, movies.ErrAlreadyRequested) {
			err = nil
		}
		return mv, err
	case "request_series":
		if in.TMDBID <= 0 {
			return nil, store.ErrInvalid
		}
		se, err := s.Series.Request(ctx, in.TMDBID, u.ID, in.Options)
		if errors.Is(err, series.ErrAlreadyRequested) {
			err = nil
		}
		return se, err
	}
	if in.ID <= 0 {
		return nil, store.ErrInvalid
	}
	kind := "movie"
	if strings.Contains(name, "episode") {
		kind = "episode"
	} else if strings.Contains(name, "series") {
		kind = "series"
	}
	switch name {
	case "movie_details":
		mv, err := s.Store.Movie(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		rels, err := s.Store.Releases(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		attempts, err := s.Store.Attempts(ctx, in.ID)
		return map[string]any{"movie": mv, "releases": rels, "attempts": attempts}, err
	case "series_details":
		se, err := s.Store.Series(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		eps, err := s.Store.Episodes(ctx, in.ID)
		return map[string]any{"series": se, "episodes": eps}, err
	case "episode_details":
		ep, err := s.Store.Episode(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		rels, err := s.Store.ReleasesOf(ctx, store.ForEpisode(in.ID))
		if err != nil {
			return nil, err
		}
		attempts, err := s.Store.AttemptsOf(ctx, store.ForEpisode(in.ID))
		return map[string]any{"episode": ep, "releases": rels, "attempts": attempts}, err
	}
	if err := access.Check(ctx, s.Store, u, kind, in.ID); err != nil {
		return nil, err
	}
	switch name {
	case "keep_movie":
		return s.Movies.SetKeep(ctx, in.ID, in.Keep)
	case "update_series":
		if in.Settings == nil {
			return nil, store.ErrInvalid
		}
		return s.Series.Update(ctx, in.ID, *in.Settings)
	case "search_movie":
		return s.Movies.Search(ctx, in.ID)
	case "search_episode":
		return s.Series.SearchEpisode(ctx, in.ID)
	case "skip_movie":
		return nil, s.Movies.SkipRelease(ctx, in.ID)
	case "skip_episode":
		return nil, s.Series.SkipRelease(ctx, in.ID)
	case "download_movie_release":
		if in.ReleaseID <= 0 {
			return nil, store.ErrInvalid
		}
		return s.Movies.DownloadRelease(ctx, in.ID, in.ReleaseID)
	case "download_episode_release":
		if in.ReleaseID <= 0 {
			return nil, store.ErrInvalid
		}
		return s.Series.DownloadRelease(ctx, in.ID, in.ReleaseID)
	case "delete_movie":
		return nil, s.Movies.Delete(ctx, in.ID, in.Forget)
	case "delete_series":
		return nil, s.Series.Delete(ctx, in.ID)
	}
	return nil, store.ErrInvalid
}

// ErrorCode avoids sending upstream URLs, API keys or filesystem errors to clients.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, access.ErrForbidden):
		return "forbidden"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	case errors.Is(err, store.ErrQuotaExceeded):
		return "quota_exceeded"
	case errors.Is(err, store.ErrInvalid):
		return "invalid_input"
	case errors.Is(err, movies.ErrNotConfigured):
		return "not_configured"
	default:
		return "operation_failed"
	}
}
