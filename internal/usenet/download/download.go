// Package download fetches all articles of an NZB in parallel and assembles
// them into files. While a download runs, its files can already be read
// through the Job's fs.FS: reads block until the data has arrived, so an
// archive can be extracted while it is still downloading.
package download

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

// ErrStalled is returned when no article arrived for Options.StallTimeout.
var ErrStalled = errors.New("download: no progress, stalled")

// maxConsecutiveServerErrors aborts a download when the servers keep failing
// for reasons other than missing articles (e.g. the provider is down).
const maxConsecutiveServerErrors = 50

// Options configure a download.
type Options struct {
	Pool *nntp.Pool
	// Dir receives the files; it is created if needed.
	Dir string
	// Workers is the number of articles fetched in parallel
	// (default: the pool's primary connections).
	Workers int
	// IncludeRecovery also downloads PAR2 recovery volumes (*.volNN+MM.par2).
	// They are only needed for repairs.
	IncludeRecovery bool
	// OnProgress is called about every ProgressInterval and once at the end.
	OnProgress       func(Progress)
	ProgressInterval time.Duration
	Log              *slog.Logger
	// StallTimeout aborts the download with ErrStalled when no article
	// arrives for this long (default 5 minutes).
	StallTimeout time.Duration
	// Limiter, if set, caps the download speed (shared by all downloads).
	Limiter *RateLimiter
}

// Progress counts articles by their encoded size as listed in the NZB.
type Progress struct {
	TotalBytes     int64         `json:"totalBytes"`
	DoneBytes      int64         `json:"doneBytes"`
	TotalSegments  int           `json:"totalSegments"`
	DoneSegments   int           `json:"doneSegments"` // including failed ones
	FailedSegments int           `json:"failedSegments"`
	FailedBytes    int64         `json:"failedBytes"`
	Elapsed        time.Duration `json:"elapsed"` // nanoseconds in JSON
}

// FileResult describes one file of the NZB.
type FileResult struct {
	// Index is the position of the file in the NZB.
	Index int
	// Name is the file name on disk.
	Name     string
	Path     string
	Size     int64
	Segments int
	// Missing counts articles that were missing or corrupt on all servers.
	Missing int
	// MissingBytes is the encoded size of the missing articles and Gaps the
	// number of separate missing ranges; both size a PAR2 repair.
	MissingBytes int64
	Gaps         int
	// Skipped is set for PAR2 recovery volumes that were not downloaded.
	Skipped bool
}

// Result is the outcome of a download.
type Result struct {
	Files    []FileResult
	Progress Progress
}

// Complete reports whether every downloaded file has all its articles.
func (r Result) Complete() bool {
	for _, f := range r.Files {
		if !f.Skipped && f.Missing > 0 {
			return false
		}
	}
	return true
}

var recoveryPar2 = regexp.MustCompile(`(?i)\.vol\d+[+-]\d+\.par2$`)

// IsRecoveryPar2 reports whether name is a PAR2 recovery volume.
func IsRecoveryPar2(name string) bool { return recoveryPar2.MatchString(name) }

// Job is a running download.
type Job struct {
	opts  Options
	ctx   context.Context
	stop  context.CancelCauseFunc
	start time.Time
	files []*fileState
	order []*fileState

	// mu guards the file states, progress and the flags below; cond is
	// signalled whenever data arrives, a file gets its name or the job ends.
	mu       sync.Mutex
	cond     *sync.Cond
	progress Progress
	ended    bool
	used     map[string]bool // claimed names, lower case

	serverErrors int
	lastProgress time.Time
	workers      sync.WaitGroup
	reporter     sync.WaitGroup
	stopReporter chan struct{}

	waitOnce sync.Once
	result   Result
	err      error
}

type fileState struct {
	index   int
	nzb     nzb.File
	skipped bool

	// Guarded by Job.mu.
	name         string
	f            *os.File
	size         int64 // from yEnc; the NZB only knows encoded sizes
	missing      int
	missingBytes int64
	seg          []segState // by segment index
	prefix       int        // segments [0, prefix) are contiguous on disk
	// avail is the number of bytes from the start of the file that are on disk.
	avail int64
	gap   bool // parts do not line up; streaming impossible
}

type segState struct {
	done, failed bool
	begin, end   int64
}

type job struct {
	file  *fileState
	index int
}

// Run downloads n and waits for the result.
func Run(ctx context.Context, n *nzb.NZB, opts Options) (Result, error) {
	j, err := Start(ctx, n, opts)
	if err != nil {
		return Result{}, err
	}
	return j.Wait()
}

// Start begins downloading n into opts.Dir and returns immediately.
// Missing articles do not stop the download; they are reported per file.
func Start(ctx context.Context, n *nzb.NZB, opts Options) (*Job, error) {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Workers <= 0 {
		opts.Workers = opts.Pool.Connections()
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = time.Second
	}
	if opts.StallTimeout <= 0 {
		opts.StallTimeout = 5 * time.Minute
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create download dir: %w", err)
	}

	j := &Job{opts: opts, start: time.Now(), lastProgress: time.Now(), used: make(map[string]bool), stopReporter: make(chan struct{})}
	j.cond = sync.NewCond(&j.mu)
	j.ctx, j.stop = context.WithCancelCause(ctx)

	for i, f := range n.Files {
		fs := &fileState{
			index:   i,
			nzb:     f,
			skipped: !opts.IncludeRecovery && IsRecoveryPar2(f.Filename),
			seg:     make([]segState, len(f.Segments)),
		}
		// Names from the subject are fixed up front so that the files can
		// be opened for streaming before their first article arrives.
		if s := sanitize(f.Filename); s != "" {
			fs.name = j.claim(s)
		}
		j.files = append(j.files, fs)
		if !fs.skipped {
			j.progress.TotalBytes += f.Size()
			j.progress.TotalSegments += len(f.Segments)
		}
	}
	j.order = downloadOrder(j.files)

	// Wake blocked readers when the job is cancelled.
	context.AfterFunc(j.ctx, func() {
		j.mu.Lock()
		j.cond.Broadcast()
		j.mu.Unlock()
	})

	jobs := make(chan job)
	go func() {
		defer close(jobs)
		for _, fs := range j.order {
			for i := range fs.nzb.Segments {
				select {
				case jobs <- job{file: fs, index: i}:
				case <-j.ctx.Done():
					return
				}
			}
		}
	}()

	j.reporter.Go(j.watchdog)
	if opts.OnProgress != nil {
		j.reporter.Go(func() {
			t := time.NewTicker(opts.ProgressInterval)
			defer t.Stop()
			for {
				select {
				case <-j.stopReporter:
					return
				case <-t.C:
					opts.OnProgress(j.Progress())
				}
			}
		})
	}
	for range opts.Workers {
		j.workers.Go(func() { j.work(jobs) })
	}
	return j, nil
}

// watchdog stops the job when articles stop arriving, e.g. because the
// connection hangs without an error.
func (j *Job) watchdog() {
	t := time.NewTicker(min(j.opts.StallTimeout/4, 30*time.Second))
	defer t.Stop()
	for {
		select {
		case <-j.stopReporter:
			return
		case <-t.C:
			j.mu.Lock()
			stalled := time.Since(j.lastProgress) > j.opts.StallTimeout
			j.mu.Unlock()
			if stalled {
				j.stop(ErrStalled)
				return
			}
		}
	}
}

// Wait blocks until the download has finished and returns its result. An
// error means the download could not run to the end (no usable server,
// cancelled, disk errors); missing articles are reported in the result.
func (j *Job) Wait() (Result, error) {
	j.waitOnce.Do(func() {
		j.workers.Wait()
		close(j.stopReporter)
		j.reporter.Wait()

		j.mu.Lock()
		res := Result{Progress: j.snapshot()}
		var closeErr error
		for _, fs := range j.files {
			fr, err := fs.finish(j.opts.Dir)
			closeErr = errors.Join(closeErr, err)
			res.Files = append(res.Files, fr)
		}
		j.ended = true
		j.cond.Broadcast()
		j.mu.Unlock()

		if j.opts.OnProgress != nil {
			j.opts.OnProgress(res.Progress)
		}
		j.result = res
		switch cause := context.Cause(j.ctx); {
		case cause != nil && !errors.Is(cause, context.Canceled):
			j.err = cause
		case j.ctx.Err() != nil:
			j.err = j.ctx.Err()
		default:
			j.err = closeErr
		}
		j.stop(nil)
	})
	return j.result, j.err
}

// Cancel stops the download.
func (j *Job) Cancel() { j.stop(context.Canceled) }

// Progress returns the current progress.
func (j *Job) Progress() Progress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshot()
}

// Names returns the file names known so far, in download order. Files whose
// subject carries no name get theirs from the first article.
func (j *Job) Names() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var names []string
	for _, fs := range j.order {
		if fs.name != "" {
			names = append(names, fs.name)
		}
	}
	return names
}

func (j *Job) snapshot() Progress {
	p := j.progress
	p.Elapsed = time.Since(j.start)
	return p
}

// claim reserves a unique file name. The caller holds j.mu or is Start.
func (j *Job) claim(name string) string {
	candidate := name
	ext := filepath.Ext(name)
	for i := 2; j.used[strings.ToLower(candidate)]; i++ {
		candidate = fmt.Sprintf("%s.%d%s", strings.TrimSuffix(name, ext), i, ext)
	}
	j.used[strings.ToLower(candidate)] = true
	return candidate
}

func (j *Job) work(jobs <-chan job) {
	dec := yenc.NewDecoder()
	for jb := range jobs {
		seg := jb.file.nzb.Segments[jb.index]
		if j.opts.Limiter != nil {
			if err := j.opts.Limiter.Wait(j.ctx, seg.Bytes); err != nil {
				return
			}
		}
		err := j.opts.Pool.Body(j.ctx, seg.MessageID, dec)
		if j.ctx.Err() != nil {
			return
		}
		var articleErr *nntp.ArticleError
		switch {
		case err == nil:
			if werr := j.write(jb, dec.Part()); werr != nil {
				j.stop(werr)
				return
			}
		case errors.Is(err, nntp.ErrNoServers), !j.opts.Pool.Usable():
			j.stop(fmt.Errorf("download stopped: %w", errors.Join(append([]error{err}, j.opts.Pool.Errors()...)...)))
			return
		case errors.Is(err, nntp.ErrNoSuchArticle), errors.As(err, &articleErr):
			j.opts.Log.Debug("article unavailable", "message_id", seg.MessageID, "err", err)
			j.failed(jb, false)
		default:
			j.opts.Log.Warn("article failed", "message_id", seg.MessageID, "err", err)
			if j.failed(jb, true) >= maxConsecutiveServerErrors {
				j.stop(fmt.Errorf("download stopped after %d server errors in a row: %w", maxConsecutiveServerErrors, err))
				return
			}
		}
	}
}

// write stores a decoded part at its offset, creating the file on first use.
func (j *Job) write(jb job, p yenc.Part) error {
	fs := jb.file
	j.mu.Lock()
	if fs.f == nil {
		if fs.name == "" {
			fs.name = j.claim(chooseName(p.Name, fs.index))
			j.cond.Broadcast()
		}
		f, err := os.OpenFile(filepath.Join(j.opts.Dir, fs.name), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
		if err != nil {
			j.mu.Unlock()
			return fmt.Errorf("create %s: %w", fs.name, err)
		}
		fs.f, fs.size = f, p.FileSize
	}
	f, name := fs.f, fs.name
	j.mu.Unlock()

	if _, err := f.WriteAt(p.Data, p.Begin); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	fs.seg[jb.index] = segState{done: true, begin: p.Begin, end: p.Begin + int64(len(p.Data))}
	fs.advance()
	j.serverErrors = 0
	j.lastProgress = time.Now()
	j.count(jb, false)
	j.cond.Broadcast()
	return nil
}

// failed records a lost article and returns the number of consecutive
// server errors.
func (j *Job) failed(jb job, serverError bool) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	jb.file.seg[jb.index].failed = true
	jb.file.missing++
	jb.file.missingBytes += jb.file.nzb.Segments[jb.index].Bytes
	j.lastProgress = time.Now()
	if serverError {
		j.serverErrors++
	} else {
		j.serverErrors = 0
	}
	j.count(jb, true)
	j.cond.Broadcast()
	return j.serverErrors
}

func (j *Job) count(jb job, failed bool) {
	j.progress.DoneSegments++
	j.progress.DoneBytes += jb.file.nzb.Segments[jb.index].Bytes
	if failed {
		j.progress.FailedSegments++
		j.progress.FailedBytes += jb.file.nzb.Segments[jb.index].Bytes
	}
}

// advance extends the contiguous prefix after a part has been written.
func (fs *fileState) advance() {
	for fs.prefix < len(fs.seg) && fs.seg[fs.prefix].done {
		s := fs.seg[fs.prefix]
		if s.begin != fs.avail {
			fs.gap = true
			return
		}
		fs.avail = s.end
		fs.prefix++
	}
}

func (fs *fileState) finish(dir string) (FileResult, error) {
	fr := FileResult{Index: fs.index, Name: fs.name, Segments: len(fs.nzb.Segments), Missing: fs.missing,
		MissingBytes: fs.missingBytes, Skipped: fs.skipped}
	inGap := false
	for _, sg := range fs.seg {
		if sg.failed && !inGap {
			fr.Gaps++
		}
		inGap = sg.failed
	}
	if fs.f == nil {
		if !fs.skipped {
			fr.Missing = fr.Segments
			fr.MissingBytes = fs.nzb.Size()
			fr.Gaps = 1
		}
		return fr, nil
	}
	fr.Path = filepath.Join(dir, fs.name)
	var err error
	if fs.size > 0 {
		// Parts beyond the last received one may be missing; give the file
		// its real size so that PAR2 can repair it later.
		err = fs.f.Truncate(fs.size)
	}
	if info, statErr := fs.f.Stat(); statErr == nil {
		fr.Size = info.Size()
	}
	return fr, errors.Join(err, fs.f.Close())
}

// downloadOrder puts the small PAR2 index first (it identifies obfuscated
// files and is needed for repairs) and small text files (NFOs may name the
// archive password), then archive volumes in volume order so that
// extraction can follow the download, then everything else.
func downloadOrder(files []*fileState) []*fileState {
	var par2, volumes, rest []*fileState
	for _, fs := range files {
		ext := strings.ToLower(filepath.Ext(fs.name))
		switch {
		case fs.skipped:
		case ext == ".par2", ext == ".nfo", ext == ".txt", ext == ".sfv":
			par2 = append(par2, fs)
		default:
			if _, _, ok := volumeKey(fs.name); ok {
				volumes = append(volumes, fs)
			} else {
				rest = append(rest, fs)
			}
		}
	}
	slices.SortStableFunc(volumes, func(a, b *fileState) int {
		ab, ai, _ := volumeKey(a.name)
		bb, bi, _ := volumeKey(b.name)
		if c := strings.Compare(ab, bb); c != 0 {
			return c
		}
		return ai - bi
	})
	return slices.Concat(par2, volumes, rest)
}

var (
	newStyleVolume = regexp.MustCompile(`(?i)^(.*)\.part(\d+)\.rar$`)
	oldStyleVolume = regexp.MustCompile(`(?i)^(.*)\.(?:rar|r(\d{2,3}))$`)
	splitVolume    = regexp.MustCompile(`^(.*)\.(\d{3})$`)
)

// volumeKey returns the archive base name and volume index of a volume file
// name: x.part01.rar → (x, 1); x.rar → (x, 0), x.r00 → (x, 1); x.001 → (x, 1).
func volumeKey(name string) (string, int, bool) {
	if m := newStyleVolume.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[2])
		return strings.ToLower(m[1]), n, true
	}
	if m := oldStyleVolume.FindStringSubmatch(name); m != nil {
		if m[2] == "" {
			return strings.ToLower(m[1]), 0, true
		}
		n, _ := strconv.Atoi(m[2])
		return strings.ToLower(m[1]), n + 1, true
	}
	if m := splitVolume.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[2])
		return strings.ToLower(m[1]), n, true
	}
	return "", 0, false
}

// chooseName names a file whose subject has no usable name.
func chooseName(yencName string, index int) string {
	if s := sanitize(yencName); s != "" {
		return s
	}
	return fmt.Sprintf("file-%03d", index+1)
}

// sanitize turns a posted name into a safe base name.
func sanitize(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':':
			return '_'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = name[:200-len(ext)] + ext
	}
	return name
}
