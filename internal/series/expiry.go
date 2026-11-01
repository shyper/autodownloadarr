package series

import (
	"context"
	"errors"
	"time"

	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
)

// expiryAt is when the files of a series nobody watches any more are
// removed (zero: not planned). The clock starts at the last playback, or
// when the oldest episode still waiting to be watched became available,
// whichever is later, so a new season does not expire right away.
func expiryAt(se store.Series, g store.General, eps []store.Episode) time.Time {
	if se.Keep || se.ExpiredAt != nil || g.UnwatchedExpiryDays <= 0 {
		return time.Time{}
	}
	var last, ready time.Time
	for _, e := range eps {
		for _, t := range []*time.Time{e.StartedAt, e.WatchedAt} {
			if t != nil && t.After(last) {
				last = *t
			}
		}
		if e.State == store.MovieAvailable && e.AvailableAt != nil && (ready.IsZero() || e.AvailableAt.Before(ready)) {
			ready = *e.AvailableAt
		}
	}
	if ready.IsZero() {
		return time.Time{} // nothing left to watch
	}
	if last.After(ready) {
		ready = last
	}
	return ready.Add(time.Duration(g.UnwatchedExpiryDays) * 24 * time.Hour)
}

func (m *Manager) handleExpire(ctx context.Context, t scheduler.Task) error {
	p, err := decode[idPayload](t)
	if err != nil {
		return err
	}
	if err := m.Expire(ctx, p.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

// Expire removes all files of a series whose expiry date has passed. The
// series stays listed and can be downloaded again with Restore.
func (m *Manager) Expire(ctx context.Context, id int64) error {
	due, err := m.expiryDue(ctx, id)
	if err != nil || !due {
		return err
	}

	// Stop the running downloads first, as Delete does. The reconcile lock
	// is taken only afterwards: a finishing download reconciles the series.
	m.mu.Lock()
	m.deleting[id] = true
	var running []chan struct{}
	for _, a := range m.active {
		if a.episode.SeriesID == id {
			a.cancel(errDeleted)
			if a.done != nil {
				running = append(running, a.done)
			}
		}
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.deleting, id)
		m.mu.Unlock()
		m.startQueued() // other series may use the free slots
	}()
	for _, done := range running {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	m.reconciling.Lock()
	defer m.reconciling.Unlock()
	// Watching may have started meanwhile.
	if due, err := m.expiryDue(ctx, id); err != nil || !due {
		return err
	}
	se, err := m.store.Series(ctx, id)
	if err != nil {
		return err
	}
	eps, err := m.store.Episodes(ctx, id)
	if err != nil {
		return err
	}
	for i := range eps {
		e := &eps[i]
		switch e.State {
		case store.MovieAvailable:
			e.State = store.MovieExpired
		case store.MovieWatched:
			e.State = store.MovieDeleted
		case store.MovieSearching, store.MovieWaiting, store.MovieQueued, store.MovieDownloading, store.MovieFailed:
			e.State = store.EpisodePending
		default:
			continue
		}
		if err := m.sched.Cancel(ctx, searchKey(e.ID)); err != nil {
			return err
		}
		if err := m.removeFiles(ctx, se, e); err != nil {
			return err
		}
		e.DeleteAt, e.NextSearchAt, e.LastError = nil, nil, ""
		if err := m.store.UpdateEpisode(ctx, *e); err != nil {
			return err
		}
		m.publishEpisode(*e)
	}
	now := m.now()
	se.ExpiredAt = &now
	if err := m.store.UpdateSeries(ctx, se); err != nil {
		return err
	}
	if err := m.sched.Cancel(ctx, checkKey(id)); err != nil {
		return err
	}
	m.cleanupFolder(ctx, se)
	m.log.Info("series expired", "series", se.Title)
	m.bus.Publish(EventSeriesUpdated, se)
	return nil
}

func (m *Manager) expiryDue(ctx context.Context, id int64) (bool, error) {
	se, err := m.store.Series(ctx, id)
	if err != nil {
		return false, err
	}
	eps, err := m.store.Episodes(ctx, id)
	if err != nil {
		return false, err
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return false, err
	}
	at := expiryAt(se, g, eps)
	return !at.IsZero() && !m.now().Before(at), nil
}

// Restore downloads an expired series again, starting at the episode the
// viewer stopped at.
func (m *Manager) Restore(ctx context.Context, id int64) (store.Series, error) {
	se, err := m.store.Series(ctx, id)
	if err != nil || se.ExpiredAt == nil {
		return se, err
	}
	// Expired series do not count against the owner's quota.
	if err := m.store.CheckUserQuota(ctx, se.RequestedBy); err != nil {
		return store.Series{}, err
	}
	m.reconciling.Lock()
	eps, err := m.store.Episodes(ctx, id)
	if err == nil {
		for _, e := range eps {
			if e.State != store.MovieExpired {
				continue
			}
			e.State = store.EpisodePending
			if err = m.store.UpdateEpisode(ctx, e); err != nil {
				break
			}
			m.publishEpisode(e)
		}
	}
	if err == nil {
		se.ExpiredAt = nil
		err = m.store.UpdateSeries(ctx, se)
	}
	m.reconciling.Unlock()
	if err != nil {
		return store.Series{}, err
	}
	m.log.Info("series restored", "series", se.Title)
	return se, m.Reconcile(ctx, id)
}
