package movies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/download"
)

// Reasons for cancelling a running download.
var (
	errSkipped    = errors.New("skipped by user")
	errSuperseded = errors.New("replaced by another release")
	errDeleted    = errors.New("movie deleted")
)

// Kinds of release problems; the release is blocklisted and the next one
// is tried. Most come from the fetch package.
const (
	kindIncomplete     = fetch.KindIncomplete
	kindSpam           = fetch.KindSpam
	kindNZBUnavailable = "nzb_unavailable"
	kindSkipped        = "skipped"
	kindCancelled      = fetch.KindCancelled
)

type outcome struct {
	kind  string // "" on success
	err   error
	video string // final path of the movie file on success
}

func (m *Manager) cancelDownload(id int64, cause error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.active[id]
	if ok {
		a.cancel(cause)
	}
	return ok
}

func (m *Manager) runDownload(ctx context.Context, mv store.Movie, rel store.Release) {
	defer func() {
		m.mu.Lock()
		delete(m.active, mv.ID)
		m.mu.Unlock()
		m.startQueued()
	}()
	bg := context.WithoutCancel(ctx)

	attemptID, err := m.store.StartAttempt(bg, mv.ID, rel)
	if err != nil {
		m.log.Error("record attempt", "err", err)
		return
	}
	mv.State, mv.ReleaseTitle = store.MovieDownloading, rel.Title
	if err := m.store.UpdateMovie(bg, mv); err != nil {
		m.log.Error("update movie", "err", err)
		return
	}
	m.publish(mv)
	m.log.Info("download started", "movie", mv.Title, "release", rel.Title)

	out := m.download(ctx, mv, rel)
	if err := m.finish(bg, ctx, mv, rel, attemptID, out); err != nil {
		m.log.Error("finish download", "movie", mv.Title, "err", err)
	}
}

// download fetches the NZB, downloads it and extracts it while it arrives.
func (m *Manager) download(ctx context.Context, mv store.Movie, rel store.Release) outcome {
	g, err := m.store.General(ctx)
	if err != nil {
		return outcome{kind: CodeDiskError, err: err}
	}
	if g.MoviesPath == "" {
		return outcome{kind: CodeNoMoviesPath}
	}
	pool, err := m.svc.Pool(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return outcome{kind: CodeNoUsenet}
	} else if err != nil {
		return outcome{kind: CodeDownloadError, err: err}
	}

	ic, err := m.svc.Indexer(ctx, rel.IndexerID)
	if err != nil {
		return outcome{kind: kindNZBUnavailable, err: err}
	}
	data, nzbName, err := ic.Client.FetchNZB(ctx, rel.NZBURL)
	switch {
	case ctx.Err() != nil:
		return outcome{kind: kindCancelled, err: context.Cause(ctx)}
	case errors.Is(err, httpx.ErrRateLimited), errors.Is(err, httpx.ErrUnavailable), errors.Is(err, httpx.ErrUnauthorized):
		return outcome{kind: CodeIndexerError, err: err}
	case err != nil:
		return outcome{kind: kindNZBUnavailable, err: err}
	}
	list, err := m.store.ArchivePasswords(ctx)
	if err != nil {
		return outcome{kind: CodeDiskError, err: err}
	}
	passwords := make([]string, len(list))
	for i, p := range list {
		passwords[i] = p.Password
	}
	m.limiter.SetRate(int64(g.MaxDownloadMBps * (1 << 20)))

	root := g.MoviesPath
	folder := filepath.Join(root, FolderName(mv))
	// Plex learns about the movie as soon as the first part is extracted,
	// so that it can be watched while the rest is still downloading.
	var visible atomic.Bool
	res, err := m.fetcher.Fetch(ctx, fetch.Request{
		Pool:         pool,
		NZB:          data,
		NZBName:      nzbName,
		ReleaseTitle: rel.Title,
		Root:         root,
		WorkDir:      filepath.Join(root, fetch.WorkDirName, "incomplete", strconv.FormatInt(mv.ID, 10)),
		Folder:       folder,
		VideoName:    FolderName(mv),
		FullCheck:    m.svc.FullCheck(ctx),
		OnVisible: func(path string) {
			visible.Store(true)
			m.log.Info("movie playable while downloading", "movie", mv.Title, "file", path)
			go m.scan(context.WithoutCancel(ctx), folder)
		},
		Passwords:  passwords,
		Limiter:    m.limiter,
		OnProgress: func(p download.Progress) { m.progress(mv.ID, p) },
	})
	if err != nil && visible.Load() {
		m.scan(context.WithoutCancel(ctx), root) // the file is gone again
	}
	var fe *fetch.Error
	if errors.As(err, &fe) {
		return outcome{kind: fe.Kind, err: fe.Err}
	} else if err != nil {
		return outcome{kind: CodeDiskError, err: err}
	}
	if res.Password != "" {
		_ = m.store.ArchivePasswordHit(ctx, res.Password)
	}

	main := fetch.LargestVideo(res.Videos)
	final := filepath.Join(folder, FolderName(mv)+strings.ToLower(filepath.Ext(main)))
	if main != final {
		if err := os.Rename(main, final); err != nil {
			fetch.Cleanup("", folder, res.Videos)
			return outcome{kind: CodeDiskError, err: err}
		}
	}
	for _, f := range res.Videos {
		if f != main {
			_ = os.Remove(f) // extras
		}
	}
	return outcome{video: final}
}

// finish records the outcome and decides what happens next.
func (m *Manager) finish(ctx, dctx context.Context, mv store.Movie, rel store.Release, attemptID int64, out outcome) error {
	msg := ""
	if out.err != nil {
		msg = out.err.Error()
	}
	// Reload: the user may have changed the movie (keep, delete) meanwhile.
	cur, err := m.store.Movie(ctx, mv.ID)
	if errors.Is(err, store.ErrNotFound) {
		return m.store.FinishAttempt(ctx, attemptID, "cancelled", kindCancelled, "movie removed")
	}
	if err != nil {
		return err
	}
	mv = cur

	switch out.kind {
	case "":
		if err := m.store.FinishAttempt(ctx, attemptID, "success", "", ""); err != nil {
			return err
		}
		return m.available(ctx, mv, rel, out.video)

	case kindCancelled:
		cause := context.Cause(dctx)
		if errors.Is(cause, errSkipped) {
			out.kind = kindSkipped
			break // treated like a broken release below
		}
		if err := m.store.FinishAttempt(ctx, attemptID, "cancelled", kindCancelled, msg); err != nil {
			return err
		}
		return nil // deleted, superseded or shutting down: the caller takes over

	case CodeNoMoviesPath, CodeNoUsenet, CodeUsenetAuth, CodeDiskError:
		if err := m.store.FinishAttempt(ctx, attemptID, "failed", out.kind, msg); err != nil {
			return err
		}
		return m.fail(ctx, mv, out.kind, msg)

	case CodeDownloadError, CodeIndexerError:
		if err := m.store.FinishAttempt(ctx, attemptID, "failed", out.kind, msg); err != nil {
			return err
		}
		return m.wait(ctx, mv, 0, out.kind, msg)
	}

	// The release itself is broken: blocklist it and try the next one.
	m.log.Warn("release failed", "movie", mv.Title, "release", rel.Title, "kind", out.kind, "err", out.err)
	if err := m.store.FinishAttempt(ctx, attemptID, "failed", out.kind, msg); err != nil {
		return err
	}
	if err := m.store.Blocklist(ctx, store.BlocklistEntry{
		MovieID: mv.ID, Fingerprint: rel.Fingerprint, Title: rel.Title, Reason: out.kind, Message: msg,
	}); err != nil {
		return err
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	mv.Attempts++
	if mv.Attempts >= max(g.MaxSources, 1) {
		return m.fail(ctx, mv, CodeSourcesExhausted, strconv.Itoa(mv.Attempts))
	}
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	if _, ok, err := m.bestRelease(ctx, mv.ID); err != nil {
		return err
	} else if ok {
		return m.enqueue(ctx, mv)
	}
	// Every stored release is used up; fresh results may exist.
	mv.State = store.MovieSearching
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.publish(mv)
	return m.scheduleSearch(ctx, mv.ID, 1, m.now())
}

func (m *Manager) progress(movieID int64, p download.Progress) {
	m.mu.Lock()
	if a, ok := m.active[movieID]; ok {
		a.progress = p
	}
	m.mu.Unlock()
	m.bus.Publish(EventDownloadProgress, Download{MovieID: movieID, Progress: p})
}

// FolderName is the library folder of a movie, with the TMDB ID as a hint
// for Plex and Jellyfin: "Dune Part Two (2024) {tmdb-693134}".
func FolderName(mv store.Movie) string {
	name := mv.Title
	if mv.Year > 0 {
		name += " (" + strconv.Itoa(mv.Year) + ")"
	}
	name += " {tmdb-" + strconv.Itoa(mv.TMDBID) + "}"
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return -1
		}
		if r < 32 {
			return -1
		}
		return r
	}, name)
}
