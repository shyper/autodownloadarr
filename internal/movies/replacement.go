package movies

import (
	"context"
	"errors"
	"fmt"

	"github.com/shyper/autodownloadarr/internal/store"
)

// ReportRelease removes a faulty release and automatically tries a replacement.
func (m *Manager) ReportRelease(ctx context.Context, id int64, key string) (store.Movie, error) {
	return m.replaceRelease(ctx, id, 0, key)
}

// replaceRelease waits for writers before removing only this title's tracked files.
func (m *Manager) replaceRelease(ctx context.Context, id, releaseID int64, reportedKey string) (store.Movie, error) {
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	mv, err := m.store.Movie(ctx, id)
	if err != nil {
		return mv, err
	}
	var report store.BlocklistEntry
	if releaseID == 0 {
		if reportedKey == "" || reportedKey != mv.ReleaseKey {
			return mv, fmt.Errorf("%w: current release changed", store.ErrInvalid)
		}
		report, err = m.store.ReportedRelease(ctx, store.ForMovie(id), mv.ReleaseKey, mv.ReleaseTitle, mv.ReleaseFingerprint)
		if err != nil {
			return mv, err
		}
	} else {
		rel, err := m.store.Release(ctx, releaseID)
		if err != nil {
			return mv, err
		}
		if rel.MovieID != id {
			return mv, store.ErrNotFound
		}
	}

	m.mu.Lock()
	if m.deleting == nil {
		m.deleting = map[int64]bool{}
	}
	m.deleting[id] = true
	var done chan struct{}
	if a, ok := m.active[id]; ok {
		a.cancel(errSuperseded)
		done = a.done
	}
	delete(m.forced, id)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.deleting, id); m.mu.Unlock(); m.startQueued() }()
	// Once cancellation was requested, finish independently of the HTTP client.
	ctx = context.WithoutCancel(ctx)
	if done != nil {
		select {
		case <-done:
		case <-m.ctx.Done():
			return mv, m.ctx.Err()
		}
	}
	mv, err = m.store.Movie(ctx, id)
	if err != nil {
		return mv, err
	}
	if err := m.sched.Cancel(ctx, searchKey(id)); err != nil {
		return mv, err
	}
	if err := m.sched.Cancel(ctx, deleteKey(id)); err != nil {
		return mv, err
	}
	// Persist a non-runnable state before touching files. A failed cleanup
	// must never leave a queued replacement behind, including after restart.
	mv.State, mv.NextSearchAt = store.MovieFailed, nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return mv, err
	}
	if err := m.removeFiles(ctx, &mv); err != nil {
		mv.LastError = "release_cleanup_failed"
		saveErr := m.store.UpdateMovie(ctx, mv)
		m.publish(mv)
		return mv, errors.Join(ErrReleaseCleanup, err, saveErr)
	}
	if releaseID == 0 {
		if err := m.store.Blocklist(ctx, report); err != nil {
			return mv, err
		}
	}
	mv.ReleaseKey, mv.ReleaseTitle, mv.ReleaseFingerprint = "", "", ""
	mv.MediaKey = ""
	mv.Progress, mv.Base = 0, store.WatchBase{}
	mv.StartedAt, mv.AvailableAt, mv.WatchedAt, mv.DeleteAt = nil, nil, nil, nil
	mv.DeleteNotBefore = nil
	mv.Attempts, mv.LastError = 0, ""
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return mv, err
	}
	if releaseID > 0 {
		m.mu.Lock()
		m.forced[id] = releaseID
		m.mu.Unlock()
		err = m.enqueue(ctx, mv)
	} else {
		if _, ok, findErr := m.chooseRelease(ctx, id, 0); findErr != nil {
			err = findErr
		} else if ok {
			err = m.enqueue(ctx, mv)
		} else {
			mv.State = store.MovieSearching
			if err = m.store.UpdateMovie(ctx, mv); err == nil {
				err = m.scheduleSearch(ctx, id, 0, m.now())
			}
		}
	}
	if err != nil {
		return mv, err
	}
	mv, err = m.store.Movie(ctx, id)
	m.publish(mv)
	return mv, err
}
