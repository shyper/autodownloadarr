package download_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
)

func TestPrecheck(t *testing.T) {
	srv := nntptest.New(t)
	n := post(srv, 1_000, postedFile{"a.rar", randomBytes(40_000, 1)}, postedFile{"a.vol00+01.par2", randomBytes(5_000, 2)})
	pool := newPool(t, srv.Config(2))

	checked, missing, err := download.Precheck(context.Background(), pool, n, 10)
	if err != nil || checked != 10 || missing != 0 {
		t.Errorf("complete release: %d checked, %d missing, %v", checked, missing, err)
	}
	// Remove most articles by replacing the NZB's IDs.
	for i := range n.Files[0].Segments {
		if i%2 == 0 {
			n.Files[0].Segments[i].MessageID = "gone-" + n.Files[0].Segments[i].MessageID
		}
	}
	_, missing, _ = download.Precheck(context.Background(), pool, n, 10)
	if missing == 0 {
		t.Error("missing articles not detected")
	}
	if srv.Requests.Load() != 0 {
		t.Error("precheck downloaded article bodies")
	}
}

func TestFullCheck(t *testing.T) {
	srv := nntptest.New(t)
	n := post(srv, 1_000, postedFile{"a.rar", randomBytes(40_000, 1)}, postedFile{"a.vol00+03.par2", randomBytes(4_000, 2)})
	pool := newPool(t, srv.Config(4))
	var last, total int
	res, err := download.FullCheck(context.Background(), pool, n, 3, func(done, all int) { last, total = done, all })
	if err != nil || res.Missing != 0 || !res.Repairable() || res.Segments != 44 || last != 44 || total != 44 {
		t.Fatalf("complete release: %+v %v, progress %d/%d", res, err, last, total)
	}
	if res.RecoveryBytes != 4_000 {
		t.Errorf("recovery bytes = %d", res.RecoveryBytes)
	}

	// Two articles gone: the 4 kB of recovery data can restore them.
	for _, i := range []int{5, 30} {
		n.Files[0].Segments[i].MessageID = "gone-" + n.Files[0].Segments[i].MessageID
	}
	res, err = download.FullCheck(context.Background(), pool, n, 3, nil)
	if err != nil || res.Missing != 2 || res.MissingBytes != 2_000 || !res.Repairable() {
		t.Errorf("two missing: %+v %v", res, err)
	}
	// Ten gone: beyond repair.
	for i := 10; i < 18; i++ {
		n.Files[0].Segments[i].MessageID = "gone-" + n.Files[0].Segments[i].MessageID
	}
	res, err = download.FullCheck(context.Background(), pool, n, 3, nil)
	if err != nil || res.Missing != 10 || res.Repairable() {
		t.Errorf("ten missing: %+v %v", res, err)
	}
	if srv.Requests.Load() != 0 {
		t.Error("the check downloaded article bodies")
	}
}

func TestMissingBytesAndGaps(t *testing.T) {
	srv := nntptest.New(t)
	n := post(srv, 1_000, postedFile{"a.rar", randomBytes(10_000, 3)})
	srv.AddArticle("f0-p3@test", []byte("bad\r\n"))
	srv.AddArticle("f0-p4@test", []byte("bad\r\n"))
	srv.AddArticle("f0-p8@test", []byte("bad\r\n"))
	res, err := download.Run(context.Background(), n, download.Options{Pool: newPool(t, srv.Config(2)), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f := res.Files[0]
	if f.Missing != 3 || f.MissingBytes != 3_000 || f.Gaps != 2 || f.Index != 0 {
		t.Errorf("file = %+v", f)
	}
}

func TestStallWatchdog(t *testing.T) {
	srv := nntptest.New(t)
	srv.SetDelay(2 * time.Second)
	n := post(srv, 1_000, postedFile{"a.bin", randomBytes(5_000, 4)})
	start := time.Now()
	_, err := download.Run(context.Background(), n, download.Options{Pool: newPool(t, srv.Config(1)), Dir: t.TempDir(), StallTimeout: 300 * time.Millisecond})
	if !errors.Is(err, download.ErrStalled) {
		t.Fatalf("err = %v, want ErrStalled", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("watchdog took %s", time.Since(start))
	}
}

func TestRateLimiter(t *testing.T) {
	l := download.NewRateLimiter(100_000) // 100 kB/s
	start := time.Now()
	for range 3 {
		if err := l.Wait(context.Background(), 50_000); err != nil {
			t.Fatal(err)
		}
	}
	// 150 kB at 100 kB/s with an empty bucket: about 1.5 s.
	if d := time.Since(start); d < 1200*time.Millisecond || d > 2500*time.Millisecond {
		t.Errorf("took %s", d)
	}
	l.SetRate(0)
	if err := l.Wait(context.Background(), 1<<30); err != nil {
		t.Error(err)
	}
}
