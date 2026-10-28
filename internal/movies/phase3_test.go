package movies

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/unpack"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

// postDir uploads every file of dir as release id. obfuscate replaces the
// names of non-PAR2 files in the subjects with random ones.
func (w *fakeWorld) postDir(t *testing.T, id, dir string, obfuscate bool) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	n := &nzb.NZB{Meta: map[string][]string{}}
	for fi, e := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		name := e.Name()
		if obfuscate && !strings.HasSuffix(name, ".par2") {
			name = fmt.Sprintf("a%07x%d", len(data)*7919, fi)
		}
		const part = 8000
		total := max((len(data)+part-1)/part, 1)
		f := nzb.File{Subject: fmt.Sprintf("%q yEnc (1/%d)", name, total)}
		for i := range total {
			begin, end := i*part, min((i+1)*part, len(data))
			mid := fmt.Sprintf("%s-%d-%d@test", id, fi, i)
			w.usenet.AddPart(mid, yenc.Part{Name: name, FileSize: int64(len(data)), Number: i + 1, Total: total, Begin: int64(begin), Data: data[begin:end]})
			f.Segments = append(f.Segments, nzb.Segment{Number: i + 1, Bytes: int64(end - begin), MessageID: mid})
		}
		n.Files = append(n.Files, f)
	}
	var buf bytes.Buffer
	if err := n.WriteXML(&buf); err != nil {
		t.Fatal(err)
	}
	w.nzbs[id] = buf.Bytes()
}

type harness struct {
	ctx   context.Context
	st    *store.Store
	m     *Manager
	world *fakeWorld
	root  string
}

// newHarness starts a manager against fake services serving one release.
func newHarness(t *testing.T, world *fakeWorld) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var srvURL string
	srv := httptest.NewServer(world.handler(t, func() string { return srvURL }))
	srvURL = srv.URL

	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	must(t, err)
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	root := t.TempDir()
	g := store.DefaultGeneral()
	g.MoviesPath = root
	must(t, st.SetGeneral(ctx, g))
	must(t, st.SetTMDBKey(ctx, "key"))
	host, port, _ := net.SplitHostPort(world.usenet.Addr())
	portNum, _ := strconv.Atoi(port)
	_, err = st.SaveUsenetServer(ctx, store.UsenetServer{Host: host, Port: portNum, Connections: 4, Enabled: true})
	must(t, err)
	_, err = st.SaveIndexer(ctx, store.Indexer{Name: "fake", URL: srv.URL + "/indexer", APIKey: "k", Enabled: true})
	must(t, err)
	profile, _ := st.DefaultProfile(ctx)
	profile.Config.MinMBPerMinute, profile.Config.MaxMBPerMinute = 0, 0
	must(t, st.SaveProfile(ctx, profile))

	sched := scheduler.New(sqldb, log, scheduler.WithPollInterval(20*time.Millisecond))
	svc := &Services{Store: st, Log: log, TMDBBaseURL: srv.URL + "/tmdb"}
	m := NewManager(st, svc, sched, events.New(log), log)
	must(t, m.Start(ctx))
	done := make(chan struct{})
	go func() { _ = sched.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		svc.Close()
		srv.Close()
		_ = sqldb.Close()
	})
	return &harness{ctx: ctx, st: st, m: m, world: world, root: root}
}

// requestAndWait requests the movie and waits for a final state.
func (h *harness) requestAndWait(t *testing.T) store.Movie {
	t.Helper()
	mv, err := h.m.Request(h.ctx, 42, 0, 0)
	must(t, err)
	var cur store.Movie
	eventually(t, "a final state", func() bool {
		cur, _ = h.st.Movie(h.ctx, mv.ID)
		return cur.State == store.MovieAvailable || cur.State == store.MovieFailed || cur.State == store.MovieWaiting
	})
	return cur
}

func (h *harness) movieFile(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.root, "Film (2024) {tmdb-42}", "Film (2024) {tmdb-42}.mkv"))
	if err != nil {
		t.Fatalf("movie file: %v", err)
	}
	return data
}

func requireTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not installed", name)
	}
	return p
}

func run(t *testing.T, dir, tool string, args ...string) {
	t.Helper()
	cmd := exec.Command(tool, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", tool, args, err, out)
	}
}

func TestRepairWithPAR2(t *testing.T) {
	tool := requireTool(t, "par2")
	movie := bytes.Repeat([]byte("repairable movie data "), 6000)
	dir := t.TempDir()
	_, err := rar5.Write(dir, "film", []rar5.File{{Name: "film.mkv", Data: movie}}, 40_000)
	must(t, err)
	run(t, dir, tool, "create", "-q", "-q", "-s4000", "-r30", "-n3", "film.par2", "film.part1.rar", "film.part2.rar", "film.part3.rar", "film.part4.rar")

	world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
	world.postDir(t, "rel", dir, false)
	// One article of a RAR volume is gone; the recovery volumes fix it.
	entries, _ := os.ReadDir(dir)
	for fi, e := range entries {
		if e.Name() == "film.part2.rar" {
			world.usenet.AddArticle(fmt.Sprintf("rel-%d-1@test", fi), []byte("broken\r\n"))
		}
	}
	world.items = item("rel", "Film.2024.German.720p.WEB.h264-GRP")

	h := newHarness(t, world)
	mv := h.requestAndWait(t)
	if mv.State != store.MovieAvailable {
		t.Fatalf("state %s: %s", mv.State, mv.LastError)
	}
	if !bytes.Equal(h.movieFile(t), movie) {
		t.Error("repaired movie differs")
	}
	if bl, _ := h.st.BlocklistEntries(h.ctx, mv.ID); len(bl) != 0 {
		t.Errorf("repairable release blocklisted: %+v", bl)
	}
}

func TestObfuscated7zWithPasswordInNFO(t *testing.T) {
	requireTool(t, "par2")
	seven, err := unpack.SevenZip()
	if err != nil {
		t.Skip("7-Zip not installed")
	}
	movie := bytes.Repeat([]byte("seven zip movie "), 5000)
	src, dir := t.TempDir(), t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "film.mkv"), movie, 0o644))
	run(t, src, seven, "a", "-bd", "-pgeheim", filepath.Join(dir, "film.7z"), "film.mkv")
	run(t, dir, requireTool(t, "par2"), "create", "-q", "-q", "-r5", "-n1", "film.par2", "film.7z")
	must(t, os.WriteFile(filepath.Join(dir, "release.nfo"), []byte("Viel Spass!\nPasswort: geheim\n"), 0o644))

	world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
	world.postDir(t, "rel", dir, true)
	world.items = item("rel", "Film.2024.German.720p.WEB.h264-GRP")

	h := newHarness(t, world)
	mv := h.requestAndWait(t)
	if mv.State != store.MovieAvailable {
		att, _ := h.st.Attempts(h.ctx, mv.ID)
		t.Fatalf("state %s: %s; attempts %+v", mv.State, mv.LastError, att)
	}
	if !bytes.Equal(h.movieFile(t), movie) {
		t.Error("movie differs")
	}
}

func TestSpamNFOIsBlocklisted(t *testing.T) {
	seven, err := unpack.SevenZip()
	if err != nil {
		t.Skip("7-Zip not installed")
	}
	src, dir := t.TempDir(), t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "film.mkv"), []byte("x"), 0o644))
	run(t, src, seven, "a", "-bd", "-pnobodyknows", filepath.Join(dir, "film.7z"), "film.mkv")
	must(t, os.WriteFile(filepath.Join(dir, "info.nfo"), []byte("Das Passwort gibt es auf www.spam-board.to"), 0o644))

	world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
	world.postDir(t, "rel", dir, false)
	world.items = item("rel", "Film.2024.German.720p.WEB.h264-GRP")

	h := newHarness(t, world)
	mv := h.requestAndWait(t)
	bl, _ := h.st.BlocklistEntries(h.ctx, mv.ID)
	if len(bl) != 1 || bl[0].Reason != kindSpam {
		t.Errorf("blocklist = %+v (state %s, %s)", bl, mv.State, mv.LastError)
	}
}

func TestPrecheckSkipsGoneRelease(t *testing.T) {
	movie := bytes.Repeat([]byte("gone "), 20000)
	dir := t.TempDir()
	_, err := rar5.Write(dir, "film", []rar5.File{{Name: "film.mkv", Data: movie}}, 0)
	must(t, err)
	world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
	world.postDir(t, "rel", dir, false)
	// Replace every article with one the server does not have.
	world.nzbs["rel"] = bytes.ReplaceAll(world.nzbs["rel"], []byte("rel-"), []byte("gone-"))
	world.items = item("rel", "Film.2024.German.720p.WEB.h264-GRP")

	h := newHarness(t, world)
	mv := h.requestAndWait(t)
	if world.usenet.Requests.Load() != 0 {
		t.Errorf("%d bodies downloaded for a release that is gone", world.usenet.Requests.Load())
	}
	att, _ := h.st.Attempts(h.ctx, mv.ID)
	if len(att) == 0 || att[len(att)-1].ErrorKind != kindIncomplete || !strings.Contains(att[len(att)-1].Message, "sampled") {
		t.Errorf("attempts = %+v", att)
	}
}
