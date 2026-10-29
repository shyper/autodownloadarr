package fetch

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/unpack"
)

// passwordSource hands out archive passwords: the known ones first, then
// those found in the NFO files of the release (read only when needed).
type passwordSource struct {
	known   []string
	nfo     func() []string
	nfoRead bool
	spam    bool
}

func (p *passwordSource) all() []string {
	if !p.nfoRead && p.nfo != nil {
		p.nfoRead = true
		for _, pw := range p.nfo() {
			if pw == spamMarker {
				p.spam = true
				continue
			}
			if !slices.Contains(p.known, pw) {
				p.known = append(p.known, pw)
			}
		}
	}
	return p.known
}

// scanDir looks for passwords in small text files of a finished download,
// e.g. NFOs whose names were obfuscated.
func (p *passwordSource) scanDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || !looksLikeText(data) {
			continue
		}
		text := string(data)
		if nzb.LooksLikeSpam(text) {
			p.spam = true
		}
		for _, pw := range nzb.PasswordsFromText(text) {
			if !slices.Contains(p.known, pw) {
				p.known = append(p.known, pw)
			}
		}
	}
}

// looksLikeText reports whether data is mostly printable text.
func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	printable := 0
	for _, b := range data {
		if b >= 32 || b == '\n' || b == '\r' || b == '\t' {
			printable++
		}
	}
	return printable*100 >= len(data)*95
}

// extract extracts an archive from the running download. Without a
// password it tries the candidates in turn and returns the one that worked.
func extract(ctx context.Context, job *download.Job, first, folder string, src *passwordSource,
	videoName string, onVisible func(string)) ([]string, string, error) {
	opts := func(pw string) unpack.Options {
		o := unpack.Options{Password: pw, Include: wanted}
		if videoName != "" {
			named := false
			o.Visible = func(name string) string {
				if named || !wanted(name) {
					return ""
				}
				named = true
				return videoName + strings.ToLower(filepath.Ext(name))
			}
			o.VisibleAfter, o.OnVisible = visibleAfter, onVisible
		}
		return o
	}
	res, err := unpack.ExtractRARFS(ctx, job, first, folder, opts(""))
	if err == nil || !errors.Is(err, unpack.ErrPasswordRequired) {
		return res.Files, "", err
	}
	for _, pw := range src.all() {
		res, err = unpack.ExtractRARFS(ctx, job, first, folder, opts(pw))
		if err == nil {
			return res.Files, pw, nil
		}
		if !errors.Is(err, unpack.ErrWrongPassword) {
			return res.Files, "", err
		}
	}
	return nil, "", err
}

// wanted selects the files worth extracting: the movie, not samples.
func wanted(name string) bool { return IsVideo(name) && !IsSample(name) }

// spamMarker is returned by passwordsFromNFOs for spam releases.
const spamMarker = "\x00spam"

// passwordsFromNFOs reads the small text files of a release (downloaded
// first) and returns the passwords they mention.
func passwordsFromNFOs(job *download.Job) []string {
	var out []string
	for _, name := range job.Names() {
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".nfo" && ext != ".txt" {
			continue
		}
		f, err := job.Open(name)
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(f, 256<<10))
		_ = f.Close()
		text := string(data)
		if nzb.LooksLikeSpam(text) {
			out = append(out, spamMarker)
		}
		out = append(out, nzb.PasswordsFromText(text)...)
	}
	return out
}
