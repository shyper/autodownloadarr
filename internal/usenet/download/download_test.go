package download_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

type postedFile struct {
	name string
	data []byte
}

func randomBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// post uploads files to srv in parts of partSize bytes and returns the NZB.
func post(srv *nntptest.Server, partSize int, files ...postedFile) *nzb.NZB {
	n := &nzb.NZB{Meta: map[string][]string{}}
	for fi, f := range files {
		total := (len(f.data) + partSize - 1) / partSize
		nf := nzb.File{Subject: fmt.Sprintf(`"%s" yEnc (1/%d)`, f.name, total), Filename: f.name}
		for i := range total {
			begin := i * partSize
			end := min(begin+partSize, len(f.data))
			id := fmt.Sprintf("f%d-p%d@test", fi, i+1)
			srv.AddPart(id, yenc.Part{
				Name: f.name, FileSize: int64(len(f.data)), Number: i + 1, Total: total,
				Begin: int64(begin), Data: f.data[begin:end],
			})
			nf.Segments = append(nf.Segments, nzb.Segment{Number: i + 1, Bytes: int64(end - begin), MessageID: id})
		}
		n.Files = append(n.Files, nf)
	}
	return n
}

func newPool(t testing.TB, cfgs ...nntp.Config) *nntp.Pool {
	t.Helper()
	p, err := nntp.NewPool(cfgs, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestDownloadAssemblesFiles(t *testing.T) {
	srv := nntptest.New(t)
	files := []postedFile{
		{"Movie.2024.part01.rar", randomBytes(100_000, 1)},
		{"Movie.2024.part02.rar", randomBytes(37_123, 2)},
		{"Movie.2024.par2", randomBytes(2_000, 3)},
		{"Movie.2024.vol00+01.par2", randomBytes(5_000, 4)},
	}
	n := post(srv, 7_000, files...)
	dir := t.TempDir()

	var updates int
	res, err := download.Run(context.Background(), n, download.Options{
		Pool:       newPool(t, srv.Config(4)),
		Dir:        dir,
		OnProgress: func(download.Progress) { updates++ },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("incomplete: %+v", res.Files)
	}
	for _, f := range files[:3] {
		got, err := os.ReadFile(filepath.Join(dir, f.name))
		if err != nil || !bytes.Equal(got, f.data) {
			t.Errorf("%s differs (err %v)", f.name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, files[3].name)); !os.IsNotExist(err) {
		t.Error("recovery volume downloaded without IncludeRecovery")
	}
	if !res.Files[3].Skipped {
		t.Error("recovery volume not marked as skipped")
	}
	p := res.Progress
	if p.DoneSegments != p.TotalSegments || p.FailedSegments != 0 || p.DoneBytes != p.TotalBytes {
		t.Errorf("progress = %+v", p)
	}
	if updates == 0 {
		t.Error("no progress callback")
	}
}

func TestDownloadReportsMissingArticles(t *testing.T) {
	primary, backup := nntptest.New(t), nntptest.New(t)
	data := randomBytes(50_000, 9)
	n := post(primary, 10_000, postedFile{"file.mkv", data})

	// Part 2 vanished from the primary but the backup still has it;
	// part 4 is gone everywhere.
	post(backup, 10_000, postedFile{"file.mkv", data})
	primary.AddArticle("f0-p2@test", []byte("=ybegin line=128 size=50000 name=file.mkv\r\n"))
	backup.AddArticle("f0-p4@test", []byte("not yenc\r\n"))
	primary.AddArticle("f0-p4@test", []byte("not yenc\r\n"))

	bcfg := backup.Config(2)
	bcfg.Priority = 1
	dir := t.TempDir()
	res, err := download.Run(context.Background(), n, download.Options{Pool: newPool(t, primary.Config(2), bcfg), Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Complete() || res.Files[0].Missing != 1 || res.Progress.FailedSegments != 1 {
		t.Fatalf("files = %+v, progress = %+v", res.Files, res.Progress)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "file.mkv"))
	if len(got) != len(data) {
		t.Fatalf("size = %d, want %d (file must keep full size for PAR2)", len(got), len(data))
	}
	if !bytes.Equal(got[:30_000], data[:30_000]) || !bytes.Equal(got[40_000:], data[40_000:]) {
		t.Error("received parts differ")
	}
}

func TestDownloadStopsWhenNoServerIsUsable(t *testing.T) {
	srv := nntptest.New(t)
	srv.RequireAuth("user", "secret")
	n := post(srv, 1000, postedFile{"a.bin", randomBytes(10_000, 5)})
	cfg := srv.Config(2)
	cfg.Password = "wrong"

	_, err := download.Run(context.Background(), n, download.Options{Pool: newPool(t, cfg), Dir: t.TempDir()})
	if !errors.Is(err, nntp.ErrAuthFailed) && !errors.Is(err, nntp.ErrNoServers) {
		t.Fatalf("got %v, want an authentication error", err)
	}
}

func TestDownloadCancel(t *testing.T) {
	srv := nntptest.New(t)
	n := post(srv, 1000, postedFile{"a.bin", randomBytes(20_000, 6)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := download.Run(ctx, n, download.Options{Pool: newPool(t, srv.Config(2)), Dir: t.TempDir()}); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestNameHandling(t *testing.T) {
	srv := nntptest.New(t)
	n := post(srv, 1000,
		postedFile{"../../etc/evil.txt", []byte("evil")},
		postedFile{"same.bin", []byte("one")},
		postedFile{"same.bin", []byte("two")},
	)
	n.Files[0].Filename = "../../etc/evil.txt"
	dir := t.TempDir()
	res, err := download.Run(context.Background(), n, download.Options{Pool: newPool(t, srv.Config(1)), Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if filepath.Dir(f.Path) != dir {
			t.Errorf("%q escaped the download dir", f.Path)
		}
	}
	if res.Files[1].Name == res.Files[2].Name {
		t.Errorf("duplicate names: %q", res.Files[1].Name)
	}
}

func TestIsRecoveryPar2(t *testing.T) {
	for name, want := range map[string]bool{
		"x.vol00+01.par2":  true,
		"X.VOL127+64.PAR2": true,
		"x.par2":           false,
		"x.part01.rar":     false,
	} {
		if got := download.IsRecoveryPar2(name); got != want {
			t.Errorf("IsRecoveryPar2(%q) = %v", name, got)
		}
	}
}
