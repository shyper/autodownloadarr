package nntp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// ErrNoServers means every configured server is unusable, e.g. because all
// of them rejected the credentials.
var ErrNoServers = errors.New("nntp: no usable server")

// BodyHandler consumes an article body. The pool calls Reset before every
// attempt (an article may be fetched again from another server), Line for
// each line and Finish after the last one.
type BodyHandler interface {
	Reset()
	Line(line []byte) error
	Finish() error
}

// Pool hands out connections to several servers. Articles are requested from
// the servers in priority order; a server that lacks an article or delivers
// it corrupt is followed by the next one.
type Pool struct {
	log     *slog.Logger
	servers []*server
}

type server struct {
	cfg  Config
	log  *slog.Logger
	slot chan struct{} // one token per allowed connection

	mu       sync.Mutex
	idle     []*Conn
	disabled error // set after an authentication failure
}

// retryDelays are the waits before re-trying a server after a connection
// problem.
var retryDelays = []time.Duration{0, time.Second, 3 * time.Second}

// NewPool creates a pool. Connections are opened lazily.
func NewPool(cfgs []Config, log *slog.Logger) (*Pool, error) {
	if len(cfgs) == 0 {
		return nil, errors.New("nntp: no server configured")
	}
	p := &Pool{log: log}
	for _, cfg := range cfgs {
		if cfg.Address == "" {
			return nil, errors.New("nntp: server address is required")
		}
		if cfg.Connections <= 0 {
			cfg.Connections = 8
		}
		p.servers = append(p.servers, &server{
			cfg:  cfg,
			log:  log.With("server", cfg.label()),
			slot: make(chan struct{}, cfg.Connections),
		})
	}
	slices.SortStableFunc(p.servers, func(a, b *server) int { return a.cfg.Priority - b.cfg.Priority })
	return p, nil
}

// Connections is the number of connections of the primary servers, i.e. how
// many articles can usefully be fetched in parallel.
func (p *Pool) Connections() int {
	n := 0
	for _, s := range p.servers {
		if s.cfg.Priority == p.servers[0].cfg.Priority {
			n += s.cfg.Connections
		}
	}
	return n
}

// Body fetches an article into h. It returns nil on success,
// ErrNoSuchArticle if no server has it, an *ArticleError if it was corrupt
// everywhere, ErrNoServers if no server can be used at all, or the last
// connection error.
func (p *Pool) Body(ctx context.Context, messageID string, h BodyHandler) error {
	var lastErr error
	usable := 0
	for _, s := range p.servers {
		if s.isDisabled() {
			continue
		}
		usable++
		err := s.body(ctx, messageID, h)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// A missing article reported by an earlier server is more telling
		// than a later connection problem.
		if lastErr == nil || !errors.Is(lastErr, ErrNoSuchArticle) {
			lastErr = err
		}
	}
	if usable == 0 {
		return ErrNoServers
	}
	return lastErr
}

// Stat checks whether any server has the article, without downloading it.
// It returns nil, ErrNoSuchArticle or a connection error.
func (p *Pool) Stat(ctx context.Context, messageID string) error {
	lastErr := ErrNoServers
	for _, s := range p.servers {
		if s.isDisabled() {
			continue
		}
		c, err := s.acquire(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		err = c.Stat(ctx, messageID)
		if errors.Is(err, ErrNoSuchArticle) || err == nil {
			s.release(c)
		} else {
			s.discard(c)
		}
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(lastErr, ErrNoServers) || !errors.Is(lastErr, ErrNoSuchArticle) {
			lastErr = err
		}
	}
	return lastErr
}

// Close closes all idle connections.
func (p *Pool) Close() {
	for _, s := range p.servers {
		s.mu.Lock()
		idle := s.idle
		s.idle = nil
		s.mu.Unlock()
		for _, c := range idle {
			_ = c.Close()
		}
	}
}

func (s *server) body(ctx context.Context, messageID string, h BodyHandler) error {
	var err error
	for _, delay := range retryDelays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		var c *Conn
		c, err = s.acquire(ctx)
		if err != nil {
			if errors.Is(err, ErrAuthFailed) {
				s.disable(err)
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Debug("nntp: connect failed, retrying", "err", err)
			continue
		}

		h.Reset()
		err = c.Body(ctx, messageID, h.Line)
		if err == nil {
			if ferr := h.Finish(); ferr != nil {
				err = &ArticleError{MessageID: messageID, Err: ferr}
			}
		}
		var articleErr *ArticleError
		switch {
		case err == nil, errors.Is(err, ErrNoSuchArticle), errors.As(err, &articleErr):
			s.release(c)
			return err
		case errors.Is(err, ErrAuthFailed):
			s.discard(c)
			s.disable(err)
			return err
		default:
			s.discard(c)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Debug("nntp: connection failed, retrying", "err", err)
		}
	}
	return err
}

// acquire returns an idle connection or dials a new one, waiting while all
// connections of the server are busy.
func (s *server) acquire(ctx context.Context) (*Conn, error) {
	select {
	case s.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	if n := len(s.idle); n > 0 {
		c := s.idle[n-1]
		s.idle = s.idle[:n-1]
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()

	c, err := Dial(ctx, s.cfg)
	if err != nil {
		<-s.slot
		return nil, err
	}
	return c, nil
}

func (s *server) release(c *Conn) {
	if c.Broken() {
		s.discard(c)
		return
	}
	s.mu.Lock()
	s.idle = append(s.idle, c)
	s.mu.Unlock()
	<-s.slot
}

func (s *server) discard(c *Conn) {
	_ = c.Close()
	<-s.slot
}

func (s *server) disable(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled == nil {
		s.disabled = err
		s.log.Error("nntp: server disabled", "err", err)
	}
}

func (s *server) isDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled != nil
}

// Usable reports whether at least one server has not been disabled.
func (p *Pool) Usable() bool {
	for _, s := range p.servers {
		if !s.isDisabled() {
			return true
		}
	}
	return false
}

// Errors returns why servers were disabled, for reporting.
func (p *Pool) Errors() []error {
	var errs []error
	for _, s := range p.servers {
		s.mu.Lock()
		if s.disabled != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.cfg.label(), s.disabled))
		}
		s.mu.Unlock()
	}
	return errs
}
