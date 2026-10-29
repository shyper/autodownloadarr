package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/unpack"
)

const downloadUsage = `Usage: autodownloadarr download-nzb [flags] <file.nzb>

Downloads an NZB with the built-in Usenet client and extracts RAR archives
while they download. Meant for testing the downloader on its own.

The password is read from AUTODOWNLOADARR_NNTP_PASSWORD so that it does not
end up in the shell history.

Flags:
`

// downloadNZB implements the download-nzb command.
func downloadNZB(args []string) int {
	fl := flag.NewFlagSet("download-nzb", flag.ContinueOnError)
	server := fl.String("server", os.Getenv("AUTODOWNLOADARR_NNTP_SERVER"), "Usenet server `host[:port]` (env AUTODOWNLOADARR_NNTP_SERVER)")
	user := fl.String("user", os.Getenv("AUTODOWNLOADARR_NNTP_USER"), "username (env AUTODOWNLOADARR_NNTP_USER)")
	useTLS := fl.Bool("tls", true, "connect with TLS")
	insecure := fl.Bool("insecure", false, "skip TLS certificate verification")
	connections := fl.Int("connections", 20, "parallel connections")
	out := fl.String("out", "downloads", "output `dir`; a folder per NZB is created inside")
	archivePassword := fl.String("archive-password", "", "archive password (NZB metadata and {{password}} in the file name are tried too)")
	recovery := fl.Bool("par2-recovery", false, "also download PAR2 recovery volumes")
	noExtract := fl.Bool("no-extract", false, "do not extract archives")
	keep := fl.Bool("keep", false, "keep archives and PAR2 files after extracting")
	verbose := fl.Bool("v", false, "verbose logging")
	fl.Usage = func() {
		_, _ = fmt.Fprint(fl.Output(), downloadUsage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() != 1 || *server == "" {
		fl.Usage()
		return 2
	}
	nzbPath := fl.Arg(0)

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	f, err := os.Open(nzbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	n, err := nzb.Parse(f)
	_ = f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	addr := *server
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, map[bool]string{true: "563", false: "119"}[*useTLS])
	}
	pool, err := nntp.NewPool([]nntp.Config{{
		Address:            addr,
		TLS:                *useTLS,
		InsecureSkipVerify: *insecure,
		Username:           *user,
		Password:           os.Getenv("AUTODOWNLOADARR_NNTP_PASSWORD"),
		Connections:        *connections,
	}}, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer pool.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir := filepath.Join(*out, releaseName(nzbPath, n))
	fmt.Fprintf(os.Stderr, "%s: %d files, %s → %s\n", filepath.Base(nzbPath), len(n.Files), formatBytes(n.Size()), dir)

	meter := newSpeedMeter()
	job, err := download.Start(ctx, n, download.Options{
		Pool:            pool,
		Dir:             dir,
		IncludeRecovery: *recovery,
		Log:             log,
		OnProgress:      func(p download.Progress) { meter.print(p) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	passwords := passwordCandidates(*archivePassword, n, nzbPath)
	extracted := make(chan extraction, 1)
	if *noExtract {
		extracted <- extraction{}
	} else {
		go func() { extracted <- extractAll(ctx, job, dir, passwords) }()
	}

	res, dlErr := job.Wait()
	downloadDone := time.Now()
	meter.finish(res.Progress)
	ex := <-extracted

	missing := res.Progress.FailedSegments
	switch {
	case dlErr != nil:
		fmt.Fprintln(os.Stderr, "download failed:", dlErr)
	case missing > 0:
		fmt.Fprintf(os.Stderr, "download incomplete: %d of %d articles missing (PAR2 repair is not implemented yet)\n",
			missing, res.Progress.TotalSegments)
	default:
		fmt.Fprintln(os.Stderr, "download complete")
	}

	if ex.err != nil {
		fmt.Fprintln(os.Stderr, "extraction failed:", ex.err)
	} else if len(ex.files) > 0 {
		lag := ex.finished.Sub(downloadDone)
		fmt.Fprintf(os.Stderr, "extracted %d file(s), ready %s after the download finished:\n", len(ex.files), max(lag, 0).Round(10*time.Millisecond))
		for _, f := range ex.files {
			fmt.Fprintln(os.Stderr, "  "+filepath.Join(dir, f))
		}
		if !*keep {
			cleanUp(dir, res, ex.volumes)
		}
	}

	switch {
	case dlErr != nil || ex.err != nil:
		return 1
	case missing > 0:
		return 3
	}
	return 0
}

type extraction struct {
	files    []string
	volumes  []string
	finished time.Time
	err      error
}

// extractAll extracts every RAR archive of the job while it downloads.
func extractAll(ctx context.Context, job *download.Job, dir string, passwords []string) extraction {
	var ex extraction
	handled := make(map[string]bool)
	for {
		// Subjects of obfuscated releases often carry no usable name: wait
		// for the first volume's yEnc name before giving up on streaming.
		firsts := job.WaitForNames(ctx, func(name string) bool {
			return !handled[name] && unpack.IsFirstVolume(name)
		})
		if len(firsts) == 0 {
			break
		}
		for _, first := range firsts {
			handled[first] = true
			res, err := extractWithPasswords(ctx, job, first, dir, passwords)
			ex.files = append(ex.files, res.Files...)
			ex.volumes = append(ex.volumes, res.Volumes...)
			if err != nil {
				ex.err = fmt.Errorf("%s: %w", first, err)
				ex.finished = time.Now()
				return ex
			}
		}
	}
	ex.finished = time.Now()
	return ex
}

func extractWithPasswords(ctx context.Context, job *download.Job, first, dir string, passwords []string) (unpack.Result, error) {
	// A password is simply ignored for unencrypted archives.
	candidates := passwords
	if len(candidates) == 0 {
		candidates = []string{""}
	}
	var lastErr error
	for _, pw := range candidates {
		res, err := unpack.ExtractRARFS(ctx, job, first, dir, unpack.Options{Password: pw})
		if err == nil || !errors.Is(err, unpack.ErrPasswordRequired) && !errors.Is(err, unpack.ErrWrongPassword) {
			return res, err
		}
		lastErr = err
	}
	return unpack.Result{}, lastErr
}

// cleanUp removes the archive volumes and PAR2 files after a successful
// extraction, keeping the extracted files.
func cleanUp(dir string, res download.Result, volumes []string) {
	for _, v := range volumes {
		_ = os.Remove(filepath.Join(dir, v))
	}
	for _, f := range res.Files {
		if f.Path != "" && strings.EqualFold(filepath.Ext(f.Name), ".par2") {
			_ = os.Remove(f.Path)
		}
	}
}

func passwordCandidates(flagValue string, n *nzb.NZB, nzbPath string) []string {
	var out []string
	for _, pw := range append([]string{flagValue, nzb.PasswordFromFilename(filepath.Base(nzbPath))}, n.Passwords()...) {
		if pw != "" && !slices.Contains(out, pw) {
			out = append(out, pw)
		}
	}
	return out
}

// releaseName derives the folder name from the NZB title or file name.
func releaseName(path string, n *nzb.NZB) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if titles := n.Meta["title"]; len(titles) > 0 {
		name = titles[0]
	}
	if i := strings.Index(name, "{{"); i > 0 {
		name = name[:i]
	}
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < 32 {
			return '_'
		}
		return r
	}, name)
	if name = strings.Trim(name, " ."); name == "" {
		name = "download"
	}
	return name
}

// speedMeter prints a progress line with a speed averaged over a few seconds.
type speedMeter struct {
	samples []sample
}

type sample struct {
	at    time.Duration
	bytes int64
}

func newSpeedMeter() *speedMeter { return &speedMeter{} }

func (m *speedMeter) print(p download.Progress) {
	m.samples = append(m.samples, sample{p.Elapsed, p.DoneBytes})
	if len(m.samples) > 6 {
		m.samples = m.samples[1:]
	}
	var speed float64
	if first, last := m.samples[0], m.samples[len(m.samples)-1]; last.at > first.at {
		speed = float64(last.bytes-first.bytes) / (last.at - first.at).Seconds()
	}
	pct := 0.0
	if p.TotalBytes > 0 {
		pct = 100 * float64(p.DoneBytes) / float64(p.TotalBytes)
	}
	eta := "--:--"
	if speed > 0 {
		eta = formatDuration(time.Duration(float64(p.TotalBytes-p.DoneBytes) / speed * float64(time.Second)))
	}
	fmt.Fprintf(os.Stderr, "\r%5.1f%%  %s / %s  %s/s  ETA %s  missing %d   ",
		pct, formatBytes(p.DoneBytes), formatBytes(p.TotalBytes), formatBytes(int64(speed)), eta, p.FailedSegments)
}

func (m *speedMeter) finish(p download.Progress) {
	avg := 0.0
	if s := p.Elapsed.Seconds(); s > 0 {
		avg = float64(p.DoneBytes) / s
	}
	fmt.Fprintf(os.Stderr, "\r%s in %s, average %s/s%s\n",
		formatBytes(p.DoneBytes), formatDuration(p.Elapsed), formatBytes(int64(avg)), strings.Repeat(" ", 30))
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
