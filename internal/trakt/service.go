package trakt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

const taskSync = "trakt.sync"

type Service struct {
	Store                      *store.Store
	Movies                     *movies.Manager
	Series                     *series.Manager
	Scheduler                  *scheduler.Scheduler
	Log                        *slog.Logger
	HTTP                       *http.Client
	APIURL, AuthURL, PublicURL string
	now                        func() time.Time
	locks                      sync.Map
}

func New(st *store.Store, mv *movies.Manager, se *series.Manager, sc *scheduler.Scheduler, publicURL string, log *slog.Logger) *Service {
	s := &Service{Store: st, Movies: mv, Series: se, Scheduler: sc, PublicURL: strings.TrimRight(publicURL, "/"), Log: log, now: time.Now}
	sc.Handle(taskSync, s.HandleTask)
	sc.Every("trakt-imports", time.Minute, s.Poll)
	return s
}
func (s *Service) lock(id int64) func() {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}
func (s *Service) CallbackURL() string {
	u, err := url.Parse(s.PublicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return s.PublicURL + "/api/v1/trakt/callback"
}
func (s *Service) client(id string) *Client {
	return &Client{HTTP: s.HTTP, APIURL: s.APIURL, AuthURL: s.AuthURL, ClientID: id}
}
func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Service) Begin(ctx context.Context, userID, sessionID int64) (string, error) {
	unlock := s.lock(userID)
	defer unlock()
	id, err := s.Store.TraktClientID(ctx)
	if err != nil {
		return "", err
	}
	if id == "" || s.CallbackURL() == "" {
		return "", movies.ErrNotConfigured
	}
	state, verifier := randomString(), randomString()
	if err := s.Store.CreateTraktState(ctx, userID, sessionID, id, state, verifier); err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {id}, "response_type": {"code"}, "redirect_uri": {s.CallbackURL()}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	return s.client(id).base(true) + "/oauth/authorize?" + q.Encode(), nil
}
func (s *Service) Complete(ctx context.Context, userID, sessionID int64, state, code string) error {
	unlock := s.lock(userID)
	defer unlock()
	id, err := s.Store.TraktClientID(ctx)
	if err != nil {
		return err
	}
	if id == "" || s.CallbackURL() == "" {
		return movies.ErrNotConfigured
	}
	verifier, err := s.Store.ConsumeTraktState(ctx, userID, sessionID, id, state)
	if err != nil {
		return err
	}
	if code == "" || len(code) > 4096 {
		return store.ErrInvalid
	}
	c := s.client(id)
	token, err := c.Exchange(ctx, s.CallbackURL(), code, verifier, "")
	if err != nil {
		return err
	}
	a, err := c.Account(ctx, token.Access)
	if err != nil {
		return err
	}
	accountID := a.User.IDs.UUID
	if accountID == "" {
		accountID = a.User.IDs.Slug
	}
	if accountID == "" || a.User.Username == "" {
		return httpx.ErrUnavailable
	}
	return s.Store.SaveTraktLink(ctx, store.TraktLink{UserID: userID, ClientID: id, AccountID: accountID, Username: a.User.Username, AccessToken: token.Access, RefreshToken: token.Refresh, ExpiresAt: token.CreatedAt + token.ExpiresIn})
}

func (s *Service) authorized(ctx context.Context, userID int64) (*Client, store.TraktLink, error) {
	n, err := s.Store.TraktLink(ctx, userID)
	if err != nil {
		return nil, n, err
	}
	id, err := s.Store.TraktClientID(ctx)
	if err != nil {
		return nil, n, err
	}
	if id == "" || id != n.ClientID || s.CallbackURL() == "" {
		return nil, n, movies.ErrNotConfigured
	}
	if n.AccessToken == "" || n.RefreshToken == "" {
		return nil, n, httpx.ErrUnauthorized
	}
	c := s.client(id)
	if n.ExpiresAt <= s.now().Add(time.Minute).Unix() {
		token, err := c.Exchange(ctx, s.CallbackURL(), "", "", n.RefreshToken)
		if err != nil {
			return nil, n, err
		}
		n.AccessToken, n.RefreshToken, n.ExpiresAt = token.Access, token.Refresh, token.CreatedAt+token.ExpiresIn
		if err := s.Store.SaveTraktLink(ctx, n); err != nil {
			return nil, n, err
		}
	}
	return c, n, nil
}
func (s *Service) Lists(ctx context.Context, userID int64) ([]List, error) {
	unlock := s.lock(userID)
	defer unlock()
	c, n, err := s.authorized(ctx, userID)
	if err != nil {
		return nil, err
	}
	return c.Lists(ctx, n.AccessToken)
}
func (s *Service) SaveSettings(ctx context.Context, userID int64, settings store.TraktSettings) error {
	unlock := s.lock(userID)
	defer unlock()
	if err := settings.Validate(); err != nil {
		return err
	}
	if !settings.Enabled || !settings.Watchlist && len(settings.ListIDs) == 0 {
		if err := s.Store.SetTraktSettings(ctx, userID, settings); err != nil {
			return err
		}
		return s.Scheduler.Cancel(ctx, taskKey(userID))
	}
	c, n, err := s.authorized(ctx, userID)
	if err != nil {
		return err
	}
	if len(settings.ListIDs) > 0 {
		lists, err := c.Lists(ctx, n.AccessToken)
		if err != nil {
			return err
		}
		for _, id := range settings.ListIDs {
			if !slices.ContainsFunc(lists, func(l List) bool { return l.IDs.Trakt == id }) {
				return fmt.Errorf("%w: select one of your own Trakt lists", store.ErrInvalid)
			}
		}
	}
	return s.Store.SetTraktSettings(ctx, userID, settings)
}
func (s *Service) Disconnect(ctx context.Context, userID int64) error {
	unlock := s.lock(userID)
	defer unlock()
	n, err := s.Store.TraktLink(ctx, userID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := s.Scheduler.Cancel(ctx, taskKey(userID)); err != nil {
		return err
	}
	if err := s.Store.DeleteTraktLink(ctx, userID); err != nil {
		return err
	}
	if n.AccessToken != "" {
		revokeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := s.client(n.ClientID).Revoke(revokeCtx, n.AccessToken); err != nil && s.Log != nil {
			s.Log.Warn("Trakt token revocation failed after local disconnection", "user", userID)
		}
	}
	return nil
}
func taskKey(id int64) string { return "trakt:" + strconv.FormatInt(id, 10) + ":sync" }
func (s *Service) Queue(ctx context.Context, userID int64, force bool) error {
	unlock := s.lock(userID)
	defer unlock()
	n, err := s.Store.TraktLink(ctx, userID)
	if err != nil {
		return err
	}
	id, err := s.Store.TraktClientID(ctx)
	if err != nil {
		return err
	}
	if id == "" || id != n.ClientID {
		return nil
	}
	if !n.Enabled || !n.Watchlist && len(n.ListIDs) == 0 {
		return nil
	}
	if !force && n.NextSyncAt > s.now().UnixMilli() {
		return nil
	}
	active, err := s.Store.TraktTaskActive(ctx, taskKey(userID))
	if err != nil || active {
		return err
	}
	_, err = s.Scheduler.Schedule(ctx, scheduler.Spec{Type: taskSync, Key: taskKey(userID), Payload: userID})
	return err
}
func (s *Service) Poll(ctx context.Context) error {
	ids, err := s.Store.TraktUserIDs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := s.Queue(ctx, id, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, store.ErrQuotaExceeded):
		return "quota_exceeded"
	case errors.Is(err, httpx.ErrUnauthorized):
		return "credentials_rejected"
	case errors.Is(err, httpx.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, movies.ErrNotConfigured):
		return "not_configured"
	case errors.Is(err, store.ErrInvalid):
		return "invalid_input"
	case errors.Is(err, httpx.ErrNotFound):
		return "not_found"
	default:
		return "service_unavailable"
	}
}
func (s *Service) HandleTask(ctx context.Context, t scheduler.Task) error {
	var userID int64
	if json.Unmarshal(t.Payload, &userID) != nil || userID <= 0 {
		return scheduler.Permanent(store.ErrInvalid)
	}
	unlock := s.lock(userID)
	defer unlock()
	link, err := s.Store.TraktLink(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !link.Enabled || !link.Watchlist && len(link.ListIDs) == 0 {
		return nil
	}
	c, n, err := s.authorized(ctx, userID)
	if err != nil {
		if saveErr := s.Store.RecordTraktSync(ctx, userID, ErrorCode(err), 0, 0, 1); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		if errors.Is(err, httpx.ErrUnauthorized) || errors.Is(err, movies.ErrNotConfigured) {
			return scheduler.Permanent(err)
		}
		return err
	}
	items := []Item{}
	var failures []error
	if n.Watchlist {
		batch, err := c.Watchlist(ctx, n.AccessToken)
		if err != nil {
			failures = append(failures, err)
		} else {
			items = append(items, batch...)
		}
	}
	for _, id := range n.ListIDs {
		if previous := errors.Join(failures...); errors.Is(previous, httpx.ErrRateLimited) || errors.Is(previous, httpx.ErrUnauthorized) {
			break
		}
		batch, err := c.ListItems(ctx, n.AccessToken, id)
		if err != nil {
			failures = append(failures, err)
		} else {
			items = append(items, batch...)
		}
		if errors.Is(err, httpx.ErrRateLimited) || errors.Is(err, httpx.ErrUnauthorized) {
			break
		}
	}
	requested, skipped := 0, 0
	seen := map[string]bool{}
	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		media := it.Movie
		if it.Type == "show" {
			media = it.Show
		} else if it.Type != "movie" {
			skipped++
			continue
		}
		id := media.IDs.TMDB
		key := it.Type + ":" + strconv.Itoa(id)
		if id <= 0 || seen[key] {
			skipped++
			continue
		}
		seen[key] = true
		var err error
		if it.Type == "movie" {
			_, err = s.Movies.RequestFromList(ctx, id, userID)
		} else {
			_, err = s.Series.RequestFromList(ctx, id, userID)
		}
		switch {
		case err == nil:
			requested++
		case errors.Is(err, movies.ErrAlreadyRequested), errors.Is(err, series.ErrAlreadyRequested), errors.Is(err, movies.ErrWatchlistExcluded):
			skipped++
		default:
			failures = append(failures, err)
		}
	}
	err = errors.Join(failures...)
	if saveErr := s.Store.RecordTraktSync(ctx, userID, ErrorCode(err), requested, skipped, len(failures)); saveErr != nil {
		return saveErr
	}
	return err
}
