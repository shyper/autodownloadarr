package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

// postObfuscated posts the given files with random names in the NZB subjects
// while keeping their real names in the yEnc headers.
func postObfuscated(t *testing.T, srv *nntptest.Server, paths []string) []byte {
	t.Helper()
	n := &nzb.NZB{Meta: map[string][]string{}}
	for fi, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		const part = 8_000
		total := max((len(data)+part-1)/part, 1)
		f := nzb.File{Subject: fmt.Sprintf("%q", fmt.Sprintf("post%03d", fi))}
		for i := range total {
			begin, end := i*part, min((i+1)*part, len(data))
			id := fmt.Sprintf("stream-%d-%d@test", fi, i)
			srv.AddPart(id, yenc.Part{
				Name: name, FileSize: int64(len(data)), Number: i + 1, Total: total,
				Begin: int64(begin), Data: data[begin:end],
			})
			f.Segments = append(f.Segments, nzb.Segment{Number: i + 1, Bytes: int64(end - begin), MessageID: id})
		}
		n.Files = append(n.Files, f)
	}
	var buf bytes.Buffer
	if err := n.WriteXML(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestFetchStreamsObfuscatedRAR checks the case from a real release: the NZB
// subjects contain random names and only the yEnc headers know the RAR
// volume. The movie must become visible while the download is still running.
func TestFetchStreamsObfuscatedRAR(t *testing.T) {
	oldVisible := visibleAfter
	visibleAfter = 16 << 10
	defer func() { visibleAfter = oldVisible }()

	srv := nntptest.New(t)
	srv.SetDelay(4 * time.Millisecond)
	movie := bytes.Repeat([]byte("streaming movie data "), 30_000)
	volumes, err := rar5.Write(t.TempDir(), "Film.2024", []rar5.File{{Name: "film.mkv", Data: movie}}, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	data := postObfuscated(t, srv, volumes)

	pool, err := nntp.NewPool([]nntp.Config{srv.Config(4)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	root := t.TempDir()
	folder := filepath.Join(root, "Film (2024) {tmdb-42}")
	videoName := "Film (2024) {tmdb-42}"
	f := &Fetcher{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	type visibleFile struct {
		path string
		size int64
	}
	visible := make(chan visibleFile, 1)
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := f.Fetch(context.Background(), Request{
			Pool:         pool,
			NZB:          data,
			NZBName:      "Film.2024.German.720p.WEB-GRP.nzb",
			ReleaseTitle: "Film.2024.German.720p.WEB-GRP",
			Root:         root,
			WorkDir:      filepath.Join(root, WorkDirName, "incomplete", "1"),
			Folder:       folder,
			VideoName:    videoName,
			OnVisible: func(path string) {
				size := int64(-1)
				if info, err := os.Stat(path); err == nil {
					size = info.Size()
				}
				select {
				case visible <- visibleFile{path, size}:
				default:
				}
			},
		})
		done <- outcome{res, err}
	}()

	select {
	case got := <-visible:
		want := filepath.Join(folder, videoName+".mkv")
		if got.path != want {
			t.Errorf("OnVisible path = %q, want %q", got.path, want)
		}
		if got.size < 0 {
			t.Fatalf("visible movie %q does not exist", got.path)
		}
		if got.size >= int64(len(movie)) {
			t.Errorf("movie already complete at OnVisible (%d bytes); it was not streamed", got.size)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("movie did not become visible while downloading")
	}

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Fetch: %v", out.err)
		}
		if len(out.res.Videos) != 1 {
			t.Fatalf("videos = %v", out.res.Videos)
		}
		got, err := os.ReadFile(out.res.Videos[0])
		if err != nil || !bytes.Equal(got, movie) {
			t.Fatalf("movie differs: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Fetch did not finish")
	}

	if _, err := os.Stat(filepath.Join(root, WorkDirName, "incomplete", "1")); !os.IsNotExist(err) {
		t.Error("work dir not cleaned up")
	}
}

// TestFetchStreamsDirectVideo checks that a directly posted video (no
// archive) is copied into the library while it is still downloading, so it
// can be played before the download finishes. The NZB subjects are
// obfuscated; only the yEnc headers know the real name.
func TestFetchStreamsDirectVideo(t *testing.T) {
	oldVisible := visibleAfter
	visibleAfter = 16 << 10
	defer func() { visibleAfter = oldVisible }()

	srv := nntptest.New(t)
	srv.SetDelay(4 * time.Millisecond)
	video := bytes.Repeat([]byte("direct video data "), 30_000)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "movie.mkv"), video, 0o644); err != nil {
		t.Fatal(err)
	}
	data := postObfuscated(t, srv, []string{filepath.Join(src, "movie.mkv")})

	pool, err := nntp.NewPool([]nntp.Config{srv.Config(2)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	root := t.TempDir()
	folder := filepath.Join(root, "Film (2024) {tmdb-42}")
	videoName := "Film (2024) {tmdb-42}"
	f := &Fetcher{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	type visibleFile struct {
		path string
		size int64
	}
	visible := make(chan visibleFile, 1)
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := f.Fetch(context.Background(), Request{
			Pool:         pool,
			NZB:          data,
			ReleaseTitle: "Film.2024.German.720p.WEB-GRP",
			Root:         root,
			WorkDir:      filepath.Join(root, WorkDirName, "incomplete", "1"),
			Folder:       folder,
			VideoName:    videoName,
			OnVisible: func(path string) {
				size := int64(-1)
				if info, err := os.Stat(path); err == nil {
					size = info.Size()
				}
				select {
				case visible <- visibleFile{path, size}:
				default:
				}
			},
		})
		done <- outcome{res, err}
	}()

	select {
	case got := <-visible:
		want := filepath.Join(folder, videoName+".mkv")
		if got.path != want {
			t.Errorf("OnVisible path = %q, want %q", got.path, want)
		}
		if got.size < 0 {
			t.Fatalf("visible video %q does not exist", got.path)
		}
		if got.size >= int64(len(video)) {
			t.Errorf("video already complete at OnVisible (%d bytes); it was not streamed", got.size)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("video did not become visible while downloading")
	}

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Fetch: %v", out.err)
		}
		if len(out.res.Videos) != 1 {
			t.Fatalf("videos = %v", out.res.Videos)
		}
		got, err := os.ReadFile(out.res.Videos[0])
		if err != nil || !bytes.Equal(got, video) {
			t.Fatalf("video differs: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Fetch did not finish")
	}

	if _, err := os.Stat(filepath.Join(root, WorkDirName, "incomplete", "1")); !os.IsNotExist(err) {
		t.Error("work dir not cleaned up")
	}
}
