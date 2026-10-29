package nntp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func collect(c *nntp.Conn, id string) ([]string, error) {
	var lines []string
	err := c.Body(context.Background(), id, func(l []byte) error {
		lines = append(lines, string(l))
		return nil
	})
	return lines, err
}

func TestConnBodyAndDotStuffing(t *testing.T) {
	srv := nntptest.New(t)
	srv.AddArticle("a@test", []byte(".starts with dot\r\nnormal\r\n..two dots\r\n"))

	c, err := nntp.Dial(context.Background(), srv.Config(1))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	lines, err := collect(c, "<a@test>")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".starts with dot", "normal", "..two dots"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}

	if _, err := collect(c, "missing@test"); !errors.Is(err, nntp.ErrNoSuchArticle) {
		t.Errorf("missing article: %v", err)
	}
	if err := c.Stat(context.Background(), "a@test"); err != nil {
		t.Errorf("Stat: %v", err)
	}
	if _, err := collect(c, "bad id"); err == nil {
		t.Error("message-id with space accepted")
	}
}

func TestHandlerErrorKeepsConnectionUsable(t *testing.T) {
	srv := nntptest.New(t)
	srv.AddArticle("a@test", []byte("one\r\ntwo\r\nthree\r\n"))
	srv.AddArticle("b@test", []byte("next\r\n"))
	c, err := nntp.Dial(context.Background(), srv.Config(1))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	boom := errors.New("boom")
	err = c.Body(context.Background(), "a@test", func([]byte) error { return boom })
	var ae *nntp.ArticleError
	if !errors.As(err, &ae) || !errors.Is(err, boom) {
		t.Fatalf("got %v, want ArticleError wrapping boom", err)
	}
	if lines, err := collect(c, "b@test"); err != nil || len(lines) != 1 || lines[0] != "next" {
		t.Errorf("connection out of sync: %q %v", lines, err)
	}
}

func TestAuthentication(t *testing.T) {
	srv := nntptest.New(t)
	srv.RequireAuth("user", "secret")

	if _, err := nntp.Dial(context.Background(), srv.Config(1)); err != nil {
		t.Errorf("valid credentials: %v", err)
	}
	cfg := srv.Config(1)
	cfg.Password = "wrong"
	if _, err := nntp.Dial(context.Background(), cfg); !errors.Is(err, nntp.ErrAuthFailed) {
		t.Errorf("wrong password: %v", err)
	}
}

func TestTooManyConnections(t *testing.T) {
	srv := nntptest.New(t)
	srv.LimitConnections(1)
	first, err := nntp.Dial(context.Background(), srv.Config(2))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := nntp.Dial(context.Background(), srv.Config(2)); !errors.Is(err, nntp.ErrServiceUnavailable) {
		t.Errorf("second connection: %v", err)
	}
}

func TestContextCancelAbortsDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := nntp.Dial(ctx, nntptest.New(t).Config(1)); err == nil {
		t.Error("dial with cancelled context succeeded")
	}
}

func part(data []byte) yenc.Part {
	return yenc.Part{Name: "f.bin", FileSize: int64(len(data)), Number: 1, Total: 1, Data: data}
}

func TestPoolFallsBackToBackupServer(t *testing.T) {
	primary, backup := nntptest.New(t), nntptest.New(t)
	data := []byte("hello from the backup server")
	backup.AddPart("only-backup@test", part(data))
	primary.AddPart("both@test", part([]byte("primary")))
	backup.AddPart("both@test", part([]byte("backup!")))

	bcfg := backup.Config(2)
	bcfg.Priority = 10
	pool, err := nntp.NewPool([]nntp.Config{bcfg, primary.Config(2)}, discard)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if pool.Connections() != 2 {
		t.Errorf("Connections = %d, want primary only (2)", pool.Connections())
	}

	d := yenc.NewDecoder()
	if err := pool.Body(context.Background(), "only-backup@test", d); err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !bytes.Equal(d.Part().Data, data) {
		t.Errorf("data = %q", d.Part().Data)
	}
	if err := pool.Body(context.Background(), "both@test", d); err != nil || string(d.Part().Data) != "primary" {
		t.Errorf("priority not respected: %q %v", d.Part().Data, err)
	}
	if err := pool.Body(context.Background(), "nowhere@test", d); !errors.Is(err, nntp.ErrNoSuchArticle) {
		t.Errorf("missing everywhere: %v", err)
	}
}

func TestPoolRetriesAfterDroppedConnection(t *testing.T) {
	srv := nntptest.New(t)
	data := bytes.Repeat([]byte("0123456789"), 5000)
	srv.AddPart("a@test", part(data))
	srv.DropOnce("a@test")

	pool, _ := nntp.NewPool([]nntp.Config{srv.Config(1)}, discard)
	defer pool.Close()
	d := yenc.NewDecoder()
	if err := pool.Body(context.Background(), "a@test", d); err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !bytes.Equal(d.Part().Data, data) {
		t.Error("data differs after retry")
	}
	if n := srv.Requests.Load(); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestPoolCorruptArticleTriesNextServer(t *testing.T) {
	primary, backup := nntptest.New(t), nntptest.New(t)
	primary.AddArticle("x@test", []byte("=ybegin line=128 size=5 name=f\r\n!!!\r\n=yend size=5\r\n"))
	backup.AddPart("x@test", part([]byte("fine!")))
	bcfg := backup.Config(1)
	bcfg.Priority = 1
	pool, _ := nntp.NewPool([]nntp.Config{primary.Config(1), bcfg}, discard)
	defer pool.Close()

	d := yenc.NewDecoder()
	if err := pool.Body(context.Background(), "x@test", d); err != nil || string(d.Part().Data) != "fine!" {
		t.Errorf("got %q, %v", d.Part().Data, err)
	}
}

func TestPoolDisablesServerOnAuthFailure(t *testing.T) {
	srv := nntptest.New(t)
	srv.RequireAuth("user", "secret")
	cfg := srv.Config(1)
	cfg.Password = "wrong"
	pool, _ := nntp.NewPool([]nntp.Config{cfg}, discard)
	defer pool.Close()

	d := yenc.NewDecoder()
	if err := pool.Body(context.Background(), "a@test", d); !errors.Is(err, nntp.ErrAuthFailed) {
		t.Errorf("first: %v", err)
	}
	if err := pool.Body(context.Background(), "a@test", d); !errors.Is(err, nntp.ErrNoServers) {
		t.Errorf("second: %v", err)
	}
	if len(pool.Errors()) != 1 {
		t.Errorf("Errors = %v", pool.Errors())
	}
}

func TestPoolStat(t *testing.T) {
	primary, backup := nntptest.New(t), nntptest.New(t)
	backup.AddArticle("b@test", []byte("x\r\n"))
	bcfg := backup.Config(1)
	bcfg.Priority = 1
	pool, _ := nntp.NewPool([]nntp.Config{primary.Config(1), bcfg}, discard)
	defer pool.Close()
	if err := pool.Stat(context.Background(), "b@test"); err != nil {
		t.Errorf("article on backup: %v", err)
	}
	if err := pool.Stat(context.Background(), "nowhere@test"); !errors.Is(err, nntp.ErrNoSuchArticle) {
		t.Errorf("missing article: %v", err)
	}
}

func TestPoolStatMany(t *testing.T) {
	primary, backup := nntptest.New(t), nntptest.New(t)
	var ids []string
	for i := range 120 {
		id := fmt.Sprintf("a%d@test", i)
		ids = append(ids, id)
		switch {
		case i%10 == 3:
			backup.AddArticle(id, []byte("x\r\n")) // only on the backup
		case i%10 != 7:
			primary.AddArticle(id, []byte("x\r\n"))
		}
	}
	bcfg := backup.Config(1)
	bcfg.Priority = 1
	pool, _ := nntp.NewPool([]nntp.Config{primary.Config(1), bcfg}, discard)
	defer pool.Close()
	found, err := pool.StatMany(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	for i, ok := range found {
		if want := i%10 != 7; ok != want {
			t.Errorf("%s: found %v, want %v", ids[i], ok, want)
		}
	}
	// The connection is still in step afterwards.
	if err := pool.Stat(context.Background(), "a0@test"); err != nil {
		t.Errorf("stat after pipelining: %v", err)
	}
	if primary.Requests.Load()+backup.Requests.Load() != 0 {
		t.Error("article bodies were downloaded")
	}
}
