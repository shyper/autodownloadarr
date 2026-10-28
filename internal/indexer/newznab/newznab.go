// Package newznab is a client for Newznab-compatible indexers (including
// Prowlarr's per-indexer Newznab endpoints).
package newznab

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

// Client queries one indexer.
type Client struct {
	HTTP   *http.Client
	Name   string
	URL    string // base URL; "/api" is appended unless present
	APIKey string
}

// New returns a client.
func New(name, baseURL, apiKey string) *Client {
	return &Client{HTTP: httpx.NewClient(), Name: name, URL: baseURL, APIKey: apiKey}
}

// Caps are the indexer's capabilities.
type Caps struct {
	MovieSearch bool       `json:"movieSearch"`
	MovieParams []string   `json:"movieParams"` // e.g. q, imdbid, tmdbid
	TVSearch    bool       `json:"tvSearch"`
	TVParams    []string   `json:"tvParams"` // e.g. q, season, ep, tvdbid, imdbid
	Categories  []Category `json:"categories"`
}

// Category is a Newznab category with its subcategories.
type Category struct {
	ID   int        `json:"id"`
	Name string     `json:"name"`
	Subs []Category `json:"subs,omitempty"`
}

// Item is one search result.
type Item struct {
	Title      string
	GUID       string
	Link       string // NZB download URL
	Size       int64
	PubDate    time.Time
	Grabs      int
	Passworded bool
	Categories []int
}

// MovieQuery describes a movie search. The strongest identifier the indexer
// supports is used.
type MovieQuery struct {
	IMDbID     string // "tt1234567"
	TMDBID     int
	Title      string
	Year       int
	Categories []int
}

// Caps fetches the capabilities.
func (c *Client) Caps(ctx context.Context) (Caps, error) {
	var doc struct {
		Searching struct {
			Movie struct {
				Available string `xml:"available,attr"`
				Params    string `xml:"supportedParams,attr"`
			} `xml:"movie-search"`
			TV struct {
				Available string `xml:"available,attr"`
				Params    string `xml:"supportedParams,attr"`
			} `xml:"tv-search"`
		} `xml:"searching"`
		Categories []struct {
			ID     int    `xml:"id,attr"`
			Name   string `xml:"name,attr"`
			Subcat []struct {
				ID   int    `xml:"id,attr"`
				Name string `xml:"name,attr"`
			} `xml:"subcat"`
		} `xml:"categories>category"`
	}
	if err := c.get(ctx, url.Values{"t": {"caps"}}, &doc); err != nil {
		return Caps{}, err
	}
	caps := Caps{MovieSearch: doc.Searching.Movie.Available == "yes", TVSearch: doc.Searching.TV.Available == "yes"}
	caps.MovieParams = splitParams(doc.Searching.Movie.Params)
	caps.TVParams = splitParams(doc.Searching.TV.Params)
	for _, cat := range doc.Categories {
		c := Category{ID: cat.ID, Name: cat.Name}
		for _, s := range cat.Subcat {
			c.Subs = append(c.Subs, Category{ID: s.ID, Name: s.Name})
		}
		caps.Categories = append(caps.Categories, c)
	}
	return caps, nil
}

// SearchMovie searches for a movie. caps decides between an ID search and a
// free-text search ("title year").
func (c *Client) SearchMovie(ctx context.Context, caps Caps, q MovieQuery) ([]Item, error) {
	v := url.Values{"extended": {"1"}, "limit": {"100"}}
	if len(q.Categories) > 0 {
		cats := make([]string, len(q.Categories))
		for i, id := range q.Categories {
			cats[i] = strconv.Itoa(id)
		}
		v.Set("cat", strings.Join(cats, ","))
	}
	switch {
	case caps.MovieSearch && q.IMDbID != "" && slices.Contains(caps.MovieParams, "imdbid"):
		v.Set("t", "movie")
		v.Set("imdbid", strings.TrimPrefix(q.IMDbID, "tt"))
	case caps.MovieSearch && q.TMDBID != 0 && slices.Contains(caps.MovieParams, "tmdbid"):
		v.Set("t", "movie")
		v.Set("tmdbid", strconv.Itoa(q.TMDBID))
	default:
		v.Set("t", "search")
		query := q.Title
		if q.Year > 0 {
			query += " " + strconv.Itoa(q.Year)
		}
		v.Set("q", query)
	}
	return c.search(ctx, v)
}

func splitParams(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(strings.ToLower(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// EpisodeQuery describes a TV search. Episode 0 searches for season packs.
type EpisodeQuery struct {
	TVDBID     int
	IMDbID     string
	Title      string
	Season     int
	Episode    int
	Categories []int
}

// SearchEpisode searches an episode (or a whole season) by TVDB or IMDb ID
// where supported, else by "Title S01E02".
func (c *Client) SearchEpisode(ctx context.Context, caps Caps, q EpisodeQuery) ([]Item, error) {
	v := url.Values{"extended": {"1"}, "limit": {"100"}}
	if len(q.Categories) > 0 {
		cats := make([]string, len(q.Categories))
		for i, id := range q.Categories {
			cats[i] = strconv.Itoa(id)
		}
		v.Set("cat", strings.Join(cats, ","))
	}
	idSearch := caps.TVSearch && slices.Contains(caps.TVParams, "season")
	switch {
	case idSearch && q.TVDBID != 0 && slices.Contains(caps.TVParams, "tvdbid"):
		v.Set("tvdbid", strconv.Itoa(q.TVDBID))
	case idSearch && q.IMDbID != "" && slices.Contains(caps.TVParams, "imdbid"):
		v.Set("imdbid", strings.TrimPrefix(q.IMDbID, "tt"))
	default:
		idSearch = false
	}
	if idSearch {
		v.Set("t", "tvsearch")
		v.Set("season", strconv.Itoa(q.Season))
		if q.Episode > 0 {
			v.Set("ep", strconv.Itoa(q.Episode))
		}
		return c.search(ctx, v)
	}
	query := fmt.Sprintf("%s S%02d", q.Title, q.Season)
	if q.Episode > 0 {
		query += fmt.Sprintf("E%02d", q.Episode)
	}
	v.Set("t", "search")
	v.Set("q", query)
	return c.search(ctx, v)
}

// SearchText runs a free-text search.
func (c *Client) SearchText(ctx context.Context, query string, categories []int) ([]Item, error) {
	return c.SearchMovie(ctx, Caps{}, MovieQuery{Title: query, Categories: categories})
}

func (c *Client) search(ctx context.Context, v url.Values) ([]Item, error) {
	var doc struct {
		Items []struct {
			Title     string `xml:"title"`
			GUID      string `xml:"guid"`
			Link      string `xml:"link"`
			PubDate   string `xml:"pubDate"`
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
			Attrs []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"attr"`
		} `xml:"channel>item"`
	}
	if err := c.get(ctx, v, &doc); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(doc.Items))
	for _, raw := range doc.Items {
		it := Item{Title: strings.TrimSpace(raw.Title), GUID: raw.GUID, Link: raw.Link, Size: raw.Enclosure.Length}
		if it.Link == "" {
			it.Link = raw.Enclosure.URL
		}
		if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(raw.PubDate)); err == nil {
			it.PubDate = t
		}
		for _, a := range raw.Attrs {
			switch strings.ToLower(a.Name) {
			case "size":
				if n, err := strconv.ParseInt(a.Value, 10, 64); err == nil && n > 0 {
					it.Size = n
				}
			case "grabs":
				it.Grabs, _ = strconv.Atoi(a.Value)
			case "password":
				it.Passworded = a.Value != "" && a.Value != "0"
			case "category":
				if n, err := strconv.Atoi(a.Value); err == nil {
					it.Categories = append(it.Categories, n)
				}
			}
		}
		if it.GUID == "" {
			it.GUID = it.Link
		}
		if it.Title != "" && it.Link != "" {
			items = append(items, it)
		}
	}
	return items, nil
}

// FetchNZB downloads the NZB of a result. The returned file name comes
// from Content-Disposition and may carry a password ("name{{pw}}.nzb").
func (c *Client) FetchNZB(ctx context.Context, link string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, "", err
	}
	body, header, err := httpx.Do(c.HTTP, req, 64<<20)
	if err != nil {
		if apiErr := parseError(body); apiErr != nil {
			return nil, "", fmt.Errorf("%s: %w", c.Name, apiErr)
		}
		return nil, "", fmt.Errorf("%s: fetch NZB: %w", c.Name, err)
	}
	if apiErr := parseError(body); apiErr != nil {
		return nil, "", fmt.Errorf("%s: %w", c.Name, apiErr)
	}
	var name string
	if _, params, err := mime.ParseMediaType(header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	return body, name, nil
}

func (c *Client) endpoint() (string, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%s: invalid URL %q", c.Name, c.URL)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/api") {
		u.Path += "/api"
	}
	return u.String(), nil
}

func (c *Client) get(ctx context.Context, v url.Values, dst any) error {
	base, err := c.endpoint()
	if err != nil {
		return err
	}
	v.Set("apikey", c.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+v.Encode(), nil)
	if err != nil {
		return err
	}
	body, _, err := httpx.Do(c.HTTP, req, 20<<20)
	if apiErr := parseError(body); apiErr != nil {
		return fmt.Errorf("%s: %w", c.Name, apiErr)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	if err := xml.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%s: invalid response: %w", c.Name, err)
	}
	return nil
}

// APIError is an <error code=".." description=".."/> response.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string { return fmt.Sprintf("indexer error %d: %s", e.Code, e.Description) }

// Unwrap classifies the error code (Newznab API specification).
func (e *APIError) Unwrap() error {
	switch {
	case e.Code >= 100 && e.Code <= 102:
		return httpx.ErrUnauthorized
	case e.Code == 429 || e.Code == 500 || e.Code == 501:
		return httpx.ErrRateLimited
	case e.Code == 300:
		return httpx.ErrNotFound
	}
	return nil
}

func parseError(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || !bytes.Contains(trimmed[:min(len(trimmed), 512)], []byte("<error")) {
		return nil
	}
	var e struct {
		XMLName     xml.Name `xml:"error"`
		Code        int      `xml:"code,attr"`
		Description string   `xml:"description,attr"`
	}
	if err := xml.Unmarshal(trimmed, &e); err != nil {
		return nil
	}
	return &APIError{Code: e.Code, Description: e.Description}
}

// IsConfigError reports whether err means the indexer is misconfigured.
func IsConfigError(err error) bool { return errors.Is(err, httpx.ErrUnauthorized) }
