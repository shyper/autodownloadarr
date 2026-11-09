package series

import (
	"context"
	"errors"
	"fmt"

	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
)

// ReportRelease removes a faulty release and automatically tries a replacement.
func (m *Manager) ReportRelease(ctx context.Context, id int64, key string) (store.Episode, error) {
	return m.replaceRelease(ctx, id, 0, key)
}

// replaceRelease waits for writers before removing only this title's tracked files.
func (m *Manager) replaceRelease(ctx context.Context, id, releaseID int64, reportedKey string) (store.Episode, error) {
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	e, err := m.store.Episode(ctx, id)
	if err != nil {
		return e, err
	}
	var report store.BlocklistEntry
	if releaseID == 0 {
		if reportedKey == "" || reportedKey != e.ReleaseKey {
			return e, fmt.Errorf("%w: current release changed", store.ErrInvalid)
		}
		report, err = m.store.ReportedRelease(ctx, store.ForEpisode(id), e.ReleaseKey, e.ReleaseTitle, e.ReleaseFingerprint)
		if err != nil {
			return e, err
		}
	} else {
		rel, err := m.store.Release(ctx, releaseID)
		if err != nil {
			return e, err
		}
		if rel.EpisodeID != id {
			return e, store.ErrNotFound
		}
	}
	se, err := m.store.Series(ctx, e.SeriesID)
	if err != nil {
		return e, err
	}

	m.mu.Lock()
	if m.replacing == nil {
		m.replacing = map[int64]bool{}
	}
	m.replacing[id] = true
	var done chan struct{}
	if a, ok := m.active[id]; ok {
		a.cancel(errSuperseded)
		done = a.done
	}
	delete(m.forced, id)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.replacing, id); m.mu.Unlock(); m.startQueued() }()
	// Once cancellation was requested, finish independently of the HTTP client.
	ctx = context.WithoutCancel(ctx)
	if done != nil {
		select {
		case <-done:
		case <-m.ctx.Done():
			return e, m.ctx.Err()
		}
	}
	m.reconciling.Lock()
	defer m.reconciling.Unlock()
	e, err = m.store.Episode(ctx, id)
	if err != nil {
		return e, err
	}
	if err := m.sched.Cancel(ctx, searchKey(id)); err != nil {
		return e, err
	}
	// Persist a non-runnable state before touching files. A failed cleanup
	// must never leave a queued replacement behind, including after restart.
	e.State, e.NextSearchAt = store.MovieFailed, nil
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return e, err
	}
	if err := m.removeFiles(ctx, se, &e); err != nil {
		e.LastError = "release_cleanup_failed"
		saveErr := m.store.UpdateEpisode(ctx, e)
		m.publishEpisode(e)
		return e, errors.Join(movies.ErrReleaseCleanup, err, saveErr)
	}
	if releaseID == 0 {
		if err := m.store.Blocklist(ctx, report); err != nil {
			return e, err
		}
	}
	e.ReleaseKey, e.ReleaseTitle, e.ReleaseFingerprint = "", "", ""
	e.MediaKey = ""
	e.Progress, e.Base = 0, store.WatchBase{}
	e.StartedAt, e.AvailableAt, e.WatchedAt, e.DeleteAt = nil, nil, nil, nil
	e.Attempts, e.LastError = 0, ""
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return e, err
	}
	if releaseID > 0 {
		m.mu.Lock()
		m.forced[id] = releaseID
		m.mu.Unlock()
		err = m.enqueue(ctx, e)
	} else {
		if _, ok, findErr := m.chooseRelease(ctx, id, 0); findErr != nil {
			err = findErr
		} else if ok {
			err = m.enqueue(ctx, e)
		} else {
			e.State = store.MovieSearching
			if err = m.store.UpdateEpisode(ctx, e); err == nil {
				err = m.scheduleSearch(ctx, id, 0, m.now())
			}
		}
	}
	if err != nil {
		return e, err
	}
	e, err = m.store.Episode(ctx, id)
	m.publishEpisode(e)
	return e, err
}
