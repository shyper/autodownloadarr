package series

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/indexer/newznab"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/release"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
)

// Rejection codes for TV releases.
const (
	RejectWrongEpisode = "wrong_episode"
)

var searchDelays = []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour}

func (m *Manager) handleSearch(ctx context.Context, t scheduler.Task) error {
	p, err := decode[searchPayload](t)
	if err != nil {
		return err
	}
	e, err := m.store.Episode(ctx, p.EpisodeID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.State != store.MovieSearching && e.State != store.MovieWaiting {
		return nil
	}
	se, err := m.store.Series(ctx, e.SeriesID)
	if err != nil {
		return err
	}
	if !e.Aired(m.now()) && e.AirDate != "" {
		// Moved broadcast date: wait again.
		return m.want(ctx, &e, m.now())
	}
	e.State, e.NextSearchAt = store.MovieSearching, nil
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return err
	}
	m.publishEpisode(e)

	rels, err := m.search(ctx, se, e)
	switch {
	case errors.Is(err, movies.ErrNotConfigured):
		return m.fail(ctx, e, CodeNoIndexer, err.Error())
	case err != nil && len(rels) == 0:
		return m.wait(ctx, e, p.Round, CodeIndexerError, err.Error())
	}
	if err := m.store.ReplaceReleasesOf(ctx, store.ForEpisode(e.ID), rels); err != nil {
		return err
	}
	return m.pickNext(ctx, e, p.Round)
}

// search queries all indexers for one episode. If no release contains the
// episode, it asks for the whole season and keeps season packs as a fallback.
func (m *Manager) search(ctx context.Context, se store.Series, e store.Episode) ([]store.Release, error) {
	out, err := m.searchReleases(ctx, se, e, false, nil)
	if hasUsable(out) {
		return out, err
	}
	seen := make(map[string]bool, len(out))
	for _, r := range out {
		seen[r.Fingerprint] = true
	}
	packs, packErr := m.searchReleases(ctx, se, e, true, seen)
	return append(out, packs...), errors.Join(err, packErr)
}

// hasUsable reports whether a search found a release that may be downloaded.
func hasUsable(rels []store.Release) bool {
	for _, r := range rels {
		if r.Rejected == "" {
			return true
		}
	}
	return false
}

// searchReleases queries all indexers, either for the episode or for its
// whole season (the fallback that also returns season packs).
func (m *Manager) searchReleases(ctx context.Context, se store.Series, e store.Episode, season bool, seen map[string]bool) ([]store.Release, error) {
	indexers, err := m.svc.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := m.profile(ctx, se)
	if err != nil {
		return nil, err
	}
	runtime := e.Runtime
	if runtime == 0 {
		runtime = se.Runtime
	}
	if seen == nil {
		seen = map[string]bool{}
	}
	var errs []error
	var out []store.Release
	for _, ic := range indexers {
		if ok, until := m.svc.IndexerAvailable(ic.Config.ID); !ok {
			errs = append(errs, fmt.Errorf("%s: %w until %s", ic.Config.Name, movies.ErrIndexerPaused, until.Format(time.TimeOnly)))
			continue
		}
		items, textSearch, err := m.searchIndexer(ctx, ic, se, e, season)
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
			d := profile.Evaluate(release.Candidate{
				Title: it.Title, Size: it.Size, Grabs: it.Grabs, Passworded: it.Passworded,
				PasswordKnown: nzb.PasswordFromFilename(it.Title) != "",
			}, runtime)
			if d.Rejected == "" {
				d.Rejected = matchEpisode(d.Info, se, e, textSearch)
			}
			r := store.Release{
				EpisodeID: e.ID, IndexerID: ic.Config.ID, IndexerName: ic.Config.Name, GUID: it.GUID, Title: it.Title,
				Size: it.Size, NZBURL: it.Link, Grabs: it.Grabs, Passworded: it.Passworded, Fingerprint: fp,
				Score: d.Score, Rejected: d.Rejected,
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

// matchEpisode checks that a release contains the wanted episode or the
// whole season it belongs to.
func matchEpisode(info release.Info, se store.Series, e store.Episode, textSearch bool) string {
	if info.SeasonPack() {
		if info.Season != e.Season {
			return RejectWrongEpisode
		}
	} else if info.Season != e.Season || !slices.Contains(info.Episodes, e.Episode) {
		return RejectWrongEpisode
	}
	if textSearch && !release.MatchesMovie(release.Info{Title: info.Title}, titles(se), 0) {
		return release.RejectWrongMovie
	}
	return ""
}

func (m *Manager) searchIndexer(ctx context.Context, ic movies.IndexerClient, se store.Series, e store.Episode, seasonOnly bool) ([]newznab.Item, bool, error) {
	caps, err := m.svc.Caps(ctx, ic)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", ic.Config.Name, err)
	}
	idSearch := caps.TVSearch && slices.Contains(caps.TVParams, "season") &&
		((se.TVDBID != 0 && slices.Contains(caps.TVParams, "tvdbid")) || (se.IMDbID != "" && slices.Contains(caps.TVParams, "imdbid")))
	episode := e.Episode
	if seasonOnly {
		episode = 0
	}
	q := newznab.EpisodeQuery{TVDBID: se.TVDBID, IMDbID: se.IMDbID, Title: se.Title, Season: e.Season, Episode: episode,
		Categories: ic.Config.TVCategories}
	items, err := ic.Client.SearchEpisode(ctx, caps, q)
	if err != nil {
		return nil, false, err
	}
	if idSearch && len(items) > 0 {
		return items, false, nil
	}
	if !idSearch {
		return items, true, nil
	}
	var all []newznab.Item
	for _, t := range titles(se) {
		q.Title = t
		res, err := ic.Client.SearchEpisode(ctx, newznab.Caps{}, q)
		if err != nil {
			return all, true, err
		}
		all = append(all, res...)
	}
	return all, true, nil
}

func titles(se store.Series) []string {
	out := []string{se.Title}
	for _, t := range append([]string{se.OriginalTitle}, se.AltTitles...) {
		if t != "" && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, t) }) {
			out = append(out, t)
		}
	}
	return out
}

func (m *Manager) profile(ctx context.Context, se store.Series) (release.Profile, error) {
	if se.ProfileID > 0 {
		if p, err := m.store.Profile(ctx, se.ProfileID); err == nil {
			return p.Config, nil
		}
	}
	p, err := m.store.DefaultProfile(ctx)
	return p.Config, err
}

func (m *Manager) pickNext(ctx context.Context, e store.Episode, round int) error {
	m.mu.Lock()
	forced := m.forced[e.ID]
	m.mu.Unlock()
	if _, ok, err := m.chooseRelease(ctx, e.ID, forced); err != nil {
		return err
	} else if !ok {
		return m.wait(ctx, e, round, CodeNoRelease, "")
	}
	return m.enqueue(ctx, e)
}

func (m *Manager) chooseRelease(ctx context.Context, episodeID, forced int64) (store.Release, bool, error) {
	if forced > 0 {
		if r, err := m.store.Release(ctx, forced); err == nil {
			return r, true, nil
		}
	}
	rels, err := m.store.ReleasesOf(ctx, store.ForEpisode(episodeID))
	if err != nil {
		return store.Release{}, false, err
	}
	// Prefer a release for the single episode; a season pack is the
	// fallback when nothing else is left.
	var pack *store.Release
	for _, r := range rels {
		if r.Rejected != "" || r.Blocklisted {
			continue
		}
		if release.Parse(r.Title).SeasonPack() {
			if pack == nil {
				rr := r
				pack = &rr
			}
			continue
		}
		return r, true, nil
	}
	if pack != nil {
		return *pack, true, nil
	}
	return store.Release{}, false, nil
}

func (m *Manager) wait(ctx context.Context, e store.Episode, round int, code, detail string) error {
	next := m.now().Add(searchDelays[min(round, len(searchDelays)-1)])
	e.State, e.NextSearchAt, e.LastError = store.MovieWaiting, &next, code
	if detail != "" {
		e.LastError += ": " + detail
	}
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return err
	}
	m.publishEpisode(e)
	if err := m.scheduleSearch(ctx, e.ID, round+1, next); err != nil {
		return err
	}
	m.startQueued() // later episodes no longer wait for this one
	return nil
}

func (m *Manager) fail(ctx context.Context, e store.Episode, code, detail string) error {
	e.State, e.LastError, e.NextSearchAt = store.MovieFailed, code, nil
	if detail != "" {
		e.LastError += ": " + detail
	}
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return err
	}
	m.log.Warn("episode needs attention", "episode", e.ID, "reason", e.LastError)
	m.publishEpisode(e)
	title := fmt.Sprintf("S%02dE%02d failed", e.Season, e.Episode)
	if se, err := m.store.Series(ctx, e.SeriesID); err == nil {
		title = fmt.Sprintf("%s S%02dE%02d failed", se.Title, e.Season, e.Episode)
	}
	m.notify(events.Notification{Event: "episode.failed", Title: title, Body: e.LastError})
	m.startQueued() // later episodes no longer wait for this one
	return nil
}

func (m *Manager) enqueue(ctx context.Context, e store.Episode) error {
	// A season pack may have filled this episode while its own search was
	// still running, so only queue it if it really still needs a file.
	cur, err := m.store.Episode(ctx, e.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch cur.State {
	case store.MovieSearching, store.MovieWaiting, store.MovieFailed, store.MovieDownloading:
	default:
		return nil
	}
	cur.State, cur.NextSearchAt = store.MovieQueued, nil
	if err := m.store.UpdateEpisode(ctx, cur); err != nil {
		return err
	}
	m.publishEpisode(cur)
	m.startQueued()
	return nil
}

// taskSpace retries downloads that wait for free space.
const taskSpace = "series.space"

// spaceRetryDelay is how long to wait before checking for space again.
var spaceRetryDelay = 5 * time.Minute

// retrySpace schedules another startQueued call for when the space situation
// may have changed.
func (m *Manager) retrySpace(ctx context.Context) {
	if _, err := m.sched.Schedule(context.WithoutCancel(ctx), scheduler.Spec{
		Type: taskSpace, Key: "series-space", RunAt: m.now().Add(spaceRetryDelay), MaxAttempts: 1,
	}); err != nil {
		m.log.Error("schedule space retry", "err", err)
	}
}

// startQueued starts queued episodes up to the number of parallel
// downloads. The episodes of a series are downloaded one after another in
// broadcast order, so that the next one to watch is ready first: while an
// earlier episode is still being searched, the later ones wait for it.
func (m *Manager) startQueued() {
	ctx := m.ctx
	g, err := m.store.General(ctx)
	if err != nil {
		m.log.Error("read settings", "err", err)
		return
	}
	queued, err := m.store.EpisodesInState(ctx, store.MovieQueued)
	if err != nil {
		m.log.Error("list queued episodes", "err", err)
		return
	}
	searching, err := m.store.EpisodesInState(ctx, store.MovieSearching)
	if err != nil {
		m.log.Error("list searching episodes", "err", err)
		return
	}
	firstSearching := map[int64]store.Episode{}
	for _, e := range searching {
		if f, ok := firstSearching[e.SeriesID]; !ok || e.Before(f) {
			firstSearching[e.SeriesID] = e
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	busy := map[int64]bool{}
	for _, a := range m.active {
		busy[a.episode.SeriesID] = true
	}
	// Space limits apply to the whole temporary library. Check them once
	// before the loop: walking the folders can take a moment.
	budget := m.svc.SpaceBudget(g, g.SeriesPath)
	retryScheduled := false
	for _, e := range queued {
		if m.deleting[e.SeriesID] {
			continue // the series is being removed
		}
		if len(m.active) >= max(g.ParallelDownloads, 1) {
			return
		}
		if _, running := m.active[e.ID]; running {
			continue
		}
		if f, ok := firstSearching[e.SeriesID]; busy[e.SeriesID] || (ok && f.Before(e)) {
			continue
		}
		rel, ok, err := m.chooseRelease(ctx, e.ID, m.forced[e.ID])
		if err != nil || !ok {
			go func() { _ = m.pickNext(ctx, e, 0) }()
			continue
		}
		if rel.Size > budget {
			m.svc.WarnNoSpace(fmt.Sprintf("S%02dE%02d needs %s, but only %s fit", e.Season, e.Episode, movies.FormatSize(rel.Size), movies.FormatSize(budget)))
			if !retryScheduled {
				m.retrySpace(ctx)
				retryScheduled = true
			}
			continue // stays queued; the queue is retried later
		}
		budget -= rel.Size
		m.svc.ClearNoSpace()
		dctx, cancel := context.WithCancelCause(ctx)
		done := make(chan struct{})
		m.active[e.ID] = &activeDownload{episode: e, cancel: cancel, release: rel, done: done}
		busy[e.SeriesID] = true
		delete(m.forced, e.ID)
		go m.runDownload(dctx, e, rel, done)
	}
}
