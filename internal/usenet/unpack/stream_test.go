package unpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

// postVolumes uploads the files in dir to srv, 5000 bytes per article.
func postVolumes(t *testing.T, srv *nntptest.Server, paths []string) *nzb.NZB {
	t.Helper()
	n := &nzb.NZB{}
	for fi, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(p)
		total := (len(data) + 4999) / 5000
		f := nzb.File{Subject: fmt.Sprintf("%q yEnc (1/%d)", name, total), Filename: name}
		for i := range total {
			begin, end := i*5000, min((i+1)*5000, len(data))
			id := fmt.Sprintf("v%d-%d@test", fi, i)
			srv.AddPart(id, yenc.Part{Name: name, FileSize: int64(len(data)), Number: i + 1, Total: total, Begin: int64(begin), Data: data[begin:end]})
			f.Segments = append(f.Segments, nzb.Segment{Number: i + 1, Bytes: int64(end - begin), MessageID: id})
		}
		n.Files = append(n.Files, f)
	}
	return n
}

func startDownload(t *testing.T, srv *nntptest.Server, n *nzb.NZB) *download.Job {
	t.Helper()
	pool, err := nntp.NewPool([]nntp.Config{srv.Config(3)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	j, err := download.Start(context.Background(), n, download.Options{Pool: pool, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestExtractWhileDownloading(t *testing.T) {
	src := t.TempDir()
	files := []rar5.File{
		{Name: "first.txt", Data: []byte("ready before the rest has arrived\n")},
		{Name: "Movie/movie.mkv", Data: movie()},
	}
	volumes, err := rar5.Write(src, "Movie.2024", files, 12_000)
	if err != nil {
		t.Fatal(err)
	}
	srv := nntptest.New(t)
	srv.SetDelay(10 * time.Millisecond)
	j := startDownload(t, srv, postVolumes(t, srv, volumes))

	first := FirstVolumes(j.Names())
	if len(first) != 1 || first[0] != "Movie.2024.part1.rar" {
		t.Fatalf("FirstVolumes = %v", first)
	}
	dest := t.TempDir()
	res, err := ExtractRARFS(context.Background(), j, first[0], dest, Options{Include: func(n string) bool { return n != "first.txt" }})
	if err != nil {
		t.Fatalf("ExtractRARFS: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "Movie", "movie.mkv"))
	if !bytes.Equal(got, files[1].Data) {
		t.Error("movie differs")
	}
	if _, err := os.Stat(filepath.Join(dest, "first.txt")); !os.IsNotExist(err) {
		t.Error("excluded file was extracted")
	}
	if len(res.Volumes) != len(volumes) {
		t.Errorf("volumes read = %v", res.Volumes)
	}
	if dl, err := j.Wait(); err != nil || !dl.Complete() {
		t.Errorf("download: %v %+v", err, dl.Files)
	}
	if matches, _ := filepath.Glob(filepath.Join(dest, "*", "*"+partialSuffix)); len(matches) > 0 {
		t.Errorf("partial files left: %v", matches)
	}
}

func TestExtractWhileDownloadingFailsOnMissingData(t *testing.T) {
	src := t.TempDir()
	volumes, _ := rar5.Write(src, "Movie", []rar5.File{{Name: "movie.mkv", Data: movie()}}, 12_000)
	srv := nntptest.New(t)
	n := postVolumes(t, srv, volumes)
	srv.AddArticle("v2-1@test", []byte("corrupt\r\n")) // third volume, second article

	j := startDownload(t, srv, n)
	dest := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := ExtractRARFS(context.Background(), j, "Movie.part1.rar", dest, Options{})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, download.ErrDataMissing) {
			t.Errorf("err = %v, want ErrDataMissing", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("extraction hangs on missing data")
	}
	_, _ = j.Wait()
	if _, err := os.Stat(filepath.Join(dest, "movie.mkv")); !os.IsNotExist(err) {
		t.Error("incomplete movie left behind")
	}
}
