package movies

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/scheduler"
)

// media server scans that fail are retried by a task. Until one succeeds again the
// failure is listed as a warning in the UI.

const taskMediaScan = "media.scan"

// scanAttempts bounds the retries of a failed scan (with the scheduler's
// backoff that is roughly an hour).
const scanAttempts = 6

// scanRetryDelay is the wait before the first retry (shorter in tests).
var scanRetryDelay = time.Minute

type scanPayload struct {
	TV     bool   `json:"tv"`
	Folder string `json:"folder"`
}

// Warning is a problem the user should know about.
type Warning struct {
	Code   string    `json:"code"`
	Detail string    `json:"detail"`
	At     time.Time `json:"at"`
}

// Warning codes.
const (
	WarnScanMovies = "media_scan_movies"
	WarnScanTV     = "media_scan_tv"
	WarnNoSpace    = "no_space"
)

// WarnNoSpace reports that downloads wait for free space. detail is shown to
// the user (for example which movie is waiting and how much space is needed).
func (s *Services) WarnNoSpace(detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.warnings == nil {
		s.warnings = map[string]Warning{}
	}
	s.warnings[WarnNoSpace] = Warning{Code: WarnNoSpace, Detail: detail, At: time.Now()}
}

// ClearNoSpace removes the waiting-for-space warning.
func (s *Services) ClearNoSpace() {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.warnings, WarnNoSpace)
}

// Scan asks the media server to scan a folder of the movie (or, with tv, the TV)
// library. A failure is logged, shown as a warning and retried later.
func (s *Services) Scan(ctx context.Context, tv bool, folder string) {
	if err := s.scan(ctx, tv, folder); err != nil {
		s.Log.Warn("media server scan failed, will retry", "folder", folder, "err", err)
		s.mu.Lock()
		sched := s.sched
		s.mu.Unlock()
		if sched == nil {
			return
		}
		if _, err := sched.Schedule(context.WithoutCancel(ctx), scheduler.Spec{Type: taskMediaScan, Key: scanKey(tv, folder),
			RunAt: time.Now().Add(scanRetryDelay), MaxAttempts: scanAttempts, Payload: scanPayload{TV: tv, Folder: folder}}); err != nil {
			s.Log.Error("schedule media server scan", "err", err)
		}
	}
}

func scanKey(tv bool, folder string) string {
	if tv {
		return "media-scan:tv:" + folder
	}
	return "media-scan:movies:" + folder
}

func (s *Services) scan(ctx context.Context, tv bool, folder string) error {
	target, err := s.Media(ctx)
	if tv {
		target, err = s.MediaTV(ctx)
	}
	if err == nil && target == nil {
		s.Log.Debug("media server scan skipped: no library configured", "folder", folder, "tv", tv)
		return nil // no library chosen: nothing to tell
	}
	remote := folder
	if err == nil {
		remote = target.Mapper.ToRemote(folder)
		s.Log.Debug("media server scan requested", "folder", folder, "remote", remote, "tv", tv)
		err = target.Server.Scan(ctx, target.Library, remote)
	}
	code := WarnScanMovies
	if tv {
		code = WarnScanTV
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.warnings == nil {
		s.warnings = map[string]Warning{}
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		s.warnings[code] = Warning{Code: code, Detail: remote + ": " + err.Error(), At: time.Now()}
	} else if err == nil {
		s.Log.Debug("media server scan completed", "remote", remote, "tv", tv)
		delete(s.warnings, code)
	}
	return err
}

// HandleScanRetries registers the task that retries failed scans. Both
// managers call it; it registers once.
func (s *Services) HandleScanRetries(sched *scheduler.Scheduler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sched != nil {
		return
	}
	s.sched = sched
	sched.Handle(taskMediaScan, func(ctx context.Context, t scheduler.Task) error {
		p, err := decode[scanPayload](t)
		if err != nil {
			return err
		}
		return s.scan(ctx, p.TV, p.Folder)
	})
}

// Warnings lists the current problems, oldest first.
func (s *Services) Warnings() []Warning {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Warning, 0, len(s.warnings))
	for _, w := range s.warnings {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return strings.Compare(out[i].Code, out[j].Code) < 0
	})
	return out
}
