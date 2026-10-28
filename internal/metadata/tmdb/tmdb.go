// Package tmdb is a client for The Movie Database API (v3), used to search
// movies and to look up their IDs, titles and runtime.
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

// DefaultBaseURL is the TMDB API endpoint.
const DefaultBaseURL = "https://api.themoviedb.org/3"

// ImageBaseURL prefixes poster paths.
const ImageBaseURL = "https://image.tmdb.org/t/p/"

// Client talks to TMDB. Both a v3 API key and a v4 read access token work.
type Client struct {
	HTTP     *http.Client
	BaseURL  string
	Key      string
	Language string // e.g. "de-DE"
}

// New returns a client for the public API.
func New(key, language string) *Client {
	return &Client{HTTP: httpx.NewClient(), BaseURL: DefaultBaseURL, Key: key, Language: language}
}

// Movie is the subset of TMDB data AutoDownloadarr uses.
type Movie struct {
	ID            int      `json:"tmdbId"`
	IMDbID        string   `json:"imdbId,omitempty"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"originalTitle,omitempty"`
	Year          int      `json:"year,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	PosterPath    string   `json:"posterPath,omitempty"`
	Runtime       int      `json:"runtime,omitempty"` // minutes, only from Movie()
	AltTitles     []string `json:"altTitles,omitempty"`
}

type apiMovie struct {
	ID            int    `json:"id"`
	IMDbID        string `json:"imdb_id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
	Runtime       int    `json:"runtime"`
	Alternative   struct {
		Titles []struct {
			Title string `json:"title"`
		} `json:"titles"`
	} `json:"alternative_titles"`
}

func (m apiMovie) convert() Movie {
	out := Movie{
		ID:            m.ID,
		IMDbID:        m.IMDbID,
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Overview:      m.Overview,
		PosterPath:    m.PosterPath,
		Runtime:       m.Runtime,
	}
	if len(m.ReleaseDate) >= 4 {
		out.Year, _ = strconv.Atoi(m.ReleaseDate[:4])
	}
	seen := map[string]bool{strings.ToLower(m.Title): true}
	for _, t := range append([]string{m.OriginalTitle}, titles(m)...) {
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out.AltTitles = append(out.AltTitles, t)
		}
	}
	return out
}

func titles(m apiMovie) []string {
	var out []string
	for _, t := range m.Alternative.Titles {
		out = append(out, t.Title)
	}
	return out
}

// SearchMovies searches by title; year narrows the results if not 0.
func (c *Client) SearchMovies(ctx context.Context, query string, year int) ([]Movie, error) {
	q := url.Values{"query": {query}, "include_adult": {"false"}}
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	var res struct {
		Results []apiMovie `json:"results"`
	}
	if err := c.get(ctx, "/search/movie", q, &res); err != nil {
		return nil, err
	}
	movies := make([]Movie, 0, len(res.Results))
	for _, m := range res.Results {
		movies = append(movies, m.convert())
	}
	return movies, nil
}

// Movie returns the details of a movie including IMDb ID, runtime and
// alternative titles.
func (c *Client) Movie(ctx context.Context, id int) (Movie, error) {
	var m apiMovie
	err := c.get(ctx, "/movie/"+strconv.Itoa(id), url.Values{"append_to_response": {"alternative_titles"}}, &m)
	if err != nil {
		return Movie{}, err
	}
	return m.convert(), nil
}

// Test checks the API key.
func (c *Client) Test(ctx context.Context) error {
	return c.get(ctx, "/configuration", nil, &struct{}{})
}

func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	if c.Key == "" {
		return fmt.Errorf("tmdb: %w: no API key configured", httpx.ErrUnauthorized)
	}
	if q == nil {
		q = url.Values{}
	}
	if c.Language != "" {
		q.Set("language", c.Language)
	}
	bearer := strings.HasPrefix(c.Key, "eyJ") // v4 read access token (JWT)
	if !bearer {
		q.Set("api_key", c.Key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	body, _, err := httpx.Do(c.HTTP, req, 4<<20)
	if err != nil {
		return fmt.Errorf("tmdb: %w", err)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("tmdb: decode response: %w", err)
	}
	return nil
}

// PosterURL returns the URL of a poster in the given width (e.g. "w342").
func PosterURL(path, size string) string {
	if path == "" {
		return ""
	}
	return ImageBaseURL + size + path
}
