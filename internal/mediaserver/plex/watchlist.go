package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

// DefaultDiscoverURL is where Plex serves the account's watchlist.
const DefaultDiscoverURL = "https://discover.provider.plex.tv"

// DefaultMetadataURL resolves IDs of watchlist entries that carry no
// external GUID in the list response.
const DefaultMetadataURL = "https://metadata.provider.plex.tv"

// WatchlistItem is one entry of the Plex account's watchlist.
type WatchlistItem struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Year     int      `json:"year"`
	Type     string   `json:"type"` // movie or show
	GUIDs    []string `json:"guids"`
	PlexGUID string   `json:"plexGuid"`
}

// Watchlist returns the watchlist of the account the token belongs to.
// The token must be the account token from the plex.tv sign-in.
func (a *Account) Watchlist(ctx context.Context, token string) ([]WatchlistItem, error) {
	var res struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey string `json:"ratingKey"`
				Title     string `json:"title"`
				Year      int    `json:"year"`
				Type      string `json:"type"`
				GUID      string `json:"guid"`
				IDs       []struct {
					ID string `json:"id"`
				} `json:"Guid"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := a.discover(ctx, a.discoverURL()+"/library/sections/watchlist/all", url.Values{"includeGuids": {"1"}}, token, &res); err != nil {
		return nil, err
	}
	items := make([]WatchlistItem, 0, len(res.MediaContainer.Metadata))
	for _, m := range res.MediaContainer.Metadata {
		it := WatchlistItem{
			Key:      m.RatingKey,
			Title:    m.Title,
			Year:     m.Year,
			Type:     m.Type,
			PlexGUID: m.GUID,
		}
		for _, g := range m.IDs {
			it.GUIDs = append(it.GUIDs, g.ID)
		}
		if !hasTMDB(it.GUIDs) && m.RatingKey != "" {
			ids, err := a.metadataGUIDs(ctx, token, m.RatingKey)
			if err != nil {
				return nil, err
			}
			it.GUIDs = append(it.GUIDs, ids...)
		}
		items = append(items, it)
	}
	return items, nil
}

func (a *Account) discoverURL() string {
	if a.DiscoverURL != "" {
		return strings.TrimRight(a.DiscoverURL, "/")
	}
	return DefaultDiscoverURL
}

func (a *Account) metadataURL() string {
	if a.MetadataURL != "" {
		return strings.TrimRight(a.MetadataURL, "/")
	}
	return DefaultMetadataURL
}

func hasTMDB(guids []string) bool {
	for _, g := range guids {
		if strings.HasPrefix(g, "tmdb://") {
			return true
		}
	}
	return false
}

// metadataGUIDs asks the metadata service for the external IDs of one item.
func (a *Account) metadataGUIDs(ctx context.Context, token, key string) ([]string, error) {
	var res struct {
		MediaContainer struct {
			Metadata []struct {
				IDs []struct {
					ID string `json:"id"`
				} `json:"Guid"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	u := a.metadataURL() + "/library/metadata/" + url.PathEscape(key)
	if err := a.discover(ctx, u, url.Values{"includeGuids": {"1"}}, token, &res); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range res.MediaContainer.Metadata {
		for _, g := range m.IDs {
			ids = append(ids, g.ID)
		}
	}
	return ids, nil
}

// discover calls a provider.plex.tv endpoint with the account token.
func (a *Account) discover(ctx context.Context, rawURL string, q url.Values, token string, dst any) error {
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	setHeaders(req, a.ClientID, token)
	body, _, err := httpx.Do(a.HTTP, req, 8<<20)
	if err != nil {
		return fmt.Errorf("plex.tv: %w", err)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("plex.tv: decode response: %w", err)
	}
	return nil
}
