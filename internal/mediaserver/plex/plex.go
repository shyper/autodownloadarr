// Package plex talks to plex.tv (sign-in, server discovery) and to a Plex
// Media Server (libraries, scans, watch state).
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/buildinfo"
	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/mediaserver"
)

// The neutral types are used by the managers; these aliases keep the Plex
// client self-explanatory.
type (
	Item     = mediaserver.Item
	Library  = mediaserver.Library
	Baseline = mediaserver.Baseline
)

// Product is how AutoDownloadarr identifies itself to Plex.
const Product = "AutoDownloadarr"

// DefaultTVURL is the plex.tv API.
const DefaultTVURL = "https://plex.tv"

// Account talks to plex.tv on behalf of this installation.
type Account struct {
	HTTP  *http.Client
	TVURL string
	// DiscoverURL and MetadataURL serve the watchlist (overridable in tests).
	DiscoverURL string
	MetadataURL string
	// ClientID identifies this installation; it must stay the same.
	ClientID string
}

// NewAccount returns a plex.tv client.
func NewAccount(clientID string) *Account {
	return &Account{HTTP: httpx.NewClient(), TVURL: DefaultTVURL, ClientID: clientID}
}

// Pin is a sign-in request. The user approves it on app.plex.tv.
type Pin struct {
	ID      int    `json:"id"`
	Code    string `json:"code"`
	AuthURL string `json:"authUrl"`
}

// CreatePin starts the sign-in.
func (a *Account) CreatePin(ctx context.Context) (Pin, error) {
	var p Pin
	if err := a.do(ctx, http.MethodPost, "/api/v2/pins?strong=true", "", &p); err != nil {
		return Pin{}, err
	}
	q := url.Values{"clientID": {a.ClientID}, "code": {p.Code}, "context[device][product]": {Product}}
	p.AuthURL = "https://app.plex.tv/auth#?" + q.Encode()
	return p, nil
}

// CheckPin returns the account token once the user has approved the pin,
// or "" while it is still pending.
func (a *Account) CheckPin(ctx context.Context, id int) (string, error) {
	var res struct {
		AuthToken *string `json:"authToken"`
	}
	if err := a.do(ctx, http.MethodGet, "/api/v2/pins/"+strconv.Itoa(id), "", &res); err != nil {
		return "", err
	}
	if res.AuthToken == nil {
		return "", nil
	}
	return *res.AuthToken, nil
}

// Server is a Plex Media Server the account can access.
type Server struct {
	Name        string       `json:"name"`
	ID          string       `json:"id"`
	Owned       bool         `json:"owned"`
	AccessToken string       `json:"-"`
	Connections []Connection `json:"connections"`
}

// Connection is one way to reach a server.
type Connection struct {
	URI   string `json:"uri"`
	Local bool   `json:"local"`
	Relay bool   `json:"relay"`
}

// Servers lists the servers of the account.
func (a *Account) Servers(ctx context.Context, token string) ([]Server, error) {
	var res []struct {
		Name             string `json:"name"`
		Provides         string `json:"provides"`
		ClientIdentifier string `json:"clientIdentifier"`
		Owned            bool   `json:"owned"`
		AccessToken      string `json:"accessToken"`
		Connections      []struct {
			URI   string `json:"uri"`
			Local bool   `json:"local"`
			Relay bool   `json:"relay"`
		} `json:"connections"`
	}
	if err := a.do(ctx, http.MethodGet, "/api/v2/resources?includeHttps=1&includeRelay=1", token, &res); err != nil {
		return nil, err
	}
	var servers []Server
	for _, r := range res {
		if !strings.Contains(r.Provides, "server") {
			continue
		}
		s := Server{Name: r.Name, ID: r.ClientIdentifier, Owned: r.Owned, AccessToken: r.AccessToken}
		for _, c := range r.Connections {
			s.Connections = append(s.Connections, Connection{URI: c.URI, Local: c.Local, Relay: c.Relay})
		}
		servers = append(servers, s)
	}
	return servers, nil
}

func (a *Account) do(ctx context.Context, method, path, token string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.TVURL, "/")+path, nil)
	if err != nil {
		return err
	}
	setHeaders(req, a.ClientID, token)
	body, _, err := httpx.Do(a.HTTP, req, 4<<20)
	if err != nil {
		return fmt.Errorf("plex.tv: %w", err)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("plex.tv: decode response: %w", err)
	}
	return nil
}

// Client talks to one Plex Media Server.
type Client struct {
	HTTP     *http.Client
	URL      string
	Token    string
	ClientID string
}

// NewClient returns a server client.
func NewClient(baseURL, token, clientID string) *Client {
	return &Client{HTTP: httpx.NewClient(), URL: baseURL, Token: token, ClientID: clientID}
}

// Identity returns the server's machine identifier and version; it doubles
// as a connection test.
func (c *Client) Identity(ctx context.Context) (id, version string, err error) {
	var res struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
			Version           string `json:"version"`
		} `json:"MediaContainer"`
	}
	if err := c.get(ctx, "/identity", nil, &res); err != nil {
		return "", "", err
	}
	return res.MediaContainer.MachineIdentifier, res.MediaContainer.Version, nil
}

// Libraries lists the library sections.
func (c *Client) Libraries(ctx context.Context) ([]Library, error) {
	var res struct {
		MediaContainer struct {
			Directory []struct {
				Key      string `json:"key"`
				Title    string `json:"title"`
				Type     string `json:"type"`
				Location []struct {
					Path string `json:"path"`
				} `json:"Location"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := c.get(ctx, "/library/sections", nil, &res); err != nil {
		return nil, err
	}
	var libs []Library
	for _, d := range res.MediaContainer.Directory {
		l := Library{Key: d.Key, Title: d.Title, Type: d.Type}
		for _, loc := range d.Location {
			l.Locations = append(l.Locations, loc.Path)
		}
		libs = append(libs, l)
	}
	return libs, nil
}

// Scan asks Plex to scan one folder of a library (a partial scan, much
// faster than scanning the whole library). remotePath is Plex's view.
func (c *Client) Scan(ctx context.Context, libraryKey, remotePath string) error {
	q := url.Values{}
	if remotePath != "" {
		q.Set("path", remotePath)
	}
	return c.get(ctx, "/library/sections/"+url.PathEscape(libraryKey)+"/refresh", q, nil)
}

// Movies lists the movies of a library with the watch state of the account
// the token belongs to.
func (c *Client) Movies(ctx context.Context, libraryKey string) ([]Item, error) {
	return c.items(ctx, libraryKey, "1")
}

// Episodes lists all episodes of a TV library with their watch state and
// resume position.
func (c *Client) Episodes(ctx context.Context, libraryKey string) ([]Item, error) {
	return c.items(ctx, libraryKey, "4")
}

func (c *Client) items(ctx context.Context, libraryKey, typ string) ([]Item, error) {
	var res struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey    string `json:"ratingKey"`
				Title        string `json:"title"`
				Year         int    `json:"year"`
				ViewCount    int    `json:"viewCount"`
				ShowTitle    string `json:"grandparentTitle"`
				ParentIndex  int    `json:"parentIndex"`
				Index        int    `json:"index"`
				LastViewedAt int64  `json:"lastViewedAt"`
				ViewOffset   int64  `json:"viewOffset"`
				Duration     int64  `json:"duration"`
				Media        []struct {
					Part []struct {
						File string `json:"file"`
					} `json:"Part"`
				} `json:"Media"`
				// Plex sends "guid" (its own ID, a string) next to "Guid" (the
				// external IDs). encoding/json matches keys case-insensitively,
				// so "guid" needs a field of its own or it lands in GUID.
				PlexGUID string `json:"guid"`
				GUID     []struct {
					ID string `json:"id"`
				} `json:"Guid"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	q := url.Values{"type": {typ}, "includeGuids": {"1"}}
	if err := c.get(ctx, "/library/sections/"+url.PathEscape(libraryKey)+"/all", q, &res); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(res.MediaContainer.Metadata))
	for _, m := range res.MediaContainer.Metadata {
		it := Item{
			Key:        m.RatingKey,
			Title:      m.Title,
			Year:       m.Year,
			ViewCount:  m.ViewCount,
			ViewOffset: time.Duration(m.ViewOffset) * time.Millisecond,
			Duration:   time.Duration(m.Duration) * time.Millisecond,
			ShowTitle:  m.ShowTitle,
		}
		if typ == "4" {
			it.Season, it.Episode = m.ParentIndex, m.Index
		}
		if m.LastViewedAt > 0 {
			it.LastViewedAt = time.Unix(m.LastViewedAt, 0)
		}
		for _, md := range m.Media {
			for _, p := range md.Part {
				it.Files = append(it.Files, p.File)
			}
		}
		for _, g := range m.GUID {
			it.GUIDs = append(it.GUIDs, g.ID)
		}
		items = append(items, it)
	}
	return items, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	u := strings.TrimRight(c.URL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	setHeaders(req, c.ClientID, c.Token)
	body, _, err := httpx.Do(c.HTTP, req, 32<<20)
	if err != nil {
		return fmt.Errorf("plex: %w", err)
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(body, dst); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("plex: decode response: %w", err)
	}
	return nil
}

func setHeaders(req *http.Request, clientID, token string) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", Product)
	req.Header.Set("X-Plex-Version", buildinfo.Version)
	req.Header.Set("X-Plex-Client-Identifier", clientID)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
}
