package series

import (
	"context"
	"time"

	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
)

// consumedProgress is the share of an episode after which it counts as seen
// even if the media server has not marked it as watched yet (end credits).
const consumedProgress = 0.9

// Reconcile brings a series in line with its window: episodes that have
// been watched far enough are deleted, and the next WindowSize episodes
// from the current position are searched (or wait for their broadcast).
func (m *Manager) Reconcile(ctx context.Context, id int64) error {
	m.reconciling.Lock()
	defer m.reconciling.Unlock()

	se, err := m.store.Series(ctx, id)
	if err != nil {
		return err
	}
	all, err := m.store.Episodes(ctx, id)
	if err != nil {
		return err
	}
	g, err := m.store.General(ctx)
	if err != nil {
		return err
	}
	now := m.now()
	var list []store.Episode
	for _, e := range all {
		if se.Wants(e.Season) && e.State != store.EpisodeSkipped {
			list = append(list, e)
		}
	}

	// 1. Delete consumed episodes whose trigger fired.
	var nextCheck time.Time
	for i := range list {
		e := &list[i]
		if (e.State != store.MovieAvailable && e.State != store.MovieWatched) || se.Keep {
			continue
		}
		due, at := deletionDue(se, g, list, i, now)
		if due {
			if err := m.removeFiles(ctx, se, e); err != nil {
				return err
			}
			e.State, e.DeleteAt = store.MovieDeleted, nil
			if err := m.store.UpdateEpisode(ctx, *e); err != nil {
				return err
			}
			m.log.Info("episode deleted", "series", se.Title, "season", e.Season, "episode", e.Episode)
			m.publishEpisode(*e)
			continue
		}
		if !at.IsZero() {
			if e.DeleteAt == nil || !e.DeleteAt.Equal(at) {
				e.DeleteAt = &at
				if err := m.store.UpdateEpisode(ctx, *e); err != nil {
					return err
				}
				m.publishEpisode(*e)
			}
			if nextCheck.IsZero() || at.Before(nextCheck) {
				nextCheck = at
			}
		}
	}

	// 2. Fill the window.
	if !se.Paused {
		start := windowStart(list)
		window := map[int64]bool{}
		for i, n := start, 0; i < len(list) && n < se.WindowSize; i++ {
			if list[i].State == store.MovieDeleted && i > start {
				continue // removed by the user; do not fetch it again
			}
			window[list[i].ID] = true
			n++
		}
		for i := range list {
			e := &list[i]
			in := window[e.ID]
			switch {
			case in && e.State == store.EpisodePending:
				if err := m.want(ctx, e, now); err != nil {
					return err
				}
			case !in && i >= start && (e.State == store.MovieSearching || e.State == store.MovieWaiting || e.State == store.MovieQueued):
				// The window moved or shrank: not needed for now.
				if err := m.sched.Cancel(ctx, searchKey(e.ID)); err != nil {
					return err
				}
				e.State, e.NextSearchAt, e.LastError = store.EpisodePending, nil, ""
				if err := m.store.UpdateEpisode(ctx, *e); err != nil {
					return err
				}
				m.publishEpisode(*e)
			}
		}
	}

	if !nextCheck.IsZero() {
		if _, err := m.sched.Schedule(ctx, scheduler.Spec{Type: taskCheck, Key: checkKey(id), RunAt: nextCheck,
			Payload: idPayload{ID: id}}); err != nil {
			return err
		}
	}
	m.bus.Publish(EventSeriesUpdated, se)
	return nil
}

// want puts an episode of the window into the queue: search now if it has
// aired, else wait for the broadcast.
func (m *Manager) want(ctx context.Context, e *store.Episode, now time.Time) error {
	e.LastError = ""
	if e.Aired(now) {
		e.State, e.NextSearchAt = store.MovieSearching, nil
		if err := m.store.UpdateEpisode(ctx, *e); err != nil {
			return err
		}
		m.publishEpisode(*e)
		return m.scheduleSearch(ctx, e.ID, 0, time.Time{})
	}
	e.State = store.MovieWaiting
	e.NextSearchAt = nil
	if d, err := time.ParseInLocation(time.DateOnly, e.AirDate, now.Location()); err == nil {
		// Releases usually appear within hours after the broadcast.
		at := d.Add(24 * time.Hour)
		e.NextSearchAt = &at
	}
	if err := m.store.UpdateEpisode(ctx, *e); err != nil {
		return err
	}
	m.publishEpisode(*e)
	if e.NextSearchAt != nil {
		return m.scheduleSearch(ctx, e.ID, 0, *e.NextSearchAt)
	}
	return nil // unknown date: the daily refresh will bring one
}

// Position is where the viewer stands in a series: the current episode
// (being watched, or the next one up) and the next episode still to be
// broadcast. Either can be nil.
func Position(se store.Series, eps []store.Episode, now time.Time) (current, upcoming *store.Episode) {
	var list []store.Episode
	for _, e := range eps {
		if se.Wants(e.Season) && e.State != store.EpisodeSkipped {
			list = append(list, e)
		}
	}
	if start := windowStart(list); start < len(list) {
		current = &list[start]
	}
	for i := range list {
		if list[i].AirDate != "" && !list[i].Aired(now) {
			upcoming = &list[i]
			break
		}
	}
	return current, upcoming
}

// windowStart is the index of the current episode: the last one started,
// or the one after it when it has been seen.
func windowStart(list []store.Episode) int {
	last := -1
	for i, e := range list {
		if e.Progress > 0 || e.WatchedAt != nil {
			last = i
		}
	}
	start := max(last, 0)
	if last >= 0 && consumed(list[last]) {
		start = last + 1
	}
	for start < len(list) && list[start].State == store.MovieDeleted {
		start++
	}
	return start
}

func consumed(e store.Episode) bool {
	return e.WatchedAt != nil || e.Progress >= consumedProgress
}

// deletionDue decides whether episode i of the list may be deleted now,
// or else when it will be (zero if not yet known).
func deletionDue(se store.Series, g store.General, list []store.Episode, i int, now time.Time) (bool, time.Time) {
	e := list[i]
	var at time.Time
	earliest := func(t time.Time) {
		if at.IsZero() || t.Before(at) {
			at = t
		}
	}
	for _, later := range list[i+1:] {
		switch se.DeleteTrigger {
		case store.TriggerProgress:
			if later.Progress*100 >= float64(se.TriggerValue) || later.WatchedAt != nil {
				return true, time.Time{}
			}
		case store.TriggerMinutes:
			if later.StartedAt != nil {
				earliest(later.StartedAt.Add(time.Duration(se.TriggerValue) * time.Minute))
			}
		case store.TriggerWatched:
			// Skipped (or left unfinished): it counts as seen once a later
			// episode has been watched.
			if e.WatchedAt == nil && later.WatchedAt != nil {
				earliest(later.WatchedAt.Add(time.Duration(se.TriggerValue) * time.Minute))
			}
		}
	}
	if e.WatchedAt != nil {
		if se.DeleteTrigger == store.TriggerWatched {
			earliest(e.WatchedAt.Add(time.Duration(se.TriggerValue) * time.Minute))
		}
		// Without a next episode (season or series finale) the movie delay
		// applies.
		earliest(e.WatchedAt.Add(time.Duration(g.DeleteAfterHours) * time.Hour))
	}
	if !at.IsZero() && !now.Before(at) {
		return true, time.Time{}
	}
	return false, at
}
