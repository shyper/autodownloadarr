package movies

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/scheduler"
)

// Plex scans that fail are retried by a task. Until one succeeds again the
// failure is listed as a warning in the UI.

const taskPlexScan = "plex.scan"

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
	WarnPlexScanMovies = "plex_scan_movies"
	WarnPlexScanTV     = "plex_scan_tv"
)

// Scan asks Plex to scan a folder of the movie (or, with tv, the TV)
// library. A failure is logged, shown as a warning and retried later.
func (s *Services) Scan(ctx context.Context, tv bool, folder string) {
	if err := s.scan(ctx, tv, folder); err != nil {
		s.Log.Warn("plex scan failed, will retry", "folder", folder, "err", err)
		s.mu.Lock()
		sched := s.sched
		s.mu.Unlock()
		if sched == nil {
			return
		}
		if _, err := sched.Schedule(context.WithoutCancel(ctx), scheduler.Spec{Type: taskPlexScan, Key: scanKey(tv, folder),
			RunAt: time.Now().Add(scanRetryDelay), MaxAttempts: scanAttempts, Payload: scanPayload{TV: tv, Folder: folder}}); err != nil {
			s.Log.Error("schedule plex scan", "err", err)
		}
	}
}

func scanKey(tv bool, folder string) string {
	if tv {
		return "plex-scan:tv:" + folder
	}
	return "plex-scan:movies:" + folder
}

func (s *Services) scan(ctx context.Context, tv bool, folder string) error {
	target, err := s.Plex(ctx)
	if tv {
		target, err = s.PlexTV(ctx)
	}
	if err == nil && target == nil {
		return nil // no library chosen: nothing to tell
	}
	remote := folder
	if err == nil {
		remote = target.Mapper.ToRemote(folder)
		err = target.Client.Scan(ctx, target.Library, remote)
	}
	code := WarnPlexScanMovies
	if tv {
		code = WarnPlexScanTV
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.warnings == nil {
		s.warnings = map[string]Warning{}
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		s.warnings[code] = Warning{Code: code, Detail: remote + ": " + err.Error(), At: time.Now()}
	} else if err == nil {
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
	sched.Handle(taskPlexScan, func(ctx context.Context, t scheduler.Task) error {
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
