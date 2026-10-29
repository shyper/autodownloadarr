package download_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
)

// postObfuscated posts the files with random names in the subjects, like an
// obfuscated release. The real names stay in the yEnc headers.
func postObfuscated(srv *nntptest.Server, partSize int, files ...postedFile) *nzb.NZB {
	n := post(srv, partSize, files...)
	for i := range n.Files {
		n.Files[i].Filename = fmt.Sprintf("post%03d", i)
		n.Files[i].Subject = fmt.Sprintf("%q", n.Files[i].Filename)
	}
	return n
}

func TestWaitForNamesFindsObfuscatedVolumes(t *testing.T) {
	srv := nntptest.New(t)
	srv.SetDelay(8 * time.Millisecond)
	first := randomBytes(60_000, 31)
	second := randomBytes(60_000, 32)
	n := postObfuscated(srv, 4_000,
		postedFile{"film.part1.rar", first},
		postedFile{"film.part2.rar", second},
	)
	dir := t.TempDir()
	j, err := download.Start(context.Background(), n, download.Options{
		Pool: newPool(t, srv.Config(1)), Dir: dir, Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The subject name is random; only the first article reveals the real
	// volume name.
	got := j.WaitForNames(context.Background(), func(name string) bool {
		return strings.HasSuffix(name, ".part1.rar")
	})
	if len(got) != 1 || got[0] != "film.part1.rar" {
		t.Fatalf("WaitForNames = %v, want [film.part1.rar]", got)
	}
	if p := j.Progress(); p.DoneSegments == p.TotalSegments {
		t.Error("download already finished when the first name was reported")
	}

	// A predicate that never matches waits for the end of the download. It
	// must be released by the workers' finish signal, without a concurrent
	// Wait call.
	done := make(chan []string, 1)
	go func() {
		done <- j.WaitForNames(context.Background(), func(string) bool { return false })
	}()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("no-match result = %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WaitForNames deadlocked after the download finished")
	}

	res, err := j.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete() {
		t.Fatalf("download incomplete: %+v", res.Files)
	}
	for name, want := range map[string][]byte{"film.part1.rar": first, "film.part2.rar": second} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
