package series

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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
	RejectSeasonPack   = "season_pack"
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

// search queries all indexers for one episode.
func (m *Manager) search(ctx context.Context, se store.Series, e store.Episode) ([]store.Release, error) {
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
	var errs []error
	seen := map[string]bool{}
	var out []store.Release
	for _, ic := range indexers {
		if ok, until := m.svc.IndexerAvailable(ic.Config.ID); !ok {
			errs = append(errs, fmt.Errorf("%s: %w until %s", ic.Config.Name, movies.ErrIndexerPaused, until.Format(time.TimeOnly)))
			continue
		}
		items, textSearch, err := m.searchIndexer(ctx, ic, se, e)
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

// matchEpisode checks that a release contains the wanted episode.
func matchEpisode(info release.Info, se store.Series, e store.Episode, textSearch bool) string {
	switch {
	case info.SeasonPack():
		return RejectSeasonPack
	case info.Season != e.Season || !slices.Contains(info.Episodes, e.Episode):
		return RejectWrongEpisode
	case textSearch && !release.MatchesMovie(release.Info{Title: info.Title}, titles(se), 0):
		return release.RejectWrongMovie
	}
	return ""
}

func (m *Manager) searchIndexer(ctx context.Context, ic movies.IndexerClient, se store.Series, e store.Episode) ([]newznab.Item, bool, error) {
	caps, err := m.svc.Caps(ctx, ic)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", ic.Config.Name, err)
	}
	idSearch := caps.TVSearch && slices.Contains(caps.TVParams, "season") &&
		((se.TVDBID != 0 && slices.Contains(caps.TVParams, "tvdbid")) || (se.IMDbID != "" && slices.Contains(caps.TVParams, "imdbid")))
	q := newznab.EpisodeQuery{TVDBID: se.TVDBID, IMDbID: se.IMDbID, Title: se.Title, Season: e.Season, Episode: e.Episode,
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
	for _, r := range rels {
		if r.Rejected == "" && !r.Blocklisted {
			return r, true, nil
		}
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
	return m.scheduleSearch(ctx, e.ID, round+1, next)
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
	return nil
}

func (m *Manager) enqueue(ctx context.Context, e store.Episode) error {
	e.State, e.NextSearchAt = store.MovieQueued, nil
	if err := m.store.UpdateEpisode(ctx, e); err != nil {
		return err
	}
	m.publishEpisode(e)
	m.startQueued()
	return nil
}

// startQueued starts queued episodes in broadcast order up to the number of
// parallel downloads.
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
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range queued {
		if len(m.active) >= max(g.ParallelDownloads, 1) {
			return
		}
		if _, running := m.active[e.ID]; running {
			continue
		}
		rel, ok, err := m.chooseRelease(ctx, e.ID, m.forced[e.ID])
		if err != nil || !ok {
			go func() { _ = m.pickNext(ctx, e, 0) }()
			continue
		}
		dctx, cancel := context.WithCancelCause(ctx)
		m.active[e.ID] = &activeDownload{cancel: cancel, release: rel}
		delete(m.forced, e.ID)
		go m.runDownload(dctx, e, rel)
	}
}
