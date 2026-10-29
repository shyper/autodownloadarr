package movies

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/indexer/newznab"
	"github.com/shyper/autodownloadarr/internal/release"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
)

// searchDelays are the waits before searching again when nothing
// acceptable was found: 1 h, 6 h, then daily.
var searchDelays = []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour}

func (m *Manager) handleSearch(ctx context.Context, t scheduler.Task) error {
	p, err := decode[searchPayload](t)
	if err != nil {
		return err
	}
	mv, err := m.store.Movie(ctx, p.MovieID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if mv.State != store.MovieSearching && mv.State != store.MovieWaiting {
		return nil
	}
	mv.State, mv.NextSearchAt = store.MovieSearching, nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.publish(mv)

	rels, err := m.search(ctx, mv)
	switch {
	case errors.Is(err, ErrNotConfigured):
		return m.fail(ctx, mv, CodeNoIndexer, err.Error())
	case err != nil && len(rels) == 0:
		m.log.Warn("search failed", "movie", mv.Title, "err", err)
		return m.wait(ctx, mv, p.Round, CodeIndexerError, err.Error())
	}
	if err := m.store.ReplaceReleases(ctx, mv.ID, rels); err != nil {
		return err
	}
	return m.pickNext(ctx, mv, p.Round)
}

// search queries all indexers and evaluates the results with the movie's
// profile. Errors of single indexers are returned joined; results of the
// others are still used.
func (m *Manager) search(ctx context.Context, mv store.Movie) ([]store.Release, error) {
	indexers, err := m.svc.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := m.profile(ctx, mv)
	if err != nil {
		return nil, err
	}
	var errs []error
	seen := map[string]bool{}
	var out []store.Release
	for _, ic := range indexers {
		if ok, until := m.svc.IndexerAvailable(ic.Config.ID); !ok {
			errs = append(errs, fmt.Errorf("%s: %w until %s", ic.Config.Name, ErrIndexerPaused, until.Format(time.TimeOnly)))
			continue
		}
		items, textSearch, err := m.searchIndexer(ctx, ic, mv)
		if ctx.Err() == nil {
			m.svc.IndexerResult(ic.Config.ID, err)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, it := range items {
			fp := release.Fingerprint(it.Title, it.Size)
			if seen[fp] {
				continue
			}
			seen[fp] = true
			cand := release.Candidate{
				Title:         it.Title,
				Size:          it.Size,
				Grabs:         it.Grabs,
				Passworded:    it.Passworded,
				PasswordKnown: nzb.PasswordFromFilename(it.Title) != "",
			}
			d := profile.Evaluate(cand, mv.Runtime)
			if d.Rejected == "" && textSearch && !release.MatchesMovie(d.Info, titles(mv), mv.Year) {
				d.Rejected = release.RejectWrongMovie
			}
			r := store.Release{
				MovieID:     mv.ID,
				IndexerID:   ic.Config.ID,
				IndexerName: ic.Config.Name,
				GUID:        it.GUID,
				Title:       it.Title,
				Size:        it.Size,
				NZBURL:      it.Link,
				Grabs:       it.Grabs,
				Passworded:  it.Passworded,
				Fingerprint: fp,
				Score:       d.Score,
				Rejected:    d.Rejected,
			}
			if !it.PubDate.IsZero() {
				pub := it.PubDate
				r.PublishedAt = &pub
			}
			out = append(out, r)
		}
	}
	return out, errors.Join(errs...)
}

// searchIndexer searches by ID where supported and falls back to the
// titles. It reports whether the results come from a text search.
func (m *Manager) searchIndexer(ctx context.Context, ic IndexerClient, mv store.Movie) ([]newznab.Item, bool, error) {
	caps, err := m.svc.Caps(ctx, ic)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", ic.Config.Name, err)
	}
	q := newznab.MovieQuery{IMDbID: mv.IMDbID, TMDBID: mv.TMDBID, Title: mv.Title, Year: mv.Year, Categories: ic.Config.MovieCategories}
	idSearch := caps.MovieSearch && (slices.Contains(caps.MovieParams, "imdbid") && mv.IMDbID != "" ||
		slices.Contains(caps.MovieParams, "tmdbid") && mv.TMDBID != 0)
	items, err := ic.Client.SearchMovie(ctx, caps, q)
	if err != nil {
		return nil, false, err
	}
	if idSearch && len(items) > 0 {
		return items, false, nil
	}
	// Text search with every known title (German title, original title, …).
	if !idSearch {
		return items, true, nil
	}
	var all []newznab.Item
	for _, t := range titles(mv) {
		res, err := ic.Client.SearchMovie(ctx, newznab.Caps{}, newznab.MovieQuery{Title: t, Year: mv.Year, Categories: ic.Config.MovieCategories})
		if err != nil {
			return all, true, err
		}
		all = append(all, res...)
	}
	return all, true, nil
}

func titles(mv store.Movie) []string {
	out := []string{mv.Title}
	for _, t := range append([]string{mv.OriginalTitle}, mv.AltTitles...) {
		if t != "" && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, t) }) {
			out = append(out, t)
		}
	}
	return out
}

func (m *Manager) profile(ctx context.Context, mv store.Movie) (release.Profile, error) {
	if mv.ProfileID > 0 {
		p, err := m.store.Profile(ctx, mv.ProfileID)
		if err == nil {
			return p.Config, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return release.Profile{}, err
		}
	}
	p, err := m.store.DefaultProfile(ctx)
	return p.Config, err
}

// pickNext queues the best stored release that is acceptable and not
// blocklisted, or waits for the next search.
func (m *Manager) pickNext(ctx context.Context, mv store.Movie, round int) error {
	rel, ok, err := m.bestRelease(ctx, mv.ID)
	if err != nil {
		return err
	}
	if !ok {
		return m.wait(ctx, mv, round, CodeNoRelease, "")
	}
	mv.ReleaseTitle = rel.Title
	return m.enqueue(ctx, mv)
}

func (m *Manager) bestRelease(ctx context.Context, movieID int64) (store.Release, bool, error) {
	m.mu.Lock()
	forced := m.forced[movieID]
	m.mu.Unlock()
	return m.chooseRelease(ctx, movieID, forced)
}

// chooseRelease returns the forced release if set, else the best acceptable
// one that is not blocklisted.
func (m *Manager) chooseRelease(ctx context.Context, movieID, forced int64) (store.Release, bool, error) {
	if forced > 0 {
		if r, err := m.store.Release(ctx, forced); err == nil {
			return r, true, nil
		}
	}
	rels, err := m.store.Releases(ctx, movieID)
	if err != nil {
		return store.Release{}, false, err
	}
	for _, r := range rels {
		if r.Rejected == "" && !r.Blocklisted {
			return r, true, nil
		}
	}
	return store.Release{}, false, nil
}

// wait parks the movie until the next search.
func (m *Manager) wait(ctx context.Context, mv store.Movie, round int, code, detail string) error {
	delay := searchDelays[min(round, len(searchDelays)-1)]
	next := m.now().Add(delay)
	mv.State, mv.NextSearchAt = store.MovieWaiting, &next
	mv.LastError = code
	if detail != "" {
		mv.LastError += ": " + detail
	}
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.publish(mv)
	return m.scheduleSearch(ctx, mv.ID, round+1, next)
}

// fail stops the automatic handling until the user acts.
func (m *Manager) fail(ctx context.Context, mv store.Movie, code, detail string) error {
	mv.State = store.MovieFailed
	mv.LastError = code
	if detail != "" {
		mv.LastError += ": " + detail
	}
	mv.NextSearchAt = nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.log.Warn("movie needs attention", "movie", mv.Title, "reason", mv.LastError)
	m.publish(mv)
	return nil
}

// enqueue marks the movie as queued and starts downloads while slots are
// free.
func (m *Manager) enqueue(ctx context.Context, mv store.Movie) error {
	mv.State = store.MovieQueued
	mv.NextSearchAt = nil
	if err := m.store.UpdateMovie(ctx, mv); err != nil {
		return err
	}
	m.publish(mv)
	m.startQueued()
	return nil
}

// taskSpace retries downloads that wait for free space.
const taskSpace = "movie.space"

// spaceRetryDelay is how long to wait before checking for space again.
var spaceRetryDelay = 5 * time.Minute

// retrySpace schedules another startQueued call for when the space situation
// may have changed.
func (m *Manager) retrySpace(ctx context.Context) {
	if _, err := m.sched.Schedule(context.WithoutCancel(ctx), scheduler.Spec{
		Type: taskSpace, Key: "movie-space", RunAt: m.now().Add(spaceRetryDelay), MaxAttempts: 1,
	}); err != nil {
		m.log.Error("schedule space retry", "err", err)
	}
}

// startQueued starts queued movies, oldest first, up to the configured
// number of parallel downloads.
func (m *Manager) startQueued() {
	ctx := m.ctx
	g, err := m.store.General(ctx)
	if err != nil {
		m.log.Error("read settings", "err", err)
		return
	}
	queued, err := m.store.Movies(ctx, store.MovieQueued)
	if err != nil {
		m.log.Error("list queued movies", "err", err)
		return
	}
	slices.Reverse(queued) // Movies() is newest first

	// Space limits apply to the whole temporary library. Check them before
	// taking the lock: walking the folders can take a moment.
	budget := m.svc.SpaceBudget(g, g.MoviesPath)

	m.mu.Lock()
	defer m.mu.Unlock()
	retryScheduled := false
	for _, mv := range queued {
		if m.deleting[mv.ID] {
			continue // the movie is being removed
		}
		if len(m.active) >= max(g.ParallelDownloads, 1) {
			return
		}
		if _, running := m.active[mv.ID]; running {
			continue
		}
		rel, ok, err := m.chooseRelease(ctx, mv.ID, m.forced[mv.ID])
		if err != nil || !ok {
			go func() { _ = m.pickNext(ctx, mv, 0) }()
			continue
		}
		if rel.Size > budget {
			m.svc.WarnNoSpace(fmt.Sprintf("%s needs %s, but only %s fit", mv.Title, FormatSize(rel.Size), FormatSize(budget)))
			if !retryScheduled {
				m.retrySpace(ctx)
				retryScheduled = true
			}
			continue // stays queued; the queue is retried periodically
		}
		budget -= rel.Size
		m.svc.ClearNoSpace()
		dctx, cancel := context.WithCancelCause(ctx)
		done := make(chan struct{})
		m.active[mv.ID] = &activeDownload{cancel: cancel, release: rel, done: done}
		delete(m.forced, mv.ID)
		go m.runDownload(dctx, mv, rel, done)
	}
}
