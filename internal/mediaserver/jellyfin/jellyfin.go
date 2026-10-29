// Package jellyfin talks to a Jellyfin or Emby Media Server (both use the
// same API). It implements the mediaserver.Server interface.
package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/mediaserver"
)

// ticksPerSecond is the number of 100ns ticks in a second.
const ticksPerSecond = 10_000_000

// Client talks to one Jellyfin or Emby server.
type Client struct {
	HTTP  *http.Client
	URL   string
	Token string
	// UserID selects whose watch state is read. Empty picks an
	// administrator (API keys are not tied to a user).
	UserID string

	mu          sync.Mutex
	userFetched bool
}

// NewClient returns a server client.
func NewClient(baseURL, token string) *Client {
	return &Client{HTTP: httpx.NewClient(), URL: baseURL, Token: token}
}

// Identity returns the server's ID and version; it doubles as a connection
// test.
func (c *Client) Identity(ctx context.Context) (id, version string, err error) {
	var res struct {
		ID      string `json:"Id"`
		Version string `json:"Version"`
	}
	if err := c.do(ctx, http.MethodGet, "/System/Info", nil, nil, &res); err != nil {
		return "", "", err
	}
	return res.ID, res.Version, nil
}

// Libraries lists the virtual folders of the server.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var res []struct {
		Name           string   `json:"Name"`
		ItemID         string   `json:"ItemId"`
		CollectionType string   `json:"CollectionType"`
		Locations      []string `json:"Locations"`
	}
	if err := c.do(ctx, http.MethodGet, "/Library/VirtualFolders", nil, nil, &res); err != nil {
		return nil, err
	}
	libs := make([]mediaserver.Library, 0, len(res))
	for _, l := range res {
		key := l.ItemID
		if key == "" {
			key = l.Name
		}
		libs = append(libs, mediaserver.Library{
			Key:       key,
			Title:     l.Name,
			Type:      libraryType(l.CollectionType),
			Locations: l.Locations,
		})
	}
	return libs, nil
}

func libraryType(collection string) string {
	switch strings.ToLower(collection) {
	case "movies":
		return "movie"
	case "tvshows":
		return "show"
	case "music":
		return "artist"
	case "photos":
		return "photo"
	}
	return strings.ToLower(collection)
}

// Scan asks the server to look at one folder. Jellyfin and Emby accept a
// list of changed paths, which is much faster than a full library scan. An
// empty remotePath refreshes the whole library.
func (c *Client) Scan(ctx context.Context, libraryKey, remotePath string) error {
	if remotePath == "" {
		return c.do(ctx, http.MethodPost, "/Items/"+url.PathEscape(libraryKey)+"/Refresh", nil, nil, nil)
	}
	body := map[string]any{"Updates": []map[string]string{{"Path": remotePath, "UpdateType": "Created"}}}
	return c.do(ctx, http.MethodPost, "/Library/Media/Updated", nil, body, nil)
}

// Movies lists the movies of a library with the watch state of the account
// the token belongs to.
func (c *Client) Movies(ctx context.Context, libraryKey string) ([]mediaserver.Item, error) {
	return c.items(ctx, libraryKey, "Movie")
}

// Episodes lists the episodes of a TV library.
func (c *Client) Episodes(ctx context.Context, libraryKey string) ([]mediaserver.Item, error) {
	return c.items(ctx, libraryKey, "Episode")
}

type apiItem struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Path              string            `json:"Path"`
	ProductionYear    int               `json:"ProductionYear"`
	SeriesName        string            `json:"SeriesName"`
	ParentIndexNumber int               `json:"ParentIndexNumber"`
	IndexNumber       int               `json:"IndexNumber"`
	RunTimeTicks      int64             `json:"RunTimeTicks"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	MediaSources      []struct {
		Path string `json:"Path"`
	} `json:"MediaSources"`
	UserData struct {
		PlayCount             int    `json:"PlayCount"`
		Played                bool   `json:"Played"`
		LastPlayedDate        string `json:"LastPlayedDate"`
		PlaybackPositionTicks int64  `json:"PlaybackPositionTicks"`
	} `json:"UserData"`
}

// userID returns the user whose watch state is read. An API key is not tied
// to a user, so an administrator is used unless one was configured.
func (c *Client) userID(ctx context.Context) string {
	if c.UserID != "" {
		return c.UserID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.userFetched {
		return c.UserID
	}
	c.userFetched = true
	var users []struct {
		ID     string `json:"Id"`
		Policy struct {
			Admin bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.do(ctx, http.MethodGet, "/Users", nil, nil, &users); err != nil {
		return ""
	}
	for _, u := range users {
		if u.Policy.Admin {
			c.UserID = u.ID
			return c.UserID
		}
	}
	if len(users) > 0 {
		c.UserID = users[0].ID
	}
	return c.UserID
}

func (c *Client) items(ctx context.Context, libraryKey, typ string) ([]mediaserver.Item, error) {
	q := url.Values{
		"ParentId":            {libraryKey},
		"Recursive":           {"true"},
		"IncludeItemTypes":    {typ},
		"Fields":              {"Path,ProviderIds,MediaSources,UserData,RunTimeTicks"},
		"EnableImages":        {"false"},
		"EnableUserData":      {"true"},
		"CollapseBoxSetItems": {"false"},
	}
	if id := c.userID(ctx); id != "" {
		q.Set("UserId", id)
	}
	var res struct {
		Items []apiItem `json:"Items"`
	}
	if err := c.do(ctx, http.MethodGet, "/Items", q, nil, &res); err != nil {
		return nil, err
	}
	items := make([]mediaserver.Item, 0, len(res.Items))
	for _, m := range res.Items {
		it := mediaserver.Item{
			Key:        m.ID,
			Title:      m.Name,
			Year:       m.ProductionYear,
			ViewCount:  m.UserData.PlayCount,
			ViewOffset: time.Duration(m.UserData.PlaybackPositionTicks) * 100 * time.Nanosecond,
			Duration:   time.Duration(m.RunTimeTicks) * 100 * time.Nanosecond,
			ShowTitle:  m.SeriesName,
			Season:     m.ParentIndexNumber,
			Episode:    m.IndexNumber,
		}
		if it.ViewCount == 0 && m.UserData.Played {
			it.ViewCount = 1
		}
		if m.UserData.LastPlayedDate != "" {
			if t, err := time.Parse(time.RFC3339Nano, m.UserData.LastPlayedDate); err == nil {
				it.LastViewedAt = t
			}
		}
		for _, src := range m.MediaSources {
			if src.Path != "" {
				it.Files = append(it.Files, src.Path)
			}
		}
		if len(it.Files) == 0 && m.Path != "" {
			it.Files = append(it.Files, m.Path)
		}
		it.GUIDs = guids(m.ProviderIDs)
		items = append(items, it)
	}
	return items, nil
}

// guids renders the provider IDs in the same form Plex uses, so the rest
// of the code does not need to know the server type.
func guids(ids map[string]string) []string {
	var out []string
	for _, p := range []struct{ key, prefix string }{
		{"tmdb", "tmdb://"},
		{"imdb", "imdb://"},
		{"tvdb", "tvdb://"},
	} {
		for k, v := range ids {
			if strings.EqualFold(k, p.key) && v != "" {
				out = append(out, p.prefix+v)
			}
		}
	}
	return out
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, dst any) error {
	u := strings.TrimRight(c.URL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("jellyfin: encode request: %w", err)
		}
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Emby-Token", c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, _, err := httpx.Do(c.HTTP, req, 32<<20)
	if err != nil {
		return fmt.Errorf("jellyfin: %w", err)
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(resp, dst); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("jellyfin: decode response: %w", err)
	}
	return nil
}
