package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/shyper/autodownloadarr/internal/store"
)

func (s *server) handleTraktConfig(w http.ResponseWriter, r *http.Request) {
	id, err := s.Store.TraktClientID(r.Context())
	if err != nil {
		s.writeStoreError(w, "read Trakt app", err)
		return
	}
	writeJSON(w, 200, map[string]any{"clientId": id, "redirectUri": s.Trakt.CallbackURL()})
}
func (s *server) handleSaveTraktConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"clientId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Store.SetTraktClientID(r.Context(), req.ClientID); err != nil {
		s.writeStoreError(w, "save Trakt app", err)
		return
	}
	s.handleTraktConfig(w, r)
}
func (s *server) handleTraktStatus(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentUser(r.Context())
	id, err := s.Store.TraktClientID(r.Context())
	if err != nil {
		s.writeStoreError(w, "read Trakt app", err)
		return
	}
	n, err := s.Store.TraktLink(r.Context(), user.ID)
	linked := err == nil
	if errors.Is(err, store.ErrNotFound) {
		n = store.TraktLink{TraktSettings: store.TraktSettings{Enabled: true, PollMinutes: 30, ListIDs: []int64{}}}
	} else if err != nil {
		s.writeStoreError(w, "read Trakt link", err)
		return
	}
	active, err := s.Store.TraktTaskActive(r.Context(), "trakt:"+strconv.FormatInt(user.ID, 10)+":sync")
	if err != nil {
		s.writeStoreError(w, "read Trakt task", err)
		return
	}
	configured := id != "" && s.Trakt.CallbackURL() != ""
	writeJSON(w, 200, struct {
		store.TraktLink
		Configured bool `json:"configured"`
		Connected  bool `json:"connected"`
		Linked     bool `json:"linked"`
		Syncing    bool `json:"syncing"`
	}{n, configured, linked && configured && n.ClientID == id && n.AccessToken != "" && n.RefreshToken != "" && n.LastError != "credentials_rejected", linked, active})
}
func (s *server) handleTraktConnect(w http.ResponseWriter, r *http.Request) {
	user, session, _ := currentUser(r.Context())
	target, err := s.Trakt.Begin(r.Context(), user.ID, session.ID)
	if err != nil {
		s.writeStoreError(w, "connect Trakt", err)
		return
	}
	writeJSON(w, 200, map[string]string{"url": target})
}
func (s *server) handleTraktCallback(w http.ResponseWriter, r *http.Request) {
	user, session, _ := currentUser(r.Context())
	result := "connected"
	if err := s.Trakt.Complete(r.Context(), user.ID, session.ID, r.URL.Query().Get("state"), r.URL.Query().Get("code")); err != nil {
		result = "failed"
	}
	http.Redirect(w, r, "/account?trakt="+result, http.StatusSeeOther)
}
func (s *server) handleTraktLists(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentUser(r.Context())
	lists, err := s.Trakt.Lists(r.Context(), user.ID)
	if err != nil {
		s.writeStoreError(w, "read Trakt lists", err)
		return
	}
	writeJSON(w, 200, lists)
}
func (s *server) handleSaveTrakt(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentUser(r.Context())
	var settings store.TraktSettings
	if !decodeJSON(w, r, &settings) {
		return
	}
	if err := s.Trakt.SaveSettings(r.Context(), user.ID, settings); err != nil {
		s.writeStoreError(w, "save Trakt settings", err)
		return
	}
	if err := s.Trakt.Queue(r.Context(), user.ID, false); err != nil {
		s.writeStoreError(w, "schedule Trakt import", err)
		return
	}
	s.handleTraktStatus(w, r)
}
func (s *server) handleSyncTrakt(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentUser(r.Context())
	if err := s.Trakt.Queue(r.Context(), user.ID, true); err != nil {
		s.writeStoreError(w, "schedule Trakt import", err)
		return
	}
	s.handleTraktStatus(w, r)
}
func (s *server) handleDisconnectTrakt(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentUser(r.Context())
	if err := s.Trakt.Disconnect(r.Context(), user.ID); err != nil {
		s.writeStoreError(w, "disconnect Trakt", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
