package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/mediaserver"
	"github.com/shyper/autodownloadarr/internal/mediaserver/jellyfin"
	"github.com/shyper/autodownloadarr/internal/mediaserver/plex"
	"github.com/shyper/autodownloadarr/internal/store"
)

// --- media-server profiles ---------------------------------------------------
//
// Profiles are the Plex Home members or Jellyfin/Emby users of the media
// server. An admin assigns them to users; see store.MediaProfile.

func (s *server) handleListMediaProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.MediaProfiles(r.Context())
	if err != nil {
		s.internalError(w, "list media profiles", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleMyMediaProfile returns the profile assigned to the signed-in user.
func (s *server) handleMyMediaProfile(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentUser(r.Context())
	p, err := s.Store.MediaProfileForUser(r.Context(), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"profile": nil})
		return
	}
	if err != nil {
		s.internalError(w, "read media profile", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": map[string]any{"name": p.Name, "thumb": p.Thumb, "watchlist": p.Watchlist}})
}

// handleRefreshMediaProfiles reads the profiles from the media server.
func (s *server) handleRefreshMediaProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ms, found, err := s.Store.MediaServer(ctx)
	if err != nil {
		s.internalError(w, "read media server", err)
		return
	}
	if !found || ms.Token == "" {
		writeError(w, http.StatusConflict, "not_configured", "no media server configured")
		return
	}
	var infos []store.MediaProfileInfo
	switch mediaserver.Kind(ms.Kind) {
	case mediaserver.Jellyfin, mediaserver.Emby:
		infos, err = s.jellyfinProfiles(ctx, ms)
	default:
		if ms.AccountToken == "" {
			writeError(w, http.StatusConflict, "not_configured", "sign in with Plex to read the Plex Home profiles")
			return
		}
		infos, err = s.plexProfiles(ctx, ms)
	}
	if err != nil {
		s.writeStoreError(w, "read media profiles", err)
		return
	}
	if err := s.Store.SyncMediaProfiles(ctx, infos); err != nil {
		s.writeStoreError(w, "save media profiles", err)
		return
	}
	if mediaserver.Kind(ms.Kind) == mediaserver.Plex {
		// Renew the tokens of profiles in use; protected ones need the PIN
		// and keep theirs.
		list, err := s.Store.MediaProfiles(ctx)
		if err != nil {
			s.internalError(w, "list media profiles", err)
			return
		}
		for _, p := range list {
			if p.Owner || p.Protected || p.UserID == 0 {
				continue
			}
			if err := s.switchPlexProfile(ctx, ms, p, ""); err != nil {
				s.Log.Warn("renew Plex profile token failed", "profile", p.Name, "err", err)
				if serr := s.Store.SetMediaProfileError(ctx, p.ID, err.Error()); serr != nil {
					s.internalError(w, "save media profile", serr)
					return
				}
			}
		}
	}
	s.handleListMediaProfiles(w, r)
}

func (s *server) plexProfiles(ctx context.Context, ms store.MediaServer) ([]store.MediaProfileInfo, error) {
	acc, err := s.Services.PlexAccount(ctx)
	if err != nil {
		return nil, err
	}
	users, err := acc.HomeUsers(ctx, ms.AccountToken)
	if err != nil {
		return nil, err
	}
	infos := make([]store.MediaProfileInfo, 0, len(users))
	for _, u := range users {
		infos = append(infos, store.MediaProfileInfo{ExternalID: u.ExternalID(), Name: u.Title, Thumb: u.Thumb,
			Owner: u.Admin, Protected: u.Protected})
	}
	return infos, nil
}

func (s *server) jellyfinProfiles(ctx context.Context, ms store.MediaServer) ([]store.MediaProfileInfo, error) {
	c := jellyfin.NewClient(ms.URL, ms.Token)
	if s.Services.HTTP != nil {
		c.HTTP = s.Services.HTTP
	}
	users, err := c.Users(ctx)
	if err != nil {
		return nil, err
	}
	// The default watch state is the first administrator's (see the
	// client), so that user is the owner.
	owner := ""
	for _, u := range users {
		if u.Admin && !u.Disabled {
			owner = u.ID
			break
		}
	}
	if owner == "" && len(users) > 0 {
		owner = users[0].ID
	}
	infos := make([]store.MediaProfileInfo, 0, len(users))
	for _, u := range users {
		if u.Disabled {
			continue
		}
		infos = append(infos, store.MediaProfileInfo{ExternalID: u.ID, Name: u.Name, Owner: u.ID == owner})
	}
	return infos, nil
}

// switchPlexProfile fetches and stores the tokens of a Plex Home profile.
// The PIN is only used for this request and never stored.
func (s *server) switchPlexProfile(ctx context.Context, ms store.MediaServer, p store.MediaProfile, pin string) error {
	acc, err := s.Services.PlexAccount(ctx)
	if err != nil {
		return err
	}
	clientID, err := s.Store.PlexClientID(ctx)
	if err != nil {
		return err
	}
	c := plex.NewClient(ms.URL, ms.Token, clientID)
	if s.Services.HTTP != nil {
		c.HTTP = s.Services.HTTP
	}
	machineID, _, err := c.Identity(ctx)
	if err != nil {
		return err
	}
	accountToken, err := acc.SwitchHomeUser(ctx, ms.AccountToken, p.ExternalID, pin)
	if err != nil {
		return err
	}
	serverToken, err := acc.ServerToken(ctx, accountToken, machineID)
	if err != nil {
		return err
	}
	return s.Store.SetMediaProfileTokens(ctx, p.ID, serverToken, accountToken)
}

type mediaProfileRequest struct {
	UserID    int64  `json:"userId"` // 0 removes the assignment
	Watchlist bool   `json:"watchlist"`
	PIN       string `json:"pin"`
}

// handleUpdateMediaProfile assigns a profile to a user. A Plex profile gets
// its tokens on the first assignment (with the PIN if it is protected).
func (s *server) handleUpdateMediaProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid profile id")
		return
	}
	var req mediaProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.UserID < 0 || len(req.PIN) > 16 {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid user or PIN")
		return
	}
	p, err := s.Store.MediaProfile(ctx, id)
	if err != nil {
		s.writeStoreError(w, "read media profile", err)
		return
	}
	if req.UserID > 0 {
		if _, err := s.Auth.User(ctx, req.UserID); errors.Is(err, auth.ErrUserNotFound) {
			writeError(w, http.StatusBadRequest, "invalid_input", "unknown user")
			return
		} else if err != nil {
			s.internalError(w, "read user", err)
			return
		}
	}
	ms, found, err := s.Store.MediaServer(ctx)
	if err != nil {
		s.internalError(w, "read media server", err)
		return
	}
	plexProfile := found && mediaserver.Kind(ms.Kind) == mediaserver.Plex && !p.Owner
	if plexProfile && req.UserID > 0 && (!p.HasToken || req.PIN != "") {
		if p.Protected && req.PIN == "" {
			writeError(w, http.StatusUnprocessableEntity, "pin_required", fmt.Sprintf("profile %q is protected by a PIN", p.Name))
			return
		}
		if err := s.switchPlexProfile(ctx, ms, p, req.PIN); err != nil {
			s.writeStoreError(w, "switch Plex profile", err)
			return
		}
	}
	watchlist := req.Watchlist && req.UserID > 0 && found && mediaserver.Kind(ms.Kind) == mediaserver.Plex
	if err := s.Store.AssignMediaProfile(ctx, id, req.UserID, watchlist); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "profile_taken", err.Error())
			return
		}
		s.writeStoreError(w, "save media profile", err)
		return
	}
	saved, err := s.Store.MediaProfile(ctx, id)
	if err != nil {
		s.writeStoreError(w, "read media profile", err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}
