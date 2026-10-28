package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/indexer/newznab"
	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/mediaserver/plex"
	"github.com/shyper/autodownloadarr/internal/metadata/tmdb"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
)

// writeStoreError maps errors of the store and of external services to
// API errors.
func (s *server) writeStoreError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, movies.ErrNotConfigured):
		writeError(w, http.StatusConflict, "not_configured", err.Error())
	case errors.Is(err, httpx.ErrUnauthorized), errors.Is(err, nntp.ErrAuthFailed):
		writeError(w, http.StatusUnprocessableEntity, "credentials_rejected", err.Error())
	case errors.Is(err, httpx.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
	case errors.Is(err, httpx.ErrUnavailable), errors.Is(err, nntp.ErrServiceUnavailable):
		writeError(w, http.StatusBadGateway, "service_unavailable", err.Error())
	default:
		s.internalError(w, what, err)
	}
}

func idParam(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return id, err == nil && id > 0
}

// --- general settings -------------------------------------------------------

func (s *server) handleGetGeneral(w http.ResponseWriter, r *http.Request) {
	g, err := s.Store.General(r.Context())
	if err != nil {
		s.internalError(w, "read settings", err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *server) handlePutGeneral(w http.ResponseWriter, r *http.Request) {
	var g store.General
	if !decodeJSON(w, r, &g) {
		return
	}
	for _, p := range []*string{&g.MoviesPath, &g.SeriesPath} {
		if *p == "" {
			continue
		}
		*p = filepath.Clean(*p)
		if err := checkWritableDir(*p); err != nil {
			writeError(w, http.StatusBadRequest, "path_not_writable", err.Error())
			return
		}
	}
	if g.SeriesWindow < 1 || (g.SeriesTrigger != "progress" && g.SeriesTrigger != "minutes" && g.SeriesTrigger != "watched") {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid series defaults")
		return
	}
	if g.DeleteAfterHours < 0 || g.UnwatchedExpiryDays < 0 || g.MaxSources < 1 || g.ParallelDownloads < 1 || g.WatchPollMinutes < 1 || g.MaxDownloadMBps < 0 {
		writeError(w, http.StatusBadRequest, "invalid_input", "values out of range")
		return
	}
	if err := s.Store.SetGeneral(r.Context(), g); err != nil {
		s.internalError(w, "save settings", err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// checkWritableDir creates dir if needed and verifies that files can be
// written there.
func checkWritableDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%s is not an absolute path", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// --- TMDB -------------------------------------------------------------------

type tmdbRequest struct {
	Key string `json:"key"`
}

func (s *server) handleGetTMDB(w http.ResponseWriter, r *http.Request) {
	key, err := s.Store.TMDBKey(r.Context())
	if err != nil {
		s.internalError(w, "read TMDB key", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"hasKey": key != ""})
}

// handlePutTMDB tests and stores the key.
func (s *server) handlePutTMDB(w http.ResponseWriter, r *http.Request) {
	var req tmdbRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	c := tmdb.New(req.Key, "en-US")
	if s.Services.HTTP != nil {
		c.HTTP = s.Services.HTTP
	}
	if s.Services.TMDBBaseURL != "" {
		c.BaseURL = s.Services.TMDBBaseURL
	}
	if err := c.Test(r.Context()); err != nil {
		s.writeStoreError(w, "test TMDB", err)
		return
	}
	if err := s.Store.SetTMDBKey(r.Context(), req.Key); err != nil {
		s.internalError(w, "save TMDB key", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"hasKey": true})
}

// --- Usenet servers ---------------------------------------------------------

func (s *server) handleListUsenet(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.UsenetServers(r.Context())
	if err != nil {
		s.internalError(w, "list usenet servers", err)
		return
	}
	for i := range list {
		list[i].Password = ""
	}
	writeJSON(w, http.StatusOK, nonNil(list))
}

func (s *server) handleSaveUsenet(w http.ResponseWriter, r *http.Request) {
	var u store.UsenetServer
	if !decodeJSON(w, r, &u) {
		return
	}
	if id, ok := idParam(r, "id"); ok {
		u.ID = id
	}
	id, err := s.Store.SaveUsenetServer(r.Context(), u)
	if err != nil {
		s.writeStoreError(w, "save usenet server", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *server) handleDeleteUsenet(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	if err := s.Store.DeleteUsenetServer(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete usenet server", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestUsenet connects and logs in. An empty password with an ID uses
// the stored password.
func (s *server) handleTestUsenet(w http.ResponseWriter, r *http.Request) {
	var u store.UsenetServer
	if !decodeJSON(w, r, &u) {
		return
	}
	if u.Password == "" && u.ID > 0 {
		list, err := s.Store.UsenetServers(r.Context())
		if err != nil {
			s.internalError(w, "read usenet servers", err)
			return
		}
		for _, existing := range list {
			if existing.ID == u.ID {
				u.Password = existing.Password
			}
		}
	}
	if err := u.Validate(); err != nil {
		s.writeStoreError(w, "test usenet server", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := nntp.Dial(ctx, u.NNTPConfig())
	if err != nil {
		code := "connection_failed"
		if errors.Is(err, nntp.ErrAuthFailed) {
			code = "credentials_rejected"
		}
		writeError(w, http.StatusUnprocessableEntity, code, err.Error())
		return
	}
	_ = conn.Close()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- indexers ---------------------------------------------------------------

func (s *server) handleListIndexers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Indexers(r.Context())
	if err != nil {
		s.internalError(w, "list indexers", err)
		return
	}
	for i := range list {
		list[i].APIKey = ""
	}
	writeJSON(w, http.StatusOK, nonNil(list))
}

func (s *server) handleSaveIndexer(w http.ResponseWriter, r *http.Request) {
	var ix store.Indexer
	if !decodeJSON(w, r, &ix) {
		return
	}
	if id, ok := idParam(r, "id"); ok {
		ix.ID = id
	}
	id, err := s.Store.SaveIndexer(r.Context(), ix)
	if err != nil {
		s.writeStoreError(w, "save indexer", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *server) handleDeleteIndexer(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	if err := s.Store.DeleteIndexer(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete indexer", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestIndexer fetches the capabilities and returns them, including
// the categories for the selection in the UI.
func (s *server) handleTestIndexer(w http.ResponseWriter, r *http.Request) {
	var ix store.Indexer
	if !decodeJSON(w, r, &ix) {
		return
	}
	if ix.APIKey == "" && ix.ID > 0 {
		if existing, err := s.Services.Indexer(r.Context(), ix.ID); err == nil {
			ix.APIKey = existing.Config.APIKey
		}
	}
	if err := ix.Validate(); err != nil {
		s.writeStoreError(w, "test indexer", err)
		return
	}
	c := newznab.New(ix.Name, ix.URL, ix.APIKey)
	if s.Services.HTTP != nil {
		c.HTTP = s.Services.HTTP
	}
	caps, err := c.Caps(r.Context())
	if err != nil {
		s.writeStoreError(w, "test indexer", err)
		return
	}
	writeJSON(w, http.StatusOK, caps)
}

// --- Plex -------------------------------------------------------------------

// plexLogins keeps the servers found after a plex.tv sign-in until the
// user picks one. The account token itself is not stored.
type plexLogins struct {
	mu      sync.Mutex
	servers map[int][]plex.Server // pin id → servers
}

func (s *server) handleGetPlex(w http.ResponseWriter, r *http.Request) {
	ms, found, err := s.Store.MediaServer(r.Context())
	if err != nil {
		s.internalError(w, "read plex config", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	ms.Token = ""
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "server": ms})
}

func (s *server) handlePutPlex(w http.ResponseWriter, r *http.Request) {
	var ms store.MediaServer
	if !decodeJSON(w, r, &ms) {
		return
	}
	if _, err := s.Store.SaveMediaServer(r.Context(), ms); err != nil {
		s.writeStoreError(w, "save plex config", err)
		return
	}
	s.handleGetPlex(w, r)
}

func (s *server) handleDeletePlex(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteMediaServer(r.Context()); err != nil {
		s.internalError(w, "delete plex config", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handlePlexPin(w http.ResponseWriter, r *http.Request) {
	acc, err := s.Services.PlexAccount(r.Context())
	if err != nil {
		s.internalError(w, "plex account", err)
		return
	}
	pin, err := acc.CreatePin(r.Context())
	if err != nil {
		s.writeStoreError(w, "create plex pin", err)
		return
	}
	writeJSON(w, http.StatusOK, pin)
}

// handlePlexPinStatus reports whether the sign-in was approved and, if so,
// lists the account's servers.
func (s *server) handlePlexPinStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid pin id")
		return
	}
	acc, err := s.Services.PlexAccount(r.Context())
	if err != nil {
		s.internalError(w, "plex account", err)
		return
	}
	token, err := acc.CheckPin(r.Context(), int(id))
	if err != nil {
		s.writeStoreError(w, "check plex pin", err)
		return
	}
	if token == "" {
		writeJSON(w, http.StatusOK, map[string]any{"authorized": false})
		return
	}
	servers, err := acc.Servers(r.Context(), token)
	if err != nil {
		s.writeStoreError(w, "list plex servers", err)
		return
	}
	s.plex.mu.Lock()
	if s.plex.servers == nil {
		s.plex.servers = map[int][]plex.Server{}
	}
	s.plex.servers[int(id)] = servers
	s.plex.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"authorized": true, "servers": nonNil(servers)})
}

type plexConnectRequest struct {
	PinID    int    `json:"pinId"`
	ServerID string `json:"serverId"`
	URI      string `json:"uri"`
}

// handlePlexConnect saves the chosen server after testing the connection.
func (s *server) handlePlexConnect(w http.ResponseWriter, r *http.Request) {
	var req plexConnectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.plex.mu.Lock()
	servers := s.plex.servers[req.PinID]
	s.plex.mu.Unlock()
	var chosen *plex.Server
	for i := range servers {
		if servers[i].ID == req.ServerID {
			chosen = &servers[i]
		}
	}
	if chosen == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "unknown server; sign in again")
		return
	}
	clientID, err := s.Store.PlexClientID(r.Context())
	if err != nil {
		s.internalError(w, "plex client id", err)
		return
	}
	c := plex.NewClient(req.URI, chosen.AccessToken, clientID)
	if s.Services.HTTP != nil {
		c.HTTP = s.Services.HTTP
	}
	if _, _, err := c.Identity(r.Context()); err != nil {
		s.writeStoreError(w, "test plex connection", err)
		return
	}
	existing, _, err := s.Store.MediaServer(r.Context())
	if err != nil {
		s.internalError(w, "read plex config", err)
		return
	}
	ms := store.MediaServer{Name: chosen.Name, URL: req.URI, Token: chosen.AccessToken, MovieLibrary: existing.MovieLibrary,
		TVLibrary: existing.TVLibrary, Mappings: existing.Mappings, Enabled: true}
	if _, err := s.Store.SaveMediaServer(r.Context(), ms); err != nil {
		s.writeStoreError(w, "save plex config", err)
		return
	}
	s.plex.mu.Lock()
	delete(s.plex.servers, req.PinID)
	s.plex.mu.Unlock()
	s.handleGetPlex(w, r)
}

// handlePlexLibraries lists the libraries and suggests path mappings for
// the movies folder.
func (s *server) handlePlexLibraries(w http.ResponseWriter, r *http.Request) {
	ms, found, err := s.Store.MediaServer(r.Context())
	if err != nil {
		s.internalError(w, "read plex config", err)
		return
	}
	if !found {
		writeError(w, http.StatusConflict, "not_configured", "no Plex server configured")
		return
	}
	clientID, err := s.Store.PlexClientID(r.Context())
	if err != nil {
		s.internalError(w, "plex client id", err)
		return
	}
	c := plex.NewClient(ms.URL, ms.Token, clientID)
	if s.Services.HTTP != nil {
		c.HTTP = s.Services.HTTP
	}
	libs, err := c.Libraries(r.Context())
	if err != nil {
		s.writeStoreError(w, "list plex libraries", err)
		return
	}
	g, err := s.Store.General(r.Context())
	if err != nil {
		s.internalError(w, "read settings", err)
		return
	}
	var movieLocations, showLocations []string
	for _, l := range libs {
		switch l.Type {
		case "movie":
			movieLocations = append(movieLocations, l.Locations...)
		case "show":
			showLocations = append(showLocations, l.Locations...)
		}
	}
	var suggestions []pathmap.Mapping
	if g.MoviesPath != "" {
		suggestions = append(suggestions, pathmap.Suggest([]string{g.MoviesPath}, movieLocations)...)
	}
	if g.SeriesPath != "" {
		suggestions = append(suggestions, pathmap.Suggest([]string{g.SeriesPath}, showLocations)...)
	}
	// Where the folders end up on the Plex side with the current mappings,
	// and whether that is inside a library of the right type.
	mapper := pathmap.New(ms.Mappings)
	preview := func(local string, locations []string) map[string]any {
		if local == "" {
			return nil
		}
		remote := mapper.ToRemote(local)
		return map[string]any{"local": local, "remote": remote, "inLibrary": pathmap.Contains(locations, remote)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"libraries":   nonNil(libs),
		"suggestions": nonNil(suggestions),
		"movies":      preview(g.MoviesPath, movieLocations),
		"series":      preview(g.SeriesPath, showLocations),
	})
}

// --- quality profiles and passwords ------------------------------------------

func (s *server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Profiles(r.Context())
	if err != nil {
		s.internalError(w, "list profiles", err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(list))
}

func (s *server) handleSaveProfile(w http.ResponseWriter, r *http.Request) {
	var p store.Profile
	if !decodeJSON(w, r, &p) {
		return
	}
	p.ID, _ = idParam(r, "id")
	if err := s.Store.SaveProfile(r.Context(), p); err != nil {
		s.writeStoreError(w, "save profile", err)
		return
	}
	saved, err := s.Store.Profile(r.Context(), p.ID)
	if err != nil {
		s.writeStoreError(w, "read profile", err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *server) handleListPasswords(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ArchivePasswords(r.Context())
	if err != nil {
		s.internalError(w, "list passwords", err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(list))
}

func (s *server) handleAddPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.Store.AddArchivePassword(r.Context(), req.Password)
	if err != nil {
		s.writeStoreError(w, "add password", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *server) handleDeletePassword(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	if err := s.Store.DeleteArchivePassword(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete password", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- setup wizard progress ----------------------------------------------------

func (s *server) handleGetOnboarding(w http.ResponseWriter, r *http.Request) {
	o, err := s.Store.Onboarding(r.Context())
	if err != nil {
		s.internalError(w, "read onboarding", err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *server) handlePutOnboarding(w http.ResponseWriter, r *http.Request) {
	var o store.Onboarding
	if !decodeJSON(w, r, &o) {
		return
	}
	if o.Done == nil {
		o.Done = []string{}
	}
	if err := s.Store.SetOnboarding(r.Context(), o); err != nil {
		s.internalError(w, "save onboarding", err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
