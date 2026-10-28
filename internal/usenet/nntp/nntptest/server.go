// Package nntptest provides an in-memory NNTP server for tests and
// benchmarks, so that no Usenet account is needed.
package nntptest

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

// Server is a fake NNTP server listening on localhost.
type Server struct {
	ln net.Listener

	mu       sync.Mutex
	username string
	password string
	maxConns int
	delay    time.Duration
	articles map[string][]byte // message-id → dot-stuffed body incl. terminator
	dropOnce map[string]bool   // message-id → cut the connection mid-body once
	active   int

	// Requests counts BODY commands.
	Requests atomic.Int64
}

// New starts a server that is stopped when the test ends.
func New(tb testing.TB) *Server {
	tb.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	s := &Server{ln: ln, articles: make(map[string][]byte), dropOnce: make(map[string]bool)}
	go s.serve()
	tb.Cleanup(func() { _ = ln.Close() })
	return s
}

// Addr is the host:port the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// RequireAuth makes BODY and STAT require these credentials.
func (s *Server) RequireAuth(username, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.username, s.password = username, password
}

// LimitConnections rejects connections beyond n with 502 (0 = unlimited).
func (s *Server) LimitConnections(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxConns = n
}

// SetDelay makes every BODY response wait d, simulating a slow line.
func (s *Server) SetDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// Config returns a client configuration with the server's credentials.
func (s *Server) Config(connections int) nntp.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nntp.Config{
		Name:        "test-" + s.Addr(),
		Address:     s.Addr(),
		Username:    s.username,
		Password:    s.password,
		Connections: connections,
	}
}

// AddArticle stores a raw article body (lines separated by CRLF).
func (s *Server) AddArticle(messageID string, body []byte) {
	var buf bytes.Buffer
	buf.Grow(len(body) + len(body)/64 + 8)
	for line := range bytes.SplitSeq(bytes.TrimSuffix(body, []byte("\r\n")), []byte("\r\n")) {
		if len(line) > 0 && line[0] == '.' {
			buf.WriteByte('.')
		}
		buf.Write(line)
		buf.WriteString("\r\n")
	}
	buf.WriteString(".\r\n")
	s.mu.Lock()
	s.articles[messageID] = buf.Bytes()
	s.mu.Unlock()
}

// AddPart yEnc-encodes p and stores it.
func (s *Server) AddPart(messageID string, p yenc.Part) {
	var buf bytes.Buffer
	if err := yenc.Encode(&buf, p, yenc.DefaultLineLength); err != nil {
		panic(err)
	}
	s.AddArticle(messageID, buf.Bytes())
}

// DropOnce makes the next BODY request for messageID end with a cut
// connection halfway through the body.
func (s *Server) DropOnce(messageID string) {
	s.mu.Lock()
	s.dropOnce[messageID] = true
	s.mu.Unlock()
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	s.mu.Lock()
	full := s.maxConns > 0 && s.active >= s.maxConns
	if !full {
		s.active++
	}
	username, password := s.username, s.password
	s.mu.Unlock()
	if full {
		_, _ = c.Write([]byte("502 too many connections\r\n"))
		return
	}
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()

	r := bufio.NewReader(c)
	w := bufio.NewWriterSize(c, 64<<10)
	reply := func(line string) bool {
		_, _ = w.WriteString(line + "\r\n")
		return w.Flush() == nil
	}
	if !reply("200 nntptest ready") {
		return
	}
	authed := username == ""
	var user string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd, arg, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		switch strings.ToUpper(cmd) {
		case "AUTHINFO":
			kind, value, _ := strings.Cut(arg, " ")
			switch strings.ToUpper(kind) {
			case "USER":
				user = value
				reply("381 password required")
			case "PASS":
				if user == username && value == password {
					authed = true
					reply("281 welcome")
				} else {
					reply("481 authentication failed")
				}
			default:
				reply("501 syntax error")
			}
		case "BODY", "STAT":
			if !authed {
				reply("480 authentication required")
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(arg, "<"), ">")
			s.mu.Lock()
			body, ok := s.articles[id]
			drop := s.dropOnce[id]
			delete(s.dropOnce, id)
			delay := s.delay
			s.mu.Unlock()
			if !ok {
				reply("430 no such article")
				continue
			}
			if strings.ToUpper(cmd) == "STAT" {
				reply("223 0 " + arg)
				continue
			}
			s.Requests.Add(1)
			time.Sleep(delay)
			_, _ = w.WriteString("222 0 " + arg + "\r\n")
			if drop {
				_, _ = w.Write(body[:len(body)/2])
				_ = w.Flush()
				return
			}
			_, _ = w.Write(body)
			if w.Flush() != nil {
				return
			}
		case "QUIT":
			reply("205 bye")
			return
		default:
			reply("500 unknown command")
		}
	}
}
