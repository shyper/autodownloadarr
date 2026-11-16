package plex

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

// HomeUser is a member of the Plex Home of the account (the owner, other
// accounts invited to the home and managed users).
type HomeUser struct {
	ID         int64  `json:"id"`
	UUID       string `json:"uuid"`
	Title      string `json:"title"`
	Thumb      string `json:"thumb"`
	Admin      bool   `json:"admin"`
	Protected  bool   `json:"protected"`
	Restricted bool   `json:"restricted"`
}

// HomeUsers lists the profiles of the account's Plex Home. An account
// without a home returns only itself.
func (a *Account) HomeUsers(ctx context.Context, token string) ([]HomeUser, error) {
	var res struct {
		Users []HomeUser `json:"users"`
	}
	if err := a.do(ctx, http.MethodGet, "/api/v2/home/users", token, &res); err != nil {
		return nil, err
	}
	return res.Users, nil
}

// SwitchHomeUser returns the plex.tv token of a home profile. pin is the
// profile's PIN and only needed for protected profiles.
func (a *Account) SwitchHomeUser(ctx context.Context, token, uuid, pin string) (string, error) {
	q := url.Values{}
	if pin != "" {
		q.Set("pin", pin)
	}
	u := strings.TrimRight(a.TVURL, "/") + "/api/v2/home/users/" + url.PathEscape(uuid) + "/switch"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return "", err
	}
	setHeaders(req, a.ClientID, token)
	body, _, err := httpx.Do(a.HTTP, req, 1<<20)
	if err != nil {
		return "", fmt.Errorf("plex.tv: %w", err)
	}
	var res struct {
		AuthToken           string `json:"authToken"`
		AuthenticationToken string `json:"authenticationToken"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("plex.tv: decode response: %w", err)
	}
	if res.AuthToken == "" {
		res.AuthToken = res.AuthenticationToken
	}
	if res.AuthToken == "" {
		return "", fmt.Errorf("plex.tv: no token for home user %s", uuid)
	}
	return res.AuthToken, nil
}

// ServerToken returns the access token an account token has for the server
// with the machine identifier id.
func (a *Account) ServerToken(ctx context.Context, token, id string) (string, error) {
	servers, err := a.Servers(ctx, token)
	if err != nil {
		return "", err
	}
	for _, s := range servers {
		if s.ID == id && s.AccessToken != "" {
			return s.AccessToken, nil
		}
	}
	return "", fmt.Errorf("plex.tv: the profile has no access to server %s", id)
}

// ExternalID is the profile ID AutoDownloadarr stores for a home user.
func (u HomeUser) ExternalID() string {
	if u.UUID != "" {
		return u.UUID
	}
	return strconv.FormatInt(u.ID, 10)
}
