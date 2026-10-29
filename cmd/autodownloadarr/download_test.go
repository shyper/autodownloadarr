package main

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

// TestDownloadNZBCommand runs the command end to end against the fake
// server: download, extraction while downloading and clean-up.
func TestDownloadNZBCommand(t *testing.T) {
	srv := nntptest.New(t)
	srv.RequireAuth("user", "secret")

	movie := bytes.Repeat([]byte("frame "), 20_000)
	volumes, err := rar5.Write(t.TempDir(), "Movie.2024", []rar5.File{{Name: "Movie.2024.mkv", Data: movie}}, 40_000)
	if err != nil {
		t.Fatal(err)
	}

	var xml strings.Builder
	xml.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
	for vi, v := range volumes {
		data, _ := os.ReadFile(v)
		name := filepath.Base(v)
		total := (len(data) + 9999) / 10000
		fmt.Fprintf(&xml, `<file poster="t" date="1" subject="%s"><groups><group>a.b.test</group></groups><segments>`,
			html.EscapeString(fmt.Sprintf("%q yEnc (1/%d)", name, total)))
		for i := range total {
			begin, end := i*10000, min((i+1)*10000, len(data))
			id := fmt.Sprintf("v%d.%d@test", vi, i)
			srv.AddPart(id, yenc.Part{Name: name, FileSize: int64(len(data)), Number: i + 1, Total: total, Begin: int64(begin), Data: data[begin:end]})
			fmt.Fprintf(&xml, `<segment bytes="%d" number="%d">%s</segment>`, end-begin, i+1, id)
		}
		xml.WriteString(`</segments></file>`)
	}
	xml.WriteString(`</nzb>`)
	nzbPath := filepath.Join(t.TempDir(), "Movie.2024.nzb")
	if err := os.WriteFile(nzbPath, []byte(xml.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	t.Setenv("AUTODOWNLOADARR_NNTP_PASSWORD", "secret")
	code := run([]string{"download-nzb", "-server", srv.Addr(), "-tls=false", "-user", "user", "-connections", "4", "-out", out, nzbPath})
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}

	dir := filepath.Join(out, "Movie.2024")
	got, err := os.ReadFile(filepath.Join(dir, "Movie.2024.mkv"))
	if err != nil || !bytes.Equal(got, movie) {
		t.Fatalf("extracted movie differs: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("volumes not cleaned up: %v", names)
	}
}

func TestDownloadNZBUsage(t *testing.T) {
	t.Setenv("AUTODOWNLOADARR_NNTP_SERVER", "")
	if code := run([]string{"download-nzb"}); code != 2 {
		t.Errorf("exit code %d, want 2", code)
	}
}

// TestDownloadNZBCommandObfuscatedNames covers releases whose NZB subjects
// only contain random names: the real RAR volume names come from the yEnc
// headers and extraction must still follow the download.
func TestDownloadNZBCommandObfuscatedNames(t *testing.T) {
	srv := nntptest.New(t)
	srv.SetDelay(2 * time.Millisecond)

	movie := bytes.Repeat([]byte("frame "), 20_000)
	volumes, err := rar5.Write(t.TempDir(), "Movie.2024", []rar5.File{{Name: "Movie.2024.mkv", Data: movie}}, 40_000)
	if err != nil {
		t.Fatal(err)
	}

	var xml strings.Builder
	xml.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
	for vi, v := range volumes {
		data, _ := os.ReadFile(v)
		name := filepath.Base(v)
		total := (len(data) + 9999) / 10000
		fmt.Fprintf(&xml, `<file poster="t" date="1" subject="%s"><groups><group>a.b.test</group></groups><segments>`,
			html.EscapeString(fmt.Sprintf("%q yEnc (1/%d)", fmt.Sprintf("post%03d", vi), total)))
		for i := range total {
			begin, end := i*10000, min((i+1)*10000, len(data))
			id := fmt.Sprintf("v%d.%d@test", vi, i)
			srv.AddPart(id, yenc.Part{Name: name, FileSize: int64(len(data)), Number: i + 1, Total: total, Begin: int64(begin), Data: data[begin:end]})
			fmt.Fprintf(&xml, `<segment bytes="%d" number="%d">%s</segment>`, end-begin, i+1, id)
		}
		xml.WriteString(`</segments></file>`)
	}
	xml.WriteString(`</nzb>`)
	nzbPath := filepath.Join(t.TempDir(), "Movie.2024.nzb")
	if err := os.WriteFile(nzbPath, []byte(xml.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	code := run([]string{"download-nzb", "-server", srv.Addr(), "-tls=false", "-connections", "4", "-out", out, nzbPath})
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}

	dir := filepath.Join(out, "Movie.2024")
	got, err := os.ReadFile(filepath.Join(dir, "Movie.2024.mkv"))
	if err != nil || !bytes.Equal(got, movie) {
		t.Fatalf("extracted movie differs: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("volumes not cleaned up: %v", names)
	}
}
