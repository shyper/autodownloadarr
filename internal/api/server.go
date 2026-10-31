// Package api implements the HTTP API (under /api/v1) and serves the embedded
// web UI.
package api

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/bots"
	"github.com/shyper/autodownloadarr/internal/control"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/oauth"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

// Options are the dependencies of the HTTP handler.
type Options struct {
	PublicURL string
	Bots      *bots.Service
	Log       *slog.Logger
	DB        *sql.DB
	Auth      *auth.Service
	Limiter   *auth.Limiter
	Bus       *events.Bus
	// Store, Services and Movies serve the configuration and movie endpoints.
	Store    *store.Store
	Services *movies.Services
	Movies   *movies.Manager
	Series   *series.Manager
	// SetupToken must be presented to create the initial admin account.
	SetupToken string
	// TrustProxy honours X-Forwarded-For / X-Forwarded-Proto.
	TrustProxy bool
	// Static is the built web UI; nil disables it.
	Static  fs.FS
	Version string
	// Done is closed on shutdown and ends long-lived event streams.
	Done <-chan struct{}
	// Heartbeat is the keep-alive interval of event streams (default 25s).
	Heartbeat time.Duration
}

type server struct {
	Options
	plex    plexLogins
	oauth   *oauth.Service
	control *control.Service
}

// New returns the root HTTP handler.
func New(opts Options) http.Handler {
	if opts.Heartbeat == 0 {
		opts.Heartbeat = 25 * time.Second
	}
	s := &server{Options: opts}
	s.oauth = oauth.New(opts.DB, opts.PublicURL)
	s.control = &control.Service{Store: opts.Store, Services: opts.Services, Movies: opts.Movies, Series: opts.Series}

	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "cross_origin_rejected", "cross-origin request rejected")
	}))

	r := chi.NewRouter()
	r.Use(middleware.RequestID, s.logRequests, middleware.Recoverer, securityHeaders)

	r.Get("/.well-known/oauth-protected-resource", s.oauthMetadata)
	r.Get("/.well-known/oauth-protected-resource/mcp", s.oauthMetadata)
	r.Get("/.well-known/oauth-authorization-server", s.oauthMetadata)
	r.With(noStore).Get("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/authorize?"+r.URL.RawQuery, http.StatusSeeOther)
	})
	r.With(noStore).Post("/oauth/token", s.handleOAuthToken)
	r.With(noStore).Post("/oauth/revoke", s.handleOAuthRevoke)
	r.Handle("/mcp", s.mcpHandler())
	r.Post("/bots/telegram/webhook", s.handleTelegramWebhook)
	r.Post("/bots/discord/interactions", s.handleDiscordInteraction)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(noStore, csrf.Handler, s.loadSession)

		r.Get("/health", s.handleHealth)
		r.Get("/system/status", s.handleSystemStatus)
		r.Post("/setup", s.handleSetup)
		r.Post("/auth/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/oauth/authorization", s.handleAuthorization)
			r.Post("/oauth/authorize", s.handleAuthorize)
			r.Get("/oauth/grants", s.handleGrants)
			r.Get("/bots/links", s.handleBotLinks)
			r.Get("/bots/link-code", s.handleBotLinkCode)
			r.Post("/bots/links", s.handleConfirmBotLink)
			r.Put("/bots/links", s.handleUpdateBotLink)
			r.Delete("/bots/links/{id}", s.handleDeleteBotLink)
			r.Post("/oauth/grants/revoke", s.handleRevokeGrant)
			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/password", s.handleChangePassword)
			r.Get("/events", s.handleEvents)
			r.Get("/system/warnings", s.handleWarnings)

			r.Get("/movies", s.handleListMovies)
			r.Get("/discover/trending", s.handleTrending)
			r.Post("/movies", s.handleRequestMovie)
			r.Get("/movies/search", s.handleSearchMovies)
			r.Get("/movies/{id}", s.handleGetMovie)
			r.With(s.requireOwner("movie")).Delete("/movies/{id}", s.handleDeleteMovie)
			r.With(s.requireOwner("movie")).Post("/movies/{id}/search", s.handleMovieSearch)
			r.With(s.requireOwner("movie")).Post("/movies/{id}/keep", s.handleMovieKeep)
			r.With(s.requireOwner("movie")).Post("/movies/{id}/skip", s.handleMovieSkip)
			r.With(s.requireOwner("movie")).Post("/movies/{id}/releases/{releaseId}/download", s.handleMovieDownloadRelease)
			r.Get("/downloads", s.handleDownloads)
			r.Get("/downloads/queue", s.handleQueue)
			r.Get("/downloads/history", s.handleHistory)

			r.Get("/series", s.handleListSeries)
			r.Post("/series", s.handleRequestSeries)
			r.Get("/series/search", s.handleSearchShows)
			r.Get("/series/shows/{tmdbId}", s.handleShowDetails)
			r.Get("/series/downloads", s.handleEpisodeDownloads)
			r.Get("/series/{id}", s.handleGetSeries)
			r.With(s.requireOwner("series")).Put("/series/{id}", s.handleUpdateSeries)
			r.With(s.requireOwner("series")).Delete("/series/{id}", s.handleDeleteSeries)
			r.Get("/episodes/{id}", s.handleGetEpisode)
			r.With(s.requireOwner("episode")).Post("/episodes/{id}/search", s.handleEpisodeSearch)
			r.With(s.requireOwner("episode")).Post("/episodes/{id}/skip", s.handleEpisodeSkip)
			r.With(s.requireOwner("episode")).Post("/episodes/{id}/releases/{releaseId}/download", s.handleEpisodeDownloadRelease)
			r.Get("/profiles", s.handleListProfiles)
		})

		// Configuration is reserved for administrators.
		r.Group(func(r chi.Router) {
			r.Use(s.requireUser, s.requireAdmin)
			r.Get("/settings/general", s.handleGetGeneral)
			r.Put("/settings/general", s.handlePutGeneral)
			r.Get("/settings/tmdb", s.handleGetTMDB)
			r.Put("/settings/tmdb", s.handlePutTMDB)
			r.Get("/notifications", s.handleGetNotification)
			r.Put("/notifications", s.handlePutNotification)
			r.Post("/notifications/test", s.handleTestNotification)
			r.Get("/onboarding", s.handleGetOnboarding)
			r.Put("/onboarding", s.handlePutOnboarding)

			r.Post("/oauth/clients", s.handleCreateOAuthClient)
			r.Get("/bots", s.handleGetBots)
			r.Put("/bots", s.handleSaveBots)
			r.Post("/bots/test", s.handleTestBots)
			r.Post("/bots/sync", s.handleSyncBots)
			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Put("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)
			r.Post("/users/{id}/password", s.handleResetUserPassword)

			r.Get("/usenet-servers", s.handleListUsenet)
			r.Post("/usenet-servers", s.handleSaveUsenet)
			r.Post("/usenet-servers/test", s.handleTestUsenet)
			r.Put("/usenet-servers/{id}", s.handleSaveUsenet)
			r.Delete("/usenet-servers/{id}", s.handleDeleteUsenet)

			r.Get("/indexers", s.handleListIndexers)
			r.Post("/indexers", s.handleSaveIndexer)
			r.Post("/indexers/test", s.handleTestIndexer)
			r.Put("/indexers/{id}", s.handleSaveIndexer)
			r.Delete("/indexers/{id}", s.handleDeleteIndexer)

			r.Get("/plex", s.handleGetPlex)
			r.Put("/plex", s.handlePutPlex)
			r.Delete("/plex", s.handleDeletePlex)
			r.Post("/plex/pin", s.handlePlexPin)
			r.Get("/plex/pin/{id}", s.handlePlexPinStatus)
			r.Post("/plex/connect", s.handlePlexConnect)
			r.Get("/plex/libraries", s.handlePlexLibraries)
			r.Post("/plex/poll", s.handlePlexPoll)

			r.Put("/profiles/{id}", s.handleSaveProfile)
			r.Get("/archive-passwords", s.handleListPasswords)
			r.Post("/archive-passwords", s.handleAddPassword)
			r.Delete("/archive-passwords/{id}", s.handleDeletePassword)
			r.Delete("/blocklist/{id}", s.handleDeleteBlocklist)
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		})
	})

	r.Handle("/*", spaHandler(opts.Static))
	return r
}

// clientIP returns the address used for rate limiting and session records.
// Behind a trusted proxy the right-most X-Forwarded-For entry is the one the
// proxy itself appended.
func (s *server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (s.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}
