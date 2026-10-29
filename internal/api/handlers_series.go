package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shyper/autodownloadarr/internal/metadata/tmdb"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

type showResult struct {
	tmdb.Show
	PosterURL string `json:"posterUrl,omitempty"`
	SeriesID  int64  `json:"seriesId,omitempty"`
}

func (s *server) handleSearchShows(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []showResult{})
		return
	}
	client, err := s.Services.TMDB(r.Context())
	if err != nil {
		s.writeStoreError(w, "tmdb", err)
		return
	}
	found, err := client.SearchShows(r.Context(), q)
	if err != nil {
		s.writeStoreError(w, "search shows", err)
		return
	}
	out := make([]showResult, 0, len(found))
	for _, sh := range found {
		res := showResult{Show: sh, PosterURL: tmdb.PosterURL(sh.PosterPath, "w342")}
		if existing, err := s.Store.SeriesByTMDB(r.Context(), sh.ID); err == nil {
			res.SeriesID = existing.ID
		}
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleShowDetails returns the seasons of a show for the request dialog.
func (s *server) handleShowDetails(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "tmdbId"))
	client, err := s.Services.TMDB(r.Context())
	if err != nil {
		s.writeStoreError(w, "tmdb", err)
		return
	}
	show, err := client.Show(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "show details", err)
		return
	}
	writeJSON(w, http.StatusOK, showResult{Show: show, PosterURL: tmdb.PosterURL(show.PosterPath, "w342")})
}

// seriesView adds the poster and episode counts per state.
type seriesView struct {
	store.Series
	PosterURL string         `json:"posterUrl,omitempty"`
	Counts    map[string]int `json:"counts"`
	// Current is the episode being watched or next up, Upcoming the next
	// one still to be broadcast.
	Current  *episodeRef `json:"current,omitempty"`
	Upcoming *episodeRef `json:"upcoming,omitempty"`
}

type episodeRef struct {
	ID       int64   `json:"id"`
	Season   int     `json:"season"`
	Episode  int     `json:"episode"`
	Progress float64 `json:"progress"`
	AirDate  string  `json:"airDate,omitempty"`
}

func refOf(e *store.Episode) *episodeRef {
	if e == nil {
		return nil
	}
	return &episodeRef{ID: e.ID, Season: e.Season, Episode: e.Episode, Progress: e.Progress, AirDate: e.AirDate}
}

func (s *server) seriesView(r *http.Request, se store.Series) (seriesView, []store.Episode, error) {
	eps, err := s.Store.Episodes(r.Context(), se.ID)
	if err != nil {
		return seriesView{}, nil, err
	}
	v := seriesView{Series: se, PosterURL: tmdb.PosterURL(se.PosterPath, "w342"), Counts: map[string]int{}}
	for _, e := range eps {
		v.Counts[e.State]++
	}
	current, upcoming := series.Position(se, eps, time.Now())
	v.Current, v.Upcoming = refOf(current), refOf(upcoming)
	return v, eps, nil
}

func (s *server) handleListSeries(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.SeriesList(r.Context())
	if err != nil {
		s.internalError(w, "list series", err)
		return
	}
	out := make([]seriesView, 0, len(list))
	for _, se := range list {
		v, _, err := s.seriesView(r, se)
		if err != nil {
			s.internalError(w, "list series", err)
			return
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

type requestSeries struct {
	TMDBID int `json:"tmdbId"`
	series.RequestOptions
}

func (s *server) handleRequestSeries(w http.ResponseWriter, r *http.Request) {
	var req requestSeries
	if !decodeJSON(w, r, &req) {
		return
	}
	user, _, _ := currentUser(r.Context())
	se, err := s.Series.Request(r.Context(), req.TMDBID, user.ID, req.RequestOptions)
	if errors.Is(err, series.ErrAlreadyRequested) {
		v, _, _ := s.seriesView(r, se)
		writeJSON(w, http.StatusOK, v)
		return
	}
	if err != nil {
		s.writeStoreError(w, "request series", err)
		return
	}
	v, _, err := s.seriesView(r, se)
	if err != nil {
		s.internalError(w, "read series", err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

type seriesDetail struct {
	seriesView
	Episodes []store.Episode `json:"episodes"`
}

func (s *server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	se, err := s.Store.Series(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "read series", err)
		return
	}
	v, eps, err := s.seriesView(r, se)
	if err != nil {
		s.internalError(w, "read episodes", err)
		return
	}
	writeJSON(w, http.StatusOK, seriesDetail{seriesView: v, Episodes: eps})
}

func (s *server) handleUpdateSeries(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	var st series.Settings
	if !decodeJSON(w, r, &st) {
		return
	}
	if st.Seasons == nil {
		st.Seasons = []int{}
	}
	if _, err := s.Series.Update(r.Context(), id, st); err != nil {
		s.writeStoreError(w, "update series", err)
		return
	}
	s.handleGetSeries(w, r)
}

func (s *server) handleDeleteSeries(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	if err := s.Series.Delete(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete series", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type episodeDetail struct {
	store.Episode
	Releases  []store.Release        `json:"releases"`
	Attempts  []store.Attempt        `json:"attempts"`
	Blocklist []store.BlocklistEntry `json:"blocklist"`
}

func (s *server) handleGetEpisode(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	e, err := s.Store.Episode(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "read episode", err)
		return
	}
	d := episodeDetail{Episode: e}
	o := store.ForEpisode(id)
	if d.Releases, err = s.Store.ReleasesOf(r.Context(), o); err != nil {
		s.internalError(w, "read releases", err)
		return
	}
	if d.Attempts, err = s.Store.AttemptsOf(r.Context(), o); err != nil {
		s.internalError(w, "read attempts", err)
		return
	}
	if d.Blocklist, err = s.Store.BlocklistOf(r.Context(), o); err != nil {
		s.internalError(w, "read blocklist", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) handleEpisodeSearch(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	e, err := s.Series.SearchEpisode(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, "search episode", err)
			return
		}
		writeError(w, http.StatusConflict, "invalid_state", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *server) handleEpisodeSkip(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	if err := s.Series.SkipRelease(r.Context(), id); err != nil {
		writeError(w, http.StatusConflict, "invalid_state", err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) handleEpisodeDownloadRelease(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	releaseID, _ := idParam(r, "releaseId")
	e, err := s.Series.DownloadRelease(r.Context(), id, releaseID)
	if err != nil {
		s.writeStoreError(w, "download release", err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *server) handleEpisodeDownloads(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Series.Downloads())
}
