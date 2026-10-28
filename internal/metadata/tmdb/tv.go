package tmdb

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Show is a TV series.
type Show struct {
	ID            int          `json:"tmdbId"`
	TVDBID        int          `json:"tvdbId,omitempty"`
	IMDbID        string       `json:"imdbId,omitempty"`
	Title         string       `json:"title"`
	OriginalTitle string       `json:"originalTitle,omitempty"`
	Year          int          `json:"year,omitempty"`
	Overview      string       `json:"overview,omitempty"`
	PosterPath    string       `json:"posterPath,omitempty"`
	Status        string       `json:"status,omitempty"` // e.g. "Returning Series", "Ended"
	Runtime       int          `json:"runtime,omitempty"`
	AltTitles     []string     `json:"altTitles,omitempty"`
	Seasons       []SeasonInfo `json:"seasons,omitempty"` // only from Show()
}

// SeasonInfo summarises a season.
type SeasonInfo struct {
	Number       int    `json:"number"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episodeCount"`
	AirDate      string `json:"airDate,omitempty"`
}

// Episode is one episode of a season.
type Episode struct {
	Season  int    `json:"season"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	AirDate string `json:"airDate,omitempty"` // YYYY-MM-DD, empty if unknown
	Runtime int    `json:"runtime,omitempty"`
}

type apiShow struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	OriginalName   string `json:"original_name"`
	FirstAirDate   string `json:"first_air_date"`
	Overview       string `json:"overview"`
	PosterPath     string `json:"poster_path"`
	Status         string `json:"status"`
	EpisodeRuntime []int  `json:"episode_run_time"`
	Seasons        []struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		EpisodeCount int    `json:"episode_count"`
		AirDate      string `json:"air_date"`
	} `json:"seasons"`
	ExternalIDs struct {
		TVDBID int    `json:"tvdb_id"`
		IMDbID string `json:"imdb_id"`
	} `json:"external_ids"`
	Alternative struct {
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
	} `json:"alternative_titles"`
}

func (s apiShow) convert() Show {
	out := Show{
		ID: s.ID, TVDBID: s.ExternalIDs.TVDBID, IMDbID: s.ExternalIDs.IMDbID, Title: s.Name, OriginalTitle: s.OriginalName,
		Overview: s.Overview, PosterPath: s.PosterPath, Status: s.Status,
	}
	if len(s.FirstAirDate) >= 4 {
		out.Year, _ = strconv.Atoi(s.FirstAirDate[:4])
	}
	if len(s.EpisodeRuntime) > 0 {
		out.Runtime = s.EpisodeRuntime[0]
	}
	seen := map[string]bool{strings.ToLower(s.Name): true}
	alt := []string{s.OriginalName}
	for _, t := range s.Alternative.Results {
		alt = append(alt, t.Title)
	}
	for _, t := range alt {
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out.AltTitles = append(out.AltTitles, t)
		}
	}
	for _, se := range s.Seasons {
		out.Seasons = append(out.Seasons, SeasonInfo{Number: se.SeasonNumber, Name: se.Name, EpisodeCount: se.EpisodeCount, AirDate: se.AirDate})
	}
	return out
}

// SearchShows searches TV series by title.
func (c *Client) SearchShows(ctx context.Context, query string) ([]Show, error) {
	var res struct {
		Results []apiShow `json:"results"`
	}
	if err := c.get(ctx, "/search/tv", url.Values{"query": {query}, "include_adult": {"false"}}, &res); err != nil {
		return nil, err
	}
	out := make([]Show, 0, len(res.Results))
	for _, s := range res.Results {
		out = append(out, s.convert())
	}
	return out, nil
}

// Show returns a series with its seasons and external IDs.
func (c *Client) Show(ctx context.Context, id int) (Show, error) {
	var s apiShow
	if err := c.get(ctx, "/tv/"+strconv.Itoa(id), url.Values{"append_to_response": {"external_ids,alternative_titles"}}, &s); err != nil {
		return Show{}, err
	}
	return s.convert(), nil
}

// Season returns the episodes of a season.
func (c *Client) Season(ctx context.Context, showID, season int) ([]Episode, error) {
	var res struct {
		Episodes []struct {
			SeasonNumber  int    `json:"season_number"`
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
			AirDate       string `json:"air_date"`
			Runtime       int    `json:"runtime"`
		} `json:"episodes"`
	}
	if err := c.get(ctx, "/tv/"+strconv.Itoa(showID)+"/season/"+strconv.Itoa(season), nil, &res); err != nil {
		return nil, err
	}
	out := make([]Episode, 0, len(res.Episodes))
	for _, e := range res.Episodes {
		out = append(out, Episode{Season: e.SeasonNumber, Number: e.EpisodeNumber, Title: e.Name, AirDate: e.AirDate, Runtime: e.Runtime})
	}
	return out, nil
}
