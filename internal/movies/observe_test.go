package movies

import (
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/mediaserver/plex"
	"github.com/shyper/autodownloadarr/internal/store"
)

func TestObserve(t *testing.T) {
	since := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	long := since.Add(-365 * 24 * time.Hour)
	hour := time.Hour
	item := func(views int, offset time.Duration, viewed time.Time) plex.Item {
		return plex.Item{ViewCount: views, ViewOffset: offset, Duration: 2 * hour, LastViewedAt: viewed}
	}
	type step struct {
		item     plex.Item
		watched  bool
		progress float64
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"new title", []step{
			{item(0, 0, time.Time{}), false, 0},
			{item(0, hour, since.Add(hour)), false, 0.5},
			{item(1, 0, since.Add(2*hour)), true, 1},
		}},
		{"watched a year ago", []step{
			{item(1, 0, long), false, 0},
			{item(1, 0, long), false, 0},
			{item(1, 30*time.Minute, since.Add(hour)), false, 0.25},
			{item(2, 0, since.Add(2*hour)), true, 1},
		}},
		{"old resume position", []step{
			{item(0, 90*time.Minute, long), false, 0},
			{item(0, 91*time.Minute, since.Add(hour)), false, 91.0 / 120},
		}},
		{"played before the first sighting", []step{
			{item(0, hour, since.Add(10*time.Minute)), false, 0.5},
		}},
		{"watched to the end before the first sighting", []step{
			{item(3, 0, since.Add(hour)), true, 1},
		}},
		{"marked unwatched in Plex", []step{
			{item(0, 0, time.Time{}), false, 0},
			{item(1, 0, since.Add(hour)), true, 1},
			{item(0, 0, since.Add(hour)), false, 0},
			{item(1, 0, since.Add(3*hour)), true, 1},
		}},
	}
	for _, c := range cases {
		var base store.WatchBase
		for i, s := range c.steps {
			watched, progress := Observe(&base, s.item, since)
			if watched != s.watched || progress != s.progress {
				t.Errorf("%s, step %d: watched %v progress %v, want %v %v", c.name, i, watched, progress, s.watched, s.progress)
			}
		}
	}
}
