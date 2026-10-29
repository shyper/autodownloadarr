package series

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/metadata/tmdb"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

const episodes = 5

// world fakes TMDB, an indexer, Usenet and a Plex TV library for one show
// ("Show", 2020, TVDB 99) with one season of five aired episodes.
type world struct {
	usenet *nntptest.Server
	nzbs   map[int][]byte // episode → NZB

	mu       sync.Mutex
	progress map[int]float64       // episode → Plex progress (1 = watched)
	delay    map[int]time.Duration // episode → how long its search takes
	old      map[int]bool          // episode → the Plex history is from long ago
	views    map[int]int           // episode → earlier complete viewings
	seasons  int                   // seasons TMDB knows (episodes of later ones air in 2020-02)
	fetched  []int                 // episodes in the order their NZB was fetched
}

func (w *world) post(t *testing.T, ep int) {
	t.Helper()
	data := bytes.Repeat([]byte(fmt.Sprintf("episode %d ", ep)), 4000)
	vols, err := rar5.Write(t.TempDir(), fmt.Sprintf("show.s01e%02d", ep), []rar5.File{{Name: fmt.Sprintf("show.s01e%02d.mkv", ep), Data: data}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(vols[0])
	name := filepath.Base(vols[0])
	n := &nzb.NZB{}
	f := nzb.File{Subject: fmt.Sprintf("%q yEnc (1/1)", name)}
	id := fmt.Sprintf("e%d@test", ep)
	w.usenet.AddPart(id, yenc.Part{Name: name, FileSize: int64(len(raw)), Number: 1, Total: 1, Data: raw})
	f.Segments = []nzb.Segment{{Number: 1, Bytes: int64(len(raw)), MessageID: id}}
	n.Files = []nzb.File{f}
	var buf bytes.Buffer
	_ = n.WriteXML(&buf)
	w.nzbs[ep] = buf.Bytes()
}

func (w *world) setProgress(ep int, p float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.progress[ep] = p
}

func (w *world) handler(t *testing.T, base *string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/tmdb/tv/7", func(rw http.ResponseWriter, _ *http.Request) {
		w.mu.Lock()
		n := max(w.seasons, 1)
		w.mu.Unlock()
		var seasons []string
		for i := 1; i <= n; i++ {
			seasons = append(seasons, fmt.Sprintf(`{"season_number":%d,"name":"Staffel %d","episode_count":%d,"air_date":"2020-%02d-01"}`,
				i, i, episodes, i))
		}
		_, _ = fmt.Fprintf(rw, `{"id":7,"name":"Show","first_air_date":"2020-01-01","status":"Returning Series","episode_run_time":[45],
			"seasons":[%s],"external_ids":{"tvdb_id":99}}`, strings.Join(seasons, ","))
	})
	mux.HandleFunc("/tmdb/tv/7/season/2", func(rw http.ResponseWriter, _ *http.Request) {
		var parts []string
		for ep := 1; ep <= episodes; ep++ {
			parts = append(parts, fmt.Sprintf(`{"season_number":2,"episode_number":%d,"name":"Neu %d","air_date":"2020-02-%02d"}`, ep, ep, ep))
		}
		_, _ = fmt.Fprintf(rw, `{"episodes":[%s]}`, strings.Join(parts, ","))
	})
	mux.HandleFunc("/tmdb/tv/7/season/1", func(rw http.ResponseWriter, _ *http.Request) {
		var parts []string
		for ep := 1; ep <= episodes; ep++ {
			parts = append(parts, fmt.Sprintf(`{"season_number":1,"episode_number":%d,"name":"Folge %d","air_date":"2020-01-%02d"}`, ep, ep, ep))
		}
		_, _ = fmt.Fprintf(rw, `{"episodes":[%s]}`, strings.Join(parts, ","))
	})
	mux.HandleFunc("/indexer/api", func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("t") == "caps" {
			_, _ = rw.Write([]byte(`<caps><searching><tv-search available="yes" supportedParams="q,season,ep,tvdbid"/></searching></caps>`))
			return
		}
		ep, _ := strconv.Atoi(q.Get("ep"))
		if q.Get("tvdbid") != "99" || q.Get("season") != "1" || ep == 0 {
			t.Errorf("unexpected search %s", r.URL.RawQuery)
		}
		w.mu.Lock()
		d := w.delay[ep]
		w.mu.Unlock()
		time.Sleep(d)
		_, _ = fmt.Fprintf(rw, `<rss><channel><item><title>Show.S01E%02d.German.720p.WEB.h264-GRP</title><guid>e%d</guid>
			<link>%s/indexer/getnzb/%d</link><attr name="size" value="40000"/></item></channel></rss>`, ep, ep, *base, ep)
	})
	mux.HandleFunc("/indexer/getnzb/", func(rw http.ResponseWriter, r *http.Request) {
		ep, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/indexer/getnzb/"))
		w.mu.Lock()
		w.fetched = append(w.fetched, ep)
		w.mu.Unlock()
		_, _ = rw.Write(w.nzbs[ep])
	})
	mux.HandleFunc("/plex/library/sections/2/refresh", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/plex/library/sections/2/all", func(rw http.ResponseWriter, _ *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		var parts []string
		for ep := 1; ep <= episodes; ep++ {
			p := w.progress[ep]
			views, offset, viewed := 0, int(p*2_700_000), int64(0)
			if p >= 1 {
				views, offset = 1, 0
			}
			views += w.views[ep]
			if p > 0 {
				viewed = time.Now().Unix() // Plex updates it while playing
			}
			if w.old[ep] {
				viewed = time.Now().AddDate(-1, 0, 0).Unix()
			}
			file := fmt.Sprintf("/remote/tv/Show (2020) {tvdb-99}/Season 01/Show (2020) - S01E%02d - Folge %d.mkv", ep, ep)
			parts = append(parts, fmt.Sprintf(`{"ratingKey":"%d","guid":"plex://episode/%d","grandparentTitle":"Show","parentIndex":1,"index":%d,"viewCount":%d,
				"viewOffset":%d,"duration":2700000,"lastViewedAt":%d,"Media":[{"Part":[{"file":%q}]}]}`,
				100+ep, ep, ep, views, offset, viewed, file))
		}
		_, _ = fmt.Fprintf(rw, `{"MediaContainer":{"Metadata":[%s]}}`, strings.Join(parts, ","))
	})
	return mux
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// setup starts a manager against the fake world; general settings can be
// adjusted with tune. It returns the manager, its store and the series root.
func setup(ctx context.Context, t *testing.T, w *world, tune func(*store.General)) (*Manager, *store.Store, string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for ep := 1; ep <= episodes; ep++ {
		w.post(t, ep)
	}
	var base string
	srv := httptest.NewServer(w.handler(t, &base))
	t.Cleanup(srv.Close)
	base = srv.URL

	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	must(t, err)
	t.Cleanup(func() { _ = sqldb.Close() })
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	root := t.TempDir()
	g := store.DefaultGeneral()
	g.SeriesPath = root
	if tune != nil {
		tune(&g)
	}
	must(t, st.SetGeneral(ctx, g))
	must(t, st.SetTMDBKey(ctx, "key"))
	host, port, _ := net.SplitHostPort(w.usenet.Addr())
	portNum, _ := strconv.Atoi(port)
	_, err = st.SaveUsenetServer(ctx, store.UsenetServer{Host: host, Port: portNum, Connections: 2, Enabled: true})
	must(t, err)
	_, err = st.SaveIndexer(ctx, store.Indexer{Name: "fake", URL: srv.URL + "/indexer", APIKey: "k", Enabled: true})
	must(t, err)
	_, err = st.SaveMediaServer(ctx, store.MediaServer{Name: "plex", URL: srv.URL + "/plex", Token: "t", TVLibrary: "2", Enabled: true,
		Mappings: []pathmap.Mapping{{Local: root, Remote: "/remote/tv"}}})
	must(t, err)
	profile, _ := st.DefaultProfile(ctx)
	profile.Config.MinMBPerMinute, profile.Config.MaxMBPerMinute = 0, 0
	must(t, st.SaveProfile(ctx, profile))

	sched := scheduler.New(sqldb, log, scheduler.WithPollInterval(20*time.Millisecond))
	svc := &movies.Services{Store: st, Log: log, TMDBBaseURL: srv.URL + "/tmdb"}
	t.Cleanup(svc.Close)
	m := NewManager(st, svc, sched, events.New(log), log, download.NewRateLimiter(0))
	must(t, m.Start(ctx))
	sctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = sched.Run(sctx); close(done) }()
	t.Cleanup(func() { stop(); <-done })
	return m, st, root
}

func newWorld(t *testing.T) *world {
	return &world{usenet: nntptest.New(t), nzbs: map[int][]byte{}, progress: map[int]float64{}, delay: map[int]time.Duration{},
		old: map[int]bool{}, views: map[int]int{}}
}

func TestRollingWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	m, st, root := setup(ctx, t, w, nil)

	se, err := m.Request(ctx, 7, 0, RequestOptions{WindowSize: 2, Trigger: store.TriggerProgress, TriggerValue: 10})
	must(t, err)
	states := func() map[int]string {
		eps, _ := st.Episodes(ctx, se.ID)
		out := map[int]string{}
		for _, e := range eps {
			out[e.Episode] = e.State
		}
		return out
	}
	waitStates := func(what string, want map[int]string) {
		t.Helper()
		eventually(t, what, func() bool {
			got := states()
			for ep, s := range want {
				if got[ep] != s {
					return false
				}
			}
			return true
		})
	}

	// The window holds episodes 1 and 2.
	waitStates("first window", map[int]string{1: store.MovieAvailable, 2: store.MovieAvailable, 3: store.EpisodePending})
	file1 := filepath.Join(root, "Show (2020) {tvdb-99}", "Season 01", "Show (2020) - S01E01 - Folge 1.mkv")
	if data, err := os.ReadFile(file1); err != nil || !bytes.HasPrefix(data, []byte("episode 1 ")) {
		t.Fatalf("episode 1 file: %v", err)
	}

	// Watching episode 1 changes nothing yet.
	w.setProgress(1, 0.5)
	must(t, m.PollWatched(ctx))
	if s := states(); s[3] != store.EpisodePending || s[1] != store.MovieAvailable {
		t.Errorf("after starting episode 1: %v", s)
	}

	// Episode 1 finished, episode 2 at 15 %: episode 1 is deleted and
	// episode 3 is fetched.
	w.setProgress(1, 1)
	w.setProgress(2, 0.15)
	must(t, m.PollWatched(ctx))
	waitStates("window moved", map[int]string{1: store.MovieDeleted, 2: store.MovieAvailable, 3: store.MovieAvailable, 4: store.EpisodePending})
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Error("episode 1 file still exists")
	}

	// Episode 2 finished, episode 3 started: 2 goes, 4 comes.
	w.setProgress(2, 1)
	w.setProgress(3, 0.5)
	must(t, m.PollWatched(ctx))
	waitStates("episode 4 fetched", map[int]string{2: store.MovieDeleted, 3: store.MovieAvailable, 4: store.MovieAvailable, 5: store.EpisodePending})

	// Skipping the rest of 3 and starting 4 deletes 3 and fetches 5.
	w.setProgress(4, 0.2)
	must(t, m.PollWatched(ctx))
	waitStates("last episode", map[int]string{3: store.MovieDeleted, 4: store.MovieAvailable, 5: store.MovieAvailable})
}

// The searches for the window run in parallel; even when a later episode's
// search finishes first, the episodes are downloaded in broadcast order,
// one after another.
func TestEpisodesDownloadInOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	w.delay[1], w.delay[2] = 400*time.Millisecond, 200*time.Millisecond
	m, st, _ := setup(ctx, t, w, func(g *store.General) { g.ParallelDownloads = 3 })

	se, err := m.Request(ctx, 7, 0, RequestOptions{WindowSize: 3, Trigger: store.TriggerProgress, TriggerValue: 10})
	must(t, err)
	eventually(t, "window downloaded", func() bool {
		eps, _ := st.Episodes(ctx, se.ID)
		n := 0
		for _, e := range eps {
			if e.State == store.MovieAvailable {
				n++
			}
		}
		return n == 3
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	if fmt.Sprint(w.fetched) != "[1 2 3]" {
		t.Errorf("download order = %v, want [1 2 3]", w.fetched)
	}
}

// Plex keeps the history of a show: episodes watched long ago show up as
// watched as soon as their files are back. That must neither delete them
// nor move the window.
func TestOldWatchHistoryIsIgnored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	for ep := 1; ep <= episodes; ep++ {
		w.progress[ep], w.old[ep] = 1, true
	}
	m, st, _ := setup(ctx, t, w, nil)
	se, err := m.Request(ctx, 7, 0, RequestOptions{WindowSize: 2, Trigger: store.TriggerProgress, TriggerValue: 10})
	must(t, err)
	states := func() string {
		eps, _ := st.Episodes(ctx, se.ID)
		var out []string
		for _, e := range eps {
			out = append(out, e.State)
		}
		return strings.Join(out, " ")
	}
	want := "available available pending pending pending"
	eventually(t, "first window", func() bool { return states() == want })
	for range 3 {
		must(t, m.PollWatched(ctx))
	}
	time.Sleep(200 * time.Millisecond)
	if got := states(); got != want {
		t.Fatalf("after polls with old history: %s, want %s", got, want)
	}

	// Watching episode 2 again now counts.
	w.mu.Lock()
	w.old[2], w.views[2], w.progress[2] = false, 1, 0.5
	w.mu.Unlock()
	must(t, m.PollWatched(ctx))
	eventually(t, "window moved", func() bool { return states() == "deleted available available pending pending" })
}

// With future seasons, a season TMDB adds later is picked up by the daily
// refresh; without, it is ignored. Seasons that already existed are not
// "future" ones.
func TestFutureSeasons(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	m, st, _ := setup(ctx, t, w, nil)
	follow, err := m.Request(ctx, 7, 0, RequestOptions{Seasons: []int{1}, FutureSeasons: true, WindowSize: 1})
	must(t, err)
	if follow.KnownSeasons != 1 {
		t.Errorf("known seasons = %d", follow.KnownSeasons)
	}

	w.mu.Lock()
	w.seasons = 2
	w.mu.Unlock()
	refresh := func(id int64) {
		t.Helper()
		payload, _ := json.Marshal(idPayload{ID: id})
		must(t, m.handleRefresh(ctx, scheduler.Task{Payload: payload}))
	}
	refresh(follow.ID)
	count := func(id int64, season int) int {
		eps, _ := st.Episodes(ctx, id)
		n := 0
		for _, e := range eps {
			if e.Season == season {
				n++
			}
		}
		return n
	}
	if n := count(follow.ID, 2); n != episodes {
		t.Errorf("future season: %d episodes of season 2, want %d", n, episodes)
	}

	// Without the option the new season stays out.
	must(t, m.Delete(ctx, follow.ID))
	only, err := m.Request(ctx, 7, 0, RequestOptions{Seasons: []int{1}, WindowSize: 1})
	must(t, err)
	refresh(only.ID)
	if n := count(only.ID, 2); n != 0 {
		t.Errorf("without future seasons: %d episodes of season 2", n)
	}

	// Switching the option on now: season 2 has aired already, so it is
	// not a future season; an announced season 3 would be.
	_, err = m.Update(ctx, only.ID, Settings{Seasons: []int{1}, FutureSeasons: true, WindowSize: 1, Trigger: store.TriggerProgress, TriggerValue: 10})
	must(t, err)
	if se, _ := st.Series(ctx, only.ID); se.KnownSeasons != 2 || se.Wants(2) || !se.Wants(3) {
		t.Errorf("after update: known %d, wants 2 %v, wants 3 %v", se.KnownSeasons, se.Wants(2), se.Wants(3))
	}
}

func TestWants(t *testing.T) {
	cases := []struct {
		se   store.Series
		want map[int]bool
	}{
		{store.Series{Seasons: []int{}}, map[int]bool{0: false, 1: true, 9: true}},
		{store.Series{Seasons: []int{0, 2}}, map[int]bool{0: true, 1: false, 2: true, 3: false}},
		{store.Series{Seasons: []int{1}, FutureSeasons: true, KnownSeasons: 3}, map[int]bool{0: false, 1: true, 2: false, 3: false, 4: true}},
	}
	for i, c := range cases {
		for season, want := range c.want {
			if got := c.se.Wants(season); got != want {
				t.Errorf("case %d: Wants(%d) = %v, want %v", i, season, got, want)
			}
		}
	}
}

func TestDeletionDue(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	started := now.Add(-20 * time.Minute)
	watched := now.Add(-time.Hour)
	list := []store.Episode{{ID: 1, WatchedAt: &watched, Progress: 1}, {ID: 2, Progress: 0.05, StartedAt: &started}}
	g := store.DefaultGeneral()

	se := store.Series{DeleteTrigger: store.TriggerProgress, TriggerValue: 10}
	if due, _ := deletionDue(se, g, list, 0, now); due {
		t.Error("progress 5 % < 10 % should not delete")
	}
	se.TriggerValue = 5
	if due, _ := deletionDue(se, g, list, 0, now); !due {
		t.Error("progress 5 % >= 5 % should delete")
	}
	se = store.Series{DeleteTrigger: store.TriggerMinutes, TriggerValue: 30}
	if due, at := deletionDue(se, g, list, 0, now); due || !at.Equal(started.Add(30*time.Minute)) {
		t.Errorf("minutes: due %v at %v", due, at)
	}
	se = store.Series{DeleteTrigger: store.TriggerWatched, TriggerValue: 0}
	if due, _ := deletionDue(se, g, list, 0, now); !due {
		t.Error("watched trigger should delete")
	}
	// A skipped episode counts as seen once a later one has been watched.
	skipped := []store.Episode{{ID: 1}, {ID: 2, Progress: 1, WatchedAt: &watched}, {ID: 3}}
	se = store.Series{DeleteTrigger: store.TriggerWatched, TriggerValue: 90}
	if due, at := deletionDue(se, g, skipped, 0, now); due || !at.Equal(watched.Add(90*time.Minute)) {
		t.Errorf("skipped, watched trigger: due %v at %v", due, at)
	}
	if due, _ := deletionDue(se, g, skipped, 0, now.Add(time.Hour)); !due {
		t.Error("skipped episode should be deleted 90 minutes after the next one was watched")
	}
	if due, at := deletionDue(se, g, skipped[:1], 0, now); due || !at.IsZero() {
		t.Errorf("nothing watched yet: due %v at %v", due, at)
	}
	for _, trig := range []string{store.TriggerProgress, store.TriggerMinutes} {
		se = store.Series{DeleteTrigger: trig, TriggerValue: 10}
		s2 := []store.Episode{{ID: 1}, {ID: 2, Progress: 0.5, StartedAt: &watched}}
		if due, _ := deletionDue(se, g, s2, 0, now); !due {
			t.Errorf("skipped, %s trigger: not deleted", trig)
		}
	}

	// The last episode falls back to the movie delay.
	se = store.Series{DeleteTrigger: store.TriggerProgress, TriggerValue: 10}
	if due, at := deletionDue(se, g, list[:1], 0, now); due || !at.Equal(watched.Add(48*time.Hour)) {
		t.Errorf("finale: due %v at %v", due, at)
	}
}

func TestNames(t *testing.T) {
	se := store.Series{Title: "Mr. Robot: Season", Year: 2015, TVDBID: 289590}
	if got := SeriesFolder(se); got != "Mr. Robot Season (2015) {tvdb-289590}" {
		t.Errorf("SeriesFolder = %q", got)
	}
	if got := EpisodeFileName(se, store.Episode{Season: 1, Episode: 3, Title: "eps1.2_d3bug.mkv"}); got != "Mr. Robot Season (2015) - S01E03 - eps1.2_d3bug.mkv" {
		t.Errorf("EpisodeFileName = %q", got)
	}
}

func TestLastSeason(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	show := tmdb.Show{Seasons: []tmdb.SeasonInfo{
		{Number: 0, AirDate: "2018-01-01"}, {Number: 1, AirDate: "2017-12-01"}, {Number: 2, AirDate: "2026-10-29"},
		{Number: 3, AirDate: "2027-06-01"}, {Number: 4},
	}}
	if got := lastSeason(show, now); got != 2 {
		t.Errorf("lastSeason = %d, want 2 (season 3 is only announced)", got)
	}
}

func TestEpisodeDownloadsReportPlayable(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.New(log)
	m := &Manager{active: map[int64]*activeDownload{}, bus: bus}
	m.active[9] = &activeDownload{
		episode:  store.Episode{ID: 9, SeriesID: 3, Season: 1, Episode: 4},
		release:  store.Release{Title: "Show.S01E04"},
		progress: download.Progress{DoneBytes: 42},
	}
	ch, unsubscribe := bus.Subscribe(8, movies.EventDownloadProgress)
	defer unsubscribe()

	m.markPlayable(9)

	list := m.Downloads()
	if len(list) != 1 || !list[0].Playable || list[0].EpisodeID != 9 || list[0].ReleaseTitle != "Show.S01E04" {
		t.Fatalf("downloads = %+v", list)
	}
	select {
	case ev := <-ch:
		got, ok := ev.Data.(Download)
		if !ok || !got.Playable || got.EpisodeID != 9 || got.Progress.DoneBytes != 42 {
			t.Errorf("event = %+v", ev.Data)
		}
	default:
		t.Fatal("no playable event published")
	}

	// Later progress updates keep the flag.
	m.progress(store.Episode{ID: 9, SeriesID: 3, Season: 1, Episode: 4}, download.Progress{DoneBytes: 100})
	list = m.Downloads()
	if !list[0].Playable || list[0].Progress.DoneBytes != 100 {
		t.Errorf("after progress: %+v", list[0])
	}
}
