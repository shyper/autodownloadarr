// Package trakt connects user-owned watchlists and personal lists to automatic requests.
package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/scheduler"
)

const apiURL = "https://api.trakt.tv"
const authURL = "https://auth.trakt.tv"

type Client struct {
	HTTP                      *http.Client
	APIURL, AuthURL, ClientID string
}
type Token struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int64  `json:"expires_in"`
	CreatedAt int64  `json:"created_at"`
}
type Media struct {
	Title string `json:"title"`
	IDs   struct {
		TMDB int `json:"tmdb"`
	} `json:"ids"`
}
type Item struct {
	Type  string `json:"type"`
	Movie Media  `json:"movie"`
	Show  Media  `json:"show"`
}
type List struct {
	Name      string `json:"name"`
	ItemCount int    `json:"item_count"`
	IDs       struct {
		Trakt int64 `json:"trakt"`
	} `json:"ids"`
}
type Account struct {
	User struct {
		Username string `json:"username"`
		IDs      struct {
			UUID string `json:"uuid"`
			Slug string `json:"slug"`
		} `json:"ids"`
	} `json:"user"`
}

func (c *Client) base(oauth bool) string {
	if oauth {
		if c.AuthURL != "" {
			return strings.TrimRight(c.AuthURL, "/")
		}
		return authURL
	}
	if c.APIURL != "" {
		return strings.TrimRight(c.APIURL, "/")
	}
	return apiURL
}
func (c *Client) request(ctx context.Context, method, path, token string, body any, out any, oauth bool) (http.Header, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base(oauth)+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.ClientID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := c.HTTP
	if client == nil {
		client = httpx.NewClient()
	}
	data, headers, err := httpx.Do(client, req, 8<<20)
	if err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) {
			if status.Code == 429 {
				return headers, scheduler.RetryAfter(httpx.ErrRateLimited, max(status.RetryAfter, time.Minute))
			}
			if status.Code == 401 || status.Code == 403 || oauth && status.Code == 400 {
				return headers, httpx.ErrUnauthorized
			}
			return headers, status
		}
		if ctx.Err() != nil {
			return headers, ctx.Err()
		}
		return headers, httpx.ErrUnavailable
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return headers, fmt.Errorf("%w: invalid Trakt response", httpx.ErrUnavailable)
	}
	return headers, nil
}
func (c *Client) Exchange(ctx context.Context, redirect, code, verifier, refresh string) (Token, error) {
	body := map[string]string{"client_id": c.ClientID, "redirect_uri": redirect}
	if refresh != "" {
		body["grant_type"] = "refresh_token"
		body["refresh_token"] = refresh
	} else {
		body["grant_type"] = "authorization_code"
		body["code"] = code
		body["code_verifier"] = verifier
	}
	var token Token
	_, err := c.request(ctx, http.MethodPost, "/oauth/token", "", body, &token, true)
	if err != nil {
		return token, err
	}
	if token.Access == "" || token.Refresh == "" || token.ExpiresIn <= 0 || token.ExpiresIn > 365*86400 {
		return token, fmt.Errorf("%w: incomplete Trakt credentials", httpx.ErrUnavailable)
	}
	if token.CreatedAt <= 0 {
		token.CreatedAt = time.Now().Unix()
	}
	return token, nil
}
func (c *Client) Revoke(ctx context.Context, token string) error {
	_, err := c.request(ctx, http.MethodPost, "/oauth/revoke", "", map[string]string{"client_id": c.ClientID, "token": token}, nil, true)
	return err
}
func (c *Client) Account(ctx context.Context, token string) (Account, error) {
	var a Account
	_, err := c.request(ctx, "GET", "/users/settings", token, nil, &a, false)
	return a, err
}

// pages follows the returned page count, including endpoint-specific page caps.
func pages[T any](ctx context.Context, c *Client, path, token string) ([]T, error) {
	out := []T{}
	for page := 1; page <= 10000; page++ {
		var batch []T
		headers, err := c.request(ctx, "GET", path+"?"+url.Values{"page": {strconv.Itoa(page)}, "limit": {"100"}}.Encode(), token, nil, &batch, false)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		count, err := strconv.Atoi(headers.Get("X-Pagination-Page-Count"))
		if err != nil || page >= count {
			return out, nil
		}
		if len(batch) == 0 {
			return nil, fmt.Errorf("%w: inconsistent Trakt pagination", httpx.ErrUnavailable)
		}
	}
	return nil, fmt.Errorf("%w: too many Trakt pages", httpx.ErrUnavailable)
}
func (c *Client) Lists(ctx context.Context, token string) ([]List, error) {
	return pages[List](ctx, c, "/users/me/lists", token)
}
func (c *Client) Watchlist(ctx context.Context, token string) ([]Item, error) {
	out := []Item{}
	for _, kind := range []string{"movies", "shows"} {
		batch, err := pages[Item](ctx, c, "/sync/watchlist/"+kind+"/added/asc", token)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}
func (c *Client) ListItems(ctx context.Context, token string, id int64) ([]Item, error) {
	return pages[Item](ctx, c, "/users/me/lists/"+strconv.FormatInt(id, 10)+"/items/movie,show", token)
}
