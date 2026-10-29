// Package fetch turns an NZB into video files in a library folder: it
// downloads the release, extracts archives while they download, falls back
// to a PAR2 repair and extraction from disk when that is not possible, and
// classifies failures (docs/PLAN.md sections 6, 7 and 9). Movies and series
// episodes share it.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/unpack"
)

// Kinds of failures. Release problems (the release is broken) are
// distinguished from problems of the setup or the connection.
const (
	// Release problems: blocklist the release and try the next one.
	KindIncomplete = "incomplete"
	KindPassword   = "password"
	KindUnpack     = "unpack"
	KindNoVideo    = "no_video"
	KindInvalidNZB = "invalid_nzb"
	KindSpam       = "spam"

	// Not the release's fault.
	KindCancelled     = "cancelled"
	KindUsenetAuth    = "usenet_auth"
	KindDownloadError = "download_error"
	KindDiskError     = "disk_error"
)

// WorkDirName is the hidden folder inside a library that holds incomplete
// downloads. Plex and Jellyfin are told to ignore it.
const WorkDirName = ".autodownloadarr"

// visibleAfter is how much of the video must be extracted before the media
// server is told about it: enough for the container headers. It is a var so
// tests can lower it.
var visibleAfter int64 = 32 << 20

// Pre-check: articles sampled with STAT before downloading, and the share
// that may be missing (a repair can still fix a few).
const (
	precheckSample     = 20
	precheckMaxMissing = 0.15
)

// errBeyondRepair cancels a download that lost more data than its recovery
// volumes can restore.
var errBeyondRepair = errors.New("more data missing than recoverable")

// Error is a classified failure.
type Error struct {
	Kind string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Kind
	}
	return e.Kind + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// IsReleaseProblem reports whether the release itself is broken, so that it
// should be blocklisted.
func IsReleaseProblem(kind string) bool {
	switch kind {
	case KindIncomplete, KindPassword, KindUnpack, KindNoVideo, KindInvalidNZB, KindSpam:
		return true
	}
	return false
}

func fail(kind string, err error) *Error { return &Error{Kind: kind, Err: err} }

// Request describes one download.
type Request struct {
	Pool *nntp.Pool
	NZB  []byte
	// NZBName is the file name the indexer sent ("name{{password}}.nzb").
	NZBName      string
	ReleaseTitle string
	// Root is the library root, WorkDir the folder for the raw download
	// (below Root/WorkDirName) and Folder where the videos end up.
	Root, WorkDir, Folder string
	// VideoName is the final file name of the video, without extension.
	// While streaming, the video is extracted under this name right away
	// and OnVisible is called once a media server can read it, so that
	// playback can start before the download is complete.
	VideoName string
	OnVisible func(path string)
	// FullCheck asks for every article before downloading instead of a
	// sample, and skips releases that cannot be repaired.
	FullCheck bool
	// Passwords from the password list, tried after the release's own.
	Passwords  []string
	Limiter    *download.RateLimiter
	OnProgress func(download.Progress)
}

// Result lists the extracted video files (samples excluded) and the archive
// password that worked, if any.
type Result struct {
	Videos   []string
	Password string
}

// Fetcher runs downloads.
type Fetcher struct {
	Log *slog.Logger
}

// Fetch downloads and extracts a release. On failure everything it created
// is removed again and the error is an *Error.
func (f *Fetcher) Fetch(ctx context.Context, req Request) (Result, error) {
	n, err := nzb.Parse(bytes.NewReader(req.NZB))
	if err != nil {
		return Result{}, fail(KindInvalidNZB, err)
	}
	for _, file := range n.Files {
		if nzb.LooksLikeSpam(file.Subject) {
			return Result{}, fail(KindSpam, fmt.Errorf("subject points to a web site for the password: %q", file.Subject))
		}
	}
	segments, named := 0, 0
	for _, file := range n.Files {
		segments += len(file.Segments)
		if file.Filename != "" {
			named++
		}
	}
	f.Log.Debug("nzb parsed", "release", req.ReleaseTitle, "files", len(n.Files), "segments", segments,
		"size", formatSize(n.Size()), "named_files", named)

	// Ask the servers whether the articles exist before downloading: every
	// one of them, or a sample (a release that is mostly gone is skipped in
	// seconds).
	if req.FullCheck {
		f.Log.Debug("checking availability", "release", req.ReleaseTitle, "full", true)
		res, err := download.FullCheck(ctx, req.Pool, n, req.Pool.Connections(), func(done, total int) {
			if req.OnProgress != nil {
				req.OnProgress(download.Progress{Checking: true, Checked: done, CheckTotal: total})
			}
		})
		switch {
		case ctx.Err() != nil:
			return Result{}, fail(KindCancelled, context.Cause(ctx))
		case errors.Is(err, nntp.ErrNoServers) || errors.Is(err, nntp.ErrAuthFailed):
			return Result{}, fail(KindUsenetAuth, err)
		case err != nil:
			return Result{}, fail(KindDownloadError, fmt.Errorf("availability check: %w", err))
		case !res.Repairable():
			return Result{}, fail(KindIncomplete, fmt.Errorf("%d of %d articles missing (%s), recovery data only %s",
				res.Missing, res.Segments, formatSize(res.MissingBytes), formatSize(res.RecoveryBytes)))
		case res.Missing > 0:
			f.Log.Info("articles missing, a repair will be needed", "release", req.ReleaseTitle, "missing", res.Missing,
				"segments", res.Segments)
		}
	} else if checked, missing, err := download.Precheck(ctx, req.Pool, n, precheckSample); err == nil && checked > 0 &&
		float64(missing) > float64(checked)*precheckMaxMissing {
		return Result{}, fail(KindIncomplete, fmt.Errorf("%d of %d sampled articles missing", missing, checked))
	}

	if err := prepareDirs(req.Root, req.WorkDir, req.Folder); err != nil {
		return Result{}, fail(KindDiskError, err)
	}

	// Abort early when more data is missing than the recovery volumes can
	// restore.
	recoveryBytes := int64(0)
	for _, file := range n.Files {
		if download.IsRecoveryPar2(file.Filename) {
			recoveryBytes += file.Size()
		}
	}
	jctx, jcancel := context.WithCancelCause(ctx)
	defer jcancel(nil)
	lastProgressLog := time.Now()
	job, err := download.Start(jctx, n, download.Options{
		Pool:    req.Pool,
		Dir:     req.WorkDir,
		Log:     f.Log,
		Limiter: req.Limiter,
		OnProgress: func(p download.Progress) {
			if p.FailedBytes > recoveryBytes {
				jcancel(errBeyondRepair)
			}
			if req.OnProgress != nil {
				req.OnProgress(p)
			}
			if p.Checking || time.Since(lastProgressLog) < 30*time.Second {
				return
			}
			lastProgressLog = time.Now()
			f.Log.Debug("download progress", "release", req.ReleaseTitle,
				"done", formatSize(p.DoneBytes), "total", formatSize(p.TotalBytes),
				"failed_segments", p.FailedSegments, "elapsed", p.Elapsed.Round(time.Second))
		},
	})
	if err != nil {
		return Result{}, fail(KindDiskError, err)
	}

	src := passwordSource{known: candidates(n, req), nfo: func() []string { return passwordsFromNFOs(job) }}
	var extracted []string
	var password string
	var exErr, videoErr error
	handled := make(map[string]bool)
	name := req.VideoName
	streamable := false
	for _, known := range job.Names() {
		if unpack.IsFirstVolume(known) || wanted(known) {
			streamable = true
			break
		}
	}
	if !streamable {
		f.Log.Debug("no RAR volume or video known yet, waiting for yEnc names", "release", req.ReleaseTitle)
	}
	for {
		names := job.WaitForNames(jctx, func(name string) bool {
			return !handled[name] && (unpack.IsFirstVolume(name) || wanted(name))
		})
		if len(names) == 0 {
			break
		}
		for _, file := range names {
			handled[file] = true
		}
		for _, file := range names {
			if unpack.IsFirstVolume(file) {
				f.Log.Debug("extracting rar while downloading", "release", req.ReleaseTitle, "volume", file)
				files, pw, err := extract(jctx, job, file, req.Folder, &src, name, req.OnVisible)
				for _, out := range files {
					if name != "" && strings.TrimSuffix(out, filepath.Ext(out)) == name {
						name = "" // only one file gets the name
					}
					extracted = append(extracted, filepath.Join(req.Folder, out))
				}
				if err != nil {
					exErr = err
					if !errors.Is(err, download.ErrDataMissing) {
						job.Cancel() // a repair cannot fix a password or a broken archive
					}
					break
				}
				if pw != "" {
					password = pw
				}
				continue
			}
			target := filepath.Base(file)
			if name != "" {
				target = name + strings.ToLower(filepath.Ext(target))
			}
			f.Log.Debug("streaming video while downloading", "release", req.ReleaseTitle, "file", file, "target", target)
			out, err := streamVideo(jctx, job, file, req.Folder, target, req.OnVisible)
			if err != nil {
				videoErr = err
				if !errors.Is(err, download.ErrDataMissing) {
					job.Cancel()
				}
				break
			}
			if name != "" {
				name = "" // only one file gets the name
			}
			extracted = append(extracted, filepath.Join(req.Folder, out))
		}
		if exErr != nil || videoErr != nil {
			break
		}
	}
	res, dlErr := job.Wait()

	failed := func(kind string, err error) (Result, error) {
		Cleanup(req.WorkDir, req.Folder, extracted)
		return Result{}, fail(kind, err)
	}
	switch {
	case ctx.Err() != nil:
		return failed(KindCancelled, context.Cause(ctx))
	case errors.Is(context.Cause(jctx), errBeyondRepair):
		return failed(KindIncomplete, fmt.Errorf("%s missing, more than the recovery data can restore", formatSize(res.Progress.FailedBytes)))
	case src.spam:
		return failed(KindSpam, errors.New("the NFO points to a web site for the password"))
	case dlErr != nil && (errors.Is(dlErr, nntp.ErrNoServers) || errors.Is(dlErr, nntp.ErrAuthFailed)):
		return failed(KindUsenetAuth, dlErr)
	case dlErr != nil && !errors.Is(dlErr, context.Canceled):
		return failed(KindDownloadError, dlErr)
	case exErr != nil && (errors.Is(exErr, unpack.ErrPasswordRequired) || errors.Is(exErr, unpack.ErrWrongPassword)):
		return failed(KindPassword, exErr)
	case exErr != nil && !errors.Is(exErr, download.ErrDataMissing):
		return failed(KindUnpack, exErr)
	case videoErr != nil && !errors.Is(videoErr, download.ErrDataMissing):
		return failed(KindDiskError, videoErr)
	}

	if len(handled) == 0 || exErr != nil || videoErr != nil {
		// Streaming was not possible (missing articles, 7z/ZIP or no
		// archive/video at all): repair if needed, then extract from disk.
		switch {
		case exErr != nil:
			f.Log.Info("streaming extraction failed, extracting after download", "release", req.ReleaseTitle, "err", exErr)
		case videoErr != nil:
			f.Log.Info("streaming video failed, extracting after download", "release", req.ReleaseTitle, "err", videoErr)
		default:
			f.Log.Info("nothing to stream while downloading, extracting after download", "release", req.ReleaseTitle,
				"files", len(res.Files), "failed_segments", res.Progress.FailedSegments)
		}
		files, pw, kind, err := f.repairAndExtract(ctx, req, n, res, &src)
		extracted = append(extracted, files...)
		if kind == KindPassword && src.spam {
			kind, err = KindSpam, errors.New("the NFO points to a web site for the password")
		}
		if kind != "" {
			return failed(kind, err)
		}
		if pw != "" {
			password = pw
		}
		f.Log.Debug("extraction after download finished", "release", req.ReleaseTitle, "files", len(files))
	}

	var videos []string
	seen := make(map[string]bool)
	for _, file := range extracted {
		if !wanted(file) {
			_ = os.Remove(file)
			continue
		}
		if !seen[file] {
			seen[file] = true
			videos = append(videos, file)
		}
	}
	if len(videos) == 0 {
		return failed(KindNoVideo, errors.New("the release contains no video file"))
	}
	_ = os.RemoveAll(req.WorkDir)
	return Result{Videos: videos, Password: password}, nil
}

// candidates collects archive passwords from the NZB, its file name, the
// release title, the subjects and the password list.
func candidates(n *nzb.NZB, req Request) []string {
	var out []string
	add := func(pw string) {
		if pw != "" && !containsString(out, pw) {
			out = append(out, pw)
		}
	}
	for _, pw := range n.Passwords() {
		add(pw)
	}
	add(nzb.PasswordFromFilename(req.NZBName))
	add(nzb.PasswordFromFilename(req.ReleaseTitle))
	for _, pw := range n.PasswordsFromSubjects() {
		add(pw)
	}
	for _, pw := range req.Passwords {
		add(pw)
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func formatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KiB", n>>10)
	}
}
