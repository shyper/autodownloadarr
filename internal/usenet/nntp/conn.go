// Package nntp is a minimal NNTP client for downloading article bodies, with
// a connection pool spanning several servers.
package nntp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Errors classifying NNTP failures.
var (
	// ErrNoSuchArticle means the server does not have the article (430).
	ErrNoSuchArticle = errors.New("nntp: no such article")
	// ErrAuthFailed means the credentials were rejected. Retrying is pointless.
	ErrAuthFailed = errors.New("nntp: authentication failed")
	// ErrServiceUnavailable is returned when the server refuses the
	// connection, typically because of too many connections (400/502).
	ErrServiceUnavailable = errors.New("nntp: service unavailable")
)

// ProtocolError is an unexpected server response.
type ProtocolError struct {
	Code int
	Line string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("nntp: unexpected response %q", e.Line)
}

// ArticleError means the article was received but rejected by the body
// handler, e.g. a yEnc CRC mismatch. The connection itself is fine.
type ArticleError struct {
	MessageID string
	Err       error
}

func (e *ArticleError) Error() string { return fmt.Sprintf("article %s: %v", e.MessageID, e.Err) }
func (e *ArticleError) Unwrap() error { return e.Err }

// Config describes one Usenet server.
type Config struct {
	Name    string
	Address string // host:port
	TLS     bool
	// InsecureSkipVerify disables TLS certificate checks.
	InsecureSkipVerify bool
	Username           string
	Password           string
	// Connections is the maximum number of parallel connections.
	Connections int
	// Priority orders servers; lower values are asked first. Backup
	// (block) accounts get a higher value.
	Priority int
	// Timeout bounds each command and every 256 lines of a body (default 60s).
	Timeout time.Duration
}

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 60 * time.Second
}

func (c Config) label() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Address
}

// Conn is a single authenticated NNTP connection. It is not safe for
// concurrent use.
type Conn struct {
	nc      net.Conn
	br      *bufio.Reader
	bw      *bufio.Writer
	timeout time.Duration
	broken  bool
}

// Dial connects, reads the greeting and authenticates.
func Dial(ctx context.Context, cfg Config) (*Conn, error) {
	d := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("nntp: connect %s: %w", cfg.label(), err)
	}
	if cfg.TLS {
		host, _, _ := net.SplitHostPort(cfg.Address)
		tc := tls.Client(nc, &tls.Config{
			ServerName:         host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // explicit user setting
		})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = nc.Close()
			return nil, fmt.Errorf("nntp: TLS handshake with %s: %w", cfg.label(), err)
		}
		nc = tc
	}

	c := &Conn{
		nc:      nc,
		br:      bufio.NewReaderSize(nc, 64<<10),
		bw:      bufio.NewWriterSize(nc, 4<<10),
		timeout: cfg.timeout(),
	}
	if err := c.handshake(ctx, cfg); err != nil {
		_ = nc.Close()
		return nil, err
	}
	return c, nil
}

func (c *Conn) handshake(ctx context.Context, cfg Config) error {
	stop := c.watch(ctx)
	defer stop()
	_ = c.nc.SetDeadline(time.Now().Add(c.timeout))

	code, line, err := c.readStatus()
	if err != nil {
		return c.ioError(ctx, "read greeting", err)
	}
	switch code {
	case 200, 201:
	case 400, 502:
		return fmt.Errorf("%w: %s", ErrServiceUnavailable, line)
	default:
		return &ProtocolError{Code: code, Line: line}
	}
	if cfg.Username == "" {
		return nil
	}

	code, line, err = c.command(ctx, "AUTHINFO USER "+cfg.Username)
	if err != nil {
		return err
	}
	if code == 381 {
		code, line, err = c.command(ctx, "AUTHINFO PASS "+cfg.Password)
		if err != nil {
			return err
		}
	}
	switch code {
	case 281:
		return nil
	case 481, 482, 502:
		return fmt.Errorf("%w: %s", ErrAuthFailed, line)
	case 400:
		return fmt.Errorf("%w: %s", ErrServiceUnavailable, line)
	default:
		return &ProtocolError{Code: code, Line: line}
	}
}

// Body requests an article body and passes every line to fn, without the
// line ending and with dot-stuffing removed. If fn fails, the rest of the
// body is still read so the connection stays usable, and an *ArticleError
// is returned.
func (c *Conn) Body(ctx context.Context, messageID string, fn func(line []byte) error) error {
	id, err := formatID(messageID)
	if err != nil {
		return err
	}
	stop := c.watch(ctx)
	defer stop()

	code, line, err := c.command(ctx, "BODY "+id)
	if err != nil {
		return err
	}
	switch code {
	case 222:
	case 420, 423, 430:
		return fmt.Errorf("%w: %s", ErrNoSuchArticle, id)
	case 480:
		return fmt.Errorf("%w: %s", ErrAuthFailed, line)
	case 400, 502:
		c.broken = true
		return fmt.Errorf("%w: %s", ErrServiceUnavailable, line)
	default:
		return &ProtocolError{Code: code, Line: line}
	}

	var handlerErr error
	for n := 0; ; n++ {
		if n%256 == 0 {
			_ = c.nc.SetReadDeadline(time.Now().Add(c.timeout))
		}
		raw, err := c.br.ReadSlice('\n')
		if err != nil {
			return c.ioError(ctx, "read body", err)
		}
		line := bytes.TrimSuffix(raw[:len(raw)-1], []byte{'\r'})
		if len(line) > 0 && line[0] == '.' {
			if len(line) == 1 {
				break // terminator
			}
			line = line[1:] // dot-stuffing
		}
		if handlerErr == nil {
			handlerErr = fn(line)
		}
	}
	if handlerErr != nil {
		return &ArticleError{MessageID: messageID, Err: handlerErr}
	}
	return nil
}

// Stat checks whether the server has an article without downloading it.
func (c *Conn) Stat(ctx context.Context, messageID string) error {
	id, err := formatID(messageID)
	if err != nil {
		return err
	}
	stop := c.watch(ctx)
	defer stop()
	code, line, err := c.command(ctx, "STAT "+id)
	if err != nil {
		return err
	}
	switch code {
	case 223:
		return nil
	case 420, 423, 430:
		return fmt.Errorf("%w: %s", ErrNoSuchArticle, id)
	default:
		return &ProtocolError{Code: code, Line: line}
	}
}

// Close says goodbye to the server and closes the connection.
func (c *Conn) Close() error {
	if !c.broken {
		_ = c.nc.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.bw.WriteString("QUIT\r\n"); err == nil {
			_ = c.bw.Flush()
		}
	}
	return c.nc.Close()
}

// Broken reports whether the connection must not be reused.
func (c *Conn) Broken() bool { return c.broken }

// command sends one line and reads the status response.
func (c *Conn) command(ctx context.Context, cmd string) (int, string, error) {
	_ = c.nc.SetDeadline(time.Now().Add(c.timeout))
	if _, err := c.bw.WriteString(cmd + "\r\n"); err != nil {
		return 0, "", c.ioError(ctx, "send command", err)
	}
	if err := c.bw.Flush(); err != nil {
		return 0, "", c.ioError(ctx, "send command", err)
	}
	code, line, err := c.readStatus()
	if err != nil {
		return 0, "", c.ioError(ctx, "read response", err)
	}
	return code, line, nil
}

func (c *Conn) readStatus() (int, string, error) {
	raw, err := c.br.ReadSlice('\n')
	if err != nil {
		return 0, "", err
	}
	line := strings.TrimRight(string(raw), "\r\n")
	if len(line) < 3 {
		c.broken = true
		return 0, line, &ProtocolError{Line: line}
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		c.broken = true
		return 0, line, &ProtocolError{Line: line}
	}
	return code, line, nil
}

// watch aborts blocked I/O when ctx is cancelled. The returned function
// stops watching.
func (c *Conn) watch(ctx context.Context) func() bool {
	return context.AfterFunc(ctx, func() { _ = c.nc.SetDeadline(time.Unix(1, 0)) })
}

func (c *Conn) ioError(ctx context.Context, what string, err error) error {
	c.broken = true
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("nntp: %s: %w", what, err)
}

func formatID(id string) (string, error) {
	id = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
	if id == "" || strings.ContainsAny(id, " \t\r\n<>") {
		return "", fmt.Errorf("nntp: invalid message-id %q", id)
	}
	return "<" + id + ">", nil
}
