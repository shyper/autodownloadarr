package download_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
)

func TestStreamReadsWhileDownloading(t *testing.T) {
	srv := nntptest.New(t)
	srv.SetDelay(5 * time.Millisecond)
	data := randomBytes(200_000, 11)
	n := post(srv, 4_000, postedFile{"movie.mkv", data})

	j, err := download.Start(context.Background(), n, download.Options{Pool: newPool(t, srv.Config(4)), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f, err := j.Open("movie.mkv") // before any article has arrived
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	first := make([]byte, 10)
	if _, err := io.ReadFull(f, first); err != nil {
		t.Fatal(err)
	}
	if p := j.Progress(); p.DoneSegments == p.TotalSegments {
		t.Error("download already finished; the test does not exercise streaming")
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := append(first, rest...); !bytes.Equal(got, data) {
		t.Fatalf("streamed %d bytes, want %d identical bytes", len(got), len(data))
	}
	if _, err := j.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamStopsAtMissingArticle(t *testing.T) {
	srv := nntptest.New(t)
	data := randomBytes(40_000, 12)
	n := post(srv, 4_000, postedFile{"movie.mkv", data})
	srv.AddArticle("f0-p5@test", []byte("garbage\r\n"))

	j, err := download.Start(context.Background(), n, download.Options{Pool: newPool(t, srv.Config(2)), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := j.Open("movie.mkv")
	defer f.Close()
	got, err := io.ReadAll(f)
	if !errors.Is(err, download.ErrDataMissing) {
		t.Fatalf("err = %v, want ErrDataMissing", err)
	}
	if !bytes.Equal(got, data[:16_000]) {
		t.Errorf("read %d bytes before the gap, want 16000", len(got))
	}
	_, _ = j.Wait()
}

func TestStreamOpenAfterCompletionAndUnknownNames(t *testing.T) {
	srv := nntptest.New(t)
	data := randomBytes(9_000, 13)
	n := post(srv, 4_000, postedFile{"a.bin", data})
	j, _ := download.Start(context.Background(), n, download.Options{Pool: newPool(t, srv.Config(2)), Dir: t.TempDir()})
	if _, err := j.Wait(); err != nil {
		t.Fatal(err)
	}

	if got, err := fs.ReadFile(j, "a.bin"); err != nil || !bytes.Equal(got, data) {
		t.Errorf("ReadFile after completion: %v", err)
	}
	if _, err := j.Open("nope.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unknown name: %v", err)
	}
	if _, err := j.Open("../a.bin"); err == nil {
		t.Error("invalid path accepted")
	}
}

func TestStreamUnblocksOnCancel(t *testing.T) {
	srv := nntptest.New(t)
	srv.SetDelay(200 * time.Millisecond)
	n := post(srv, 1_000, postedFile{"slow.bin", randomBytes(20_000, 14)})
	ctx, cancel := context.WithCancel(context.Background())
	j, _ := download.Start(ctx, n, download.Options{Pool: newPool(t, srv.Config(1)), Dir: t.TempDir()})
	f, _ := j.Open("slow.bin")
	defer f.Close()

	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(f)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("read succeeded after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader still blocked after cancel")
	}
	_, _ = j.Wait()
}

func TestDownloadOrderFollowsVolumes(t *testing.T) {
	srv := nntptest.New(t)
	// Posted out of order; the stream of part1 must not wait for part3.
	n := post(srv, 1_000,
		postedFile{"x.part3.rar", randomBytes(2_000, 1)},
		postedFile{"x.part1.rar", randomBytes(2_000, 2)},
		postedFile{"x.par2", randomBytes(500, 3)},
		postedFile{"x.part2.rar", randomBytes(2_000, 4)},
	)
	j, _ := download.Start(context.Background(), n, download.Options{Pool: newPool(t, srv.Config(1)), Dir: t.TempDir(), Workers: 1})
	want := []string{"x.par2", "x.part1.rar", "x.part2.rar", "x.part3.rar"}
	got := j.Names()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	_, _ = j.Wait()
}
