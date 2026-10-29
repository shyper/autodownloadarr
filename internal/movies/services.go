package movies

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/shyper/autodownloadarr/internal/disk"
	"github.com/shyper/autodownloadarr/internal/indexer/newznab"
	"github.com/shyper/autodownloadarr/internal/mediaserver"
	"github.com/shyper/autodownloadarr/internal/mediaserver/jellyfin"
	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/mediaserver/plex"
	"github.com/shyper/autodownloadarr/internal/metadata/tmdb"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
)

// ErrNotConfigured means a required service has not been set up yet.
var ErrNotConfigured = errors.New("not configured")

// Services builds clients for the external services from the stored
// configuration. Clients are created per use, so configuration changes
// apply immediately; the NNTP pool is reused while its servers stay the same.
type Services struct {
	Store *store.Store
	Log   *slog.Logger
	// HTTP, TMDBBaseURL and PlexTVURL can be overridden in tests.
	HTTP        *http.Client
	TMDBBaseURL string
	PlexTVURL   string

	mu       sync.Mutex
	pool     *nntp.Pool
	poolKey  string
	caps     map[int64]cachedCaps
	breaker  map[int64]*breakerState
	sched    *scheduler.Scheduler // retries failed Plex scans
	warnings map[string]Warning
}

type breakerState struct {
	failures    int
	pausedUntil time.Time
}

// An indexer failing this many times in a row is skipped for indexerPause.
const (
	indexerMaxFailures = 3
	indexerPause       = 15 * time.Minute
)

// ErrIndexerPaused is returned for indexers skipped after repeated errors.
var ErrIndexerPaused = errors.New("indexer paused after repeated errors")

// IndexerAvailable reports whether an indexer may be queried, or until when
// it is paused.
func (s *Services) IndexerAvailable(id int64) (bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.breaker[id]
	if b == nil || time.Now().After(b.pausedUntil) {
		return true, time.Time{}
	}
	return false, b.pausedUntil
}

// IndexerResult records the outcome of a request to an indexer.
func (s *Services) IndexerResult(id int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.breaker == nil {
		s.breaker = map[int64]*breakerState{}
	}
	b := s.breaker[id]
	if b == nil {
		b = &breakerState{}
		s.breaker[id] = b
	}
	if err == nil {
		b.failures = 0
		return
	}
	b.failures++
	if b.failures >= indexerMaxFailures {
		b.failures = 0
		b.pausedUntil = time.Now().Add(indexerPause)
		if s.Log != nil {
			s.Log.Warn("indexer paused after repeated errors", "indexer", id, "until", b.pausedUntil, "err", err)
		}
	}
}

type cachedCaps struct {
	caps newznab.Caps
	at   time.Time
}

const capsTTL = 6 * time.Hour

func (s *Services) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// TMDB returns a TMDB client.
func (s *Services) TMDB(ctx context.Context) (*tmdb.Client, error) {
	key, err := s.Store.TMDBKey(ctx)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("TMDB API key: %w", ErrNotConfigured)
	}
	g, err := s.Store.General(ctx)
	if err != nil {
		return nil, err
	}
	c := tmdb.New(key, g.Language)
	c.HTTP = s.httpClient()
	if s.TMDBBaseURL != "" {
		c.BaseURL = s.TMDBBaseURL
	}
	return c, nil
}

// IndexerClient pairs an indexer's configuration with its client.
type IndexerClient struct {
	Config store.Indexer
	Client *newznab.Client
}

// Indexers returns the enabled indexers.
func (s *Services) Indexers(ctx context.Context) ([]IndexerClient, error) {
	list, err := s.Store.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	var out []IndexerClient
	for _, ix := range list {
		if !ix.Enabled {
			continue
		}
		c := newznab.New(ix.Name, ix.URL, ix.APIKey)
		c.HTTP = s.httpClient()
		out = append(out, IndexerClient{Config: ix, Client: c})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("indexer: %w", ErrNotConfigured)
	}
	return out, nil
}

// Indexer returns one indexer by ID, enabled or not.
func (s *Services) Indexer(ctx context.Context, id int64) (IndexerClient, error) {
	list, err := s.Store.Indexers(ctx)
	if err != nil {
		return IndexerClient{}, err
	}
	for _, ix := range list {
		if ix.ID == id {
			c := newznab.New(ix.Name, ix.URL, ix.APIKey)
			c.HTTP = s.httpClient()
			return IndexerClient{Config: ix, Client: c}, nil
		}
	}
	return IndexerClient{}, fmt.Errorf("indexer %d: %w", id, store.ErrNotFound)
}

// Caps returns an indexer's capabilities, cached for a few hours.
func (s *Services) Caps(ctx context.Context, ic IndexerClient) (newznab.Caps, error) {
	s.mu.Lock()
	c, ok := s.caps[ic.Config.ID]
	s.mu.Unlock()
	if ok && time.Since(c.at) < capsTTL {
		return c.caps, nil
	}
	caps, err := ic.Client.Caps(ctx)
	if err != nil {
		return newznab.Caps{}, err
	}
	s.mu.Lock()
	if s.caps == nil {
		s.caps = map[int64]cachedCaps{}
	}
	s.caps[ic.Config.ID] = cachedCaps{caps: caps, at: time.Now()}
	s.mu.Unlock()
	return caps, nil
}

// Pool returns the NNTP pool for the enabled Usenet servers.
func (s *Services) Pool(ctx context.Context) (*nntp.Pool, error) {
	servers, err := s.Store.UsenetServers(ctx)
	if err != nil {
		return nil, err
	}
	var cfgs []nntp.Config
	for _, u := range servers {
		if u.Enabled {
			cfgs = append(cfgs, u.NNTPConfig())
		}
	}
	if len(cfgs) == 0 {
		return nil, fmt.Errorf("usenet server: %w", ErrNotConfigured)
	}
	key := fmt.Sprintf("%+v", cfgs)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil && s.poolKey == key && s.pool.Usable() {
		return s.pool, nil
	}
	if s.pool != nil {
		s.pool.Close()
	}
	pool, err := nntp.NewPool(cfgs, s.Log)
	if err != nil {
		return nil, err
	}
	s.pool, s.poolKey = pool, key
	return pool, nil
}

// FullCheck reports whether releases are checked completely before they
// are downloaded: when an enabled Usenet server asks for it.
func (s *Services) FullCheck(ctx context.Context) bool {
	list, err := s.Store.UsenetServers(ctx)
	if err != nil {
		return false
	}
	for _, u := range list {
		if u.Enabled && u.FullCheck {
			return true
		}
	}
	return false
}

// gib is one gigabyte as used by the size limits in the settings.
const gib = 1 << 30

// SpaceBudget returns how many more bytes may be written below path without
// exceeding the library size cap or the free space reserve. Both limits cover
// the temporary library as a whole; free space is measured on the file system
// of path. A budget of 0 means that no further download fits.
func (s *Services) SpaceBudget(g store.General, path string) int64 {
	budget := int64(math.MaxInt64)
	if g.MaxLibraryGB > 0 {
		used := disk.Size(g.MoviesPath) + disk.Size(g.SeriesPath)
		budget = min(budget, int64(g.MaxLibraryGB*float64(gib))-used)
	}
	if g.MinFreeGB > 0 && path != "" {
		free, err := disk.Free(path)
		if err != nil {
			if s.Log != nil {
				s.Log.Debug("free space check failed", "path", path, "err", err)
			}
		} else {
			budget = min(budget, int64(free)-int64(g.MinFreeGB*float64(gib)))
		}
	}
	return max(budget, 0)
}

// FormatSize renders a number of bytes for warnings and notifications.
func FormatSize(n int64) string {
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GB", float64(n)/gib)
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", n/(1<<10))
	}
}

// Target is a configured library of the media server for movies or series.
type Target struct {
	Server  mediaserver.Server
	Library string
	Mapper  pathmap.Mapper
}

// Media returns the movie library, or nil if none is configured.
func (s *Services) Media(ctx context.Context) (*Target, error) {
	return s.mediaTarget(ctx, func(ms store.MediaServer) string { return ms.MovieLibrary })
}

// MediaTV returns the TV library, or nil if none is configured.
func (s *Services) MediaTV(ctx context.Context) (*Target, error) {
	return s.mediaTarget(ctx, func(ms store.MediaServer) string { return ms.TVLibrary })
}

// MediaServer returns a client for the configured media server, whether or
// not a library is selected. It returns nil if none is configured.
func (s *Services) MediaServer(ctx context.Context) (mediaserver.Server, error) {
	ms, found, err := s.Store.MediaServer(ctx)
	if err != nil || !found || ms.Token == "" {
		return nil, err
	}
	return s.serverClient(ctx, ms)
}

func (s *Services) mediaTarget(ctx context.Context, library func(store.MediaServer) string) (*Target, error) {
	ms, found, err := s.Store.MediaServer(ctx)
	if err != nil || !found || !ms.Enabled || library(ms) == "" || ms.Token == "" {
		return nil, err
	}
	c, err := s.serverClient(ctx, ms)
	if err != nil {
		return nil, err
	}
	return &Target{Server: c, Library: library(ms), Mapper: pathmap.New(ms.Mappings)}, nil
}

func (s *Services) serverClient(ctx context.Context, ms store.MediaServer) (mediaserver.Server, error) {
	switch mediaserver.Kind(ms.Kind) {
	case mediaserver.Jellyfin, mediaserver.Emby:
		c := jellyfin.NewClient(ms.URL, ms.Token)
		c.HTTP = s.httpClient()
		return c, nil
	default:
		clientID, err := s.Store.PlexClientID(ctx)
		if err != nil {
			return nil, err
		}
		c := plex.NewClient(ms.URL, ms.Token, clientID)
		c.HTTP = s.httpClient()
		return c, nil
	}
}

// PlexAccount returns a plex.tv client for sign-in and server discovery.
func (s *Services) PlexAccount(ctx context.Context) (*plex.Account, error) {
	clientID, err := s.Store.PlexClientID(ctx)
	if err != nil {
		return nil, err
	}
	a := plex.NewAccount(clientID)
	a.HTTP = s.httpClient()
	if s.PlexTVURL != "" {
		a.TVURL = s.PlexTVURL
	}
	return a, nil
}

// Close releases the NNTP pool.
func (s *Services) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil {
		s.pool.Close()
		s.pool = nil
	}
}
