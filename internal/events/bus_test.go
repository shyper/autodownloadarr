package events

import (
	"io"
	"log/slog"
	"testing"
)

func newBus() *Bus { return New(slog.New(slog.NewTextHandler(io.Discard, nil))) }

func receive(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	default:
		return Event{}, false
	}
}

func TestPatterns(t *testing.T) {
	b := newBus()
	all, unsubAll := b.Subscribe(10)
	defer unsubAll()
	downloads, unsubDownloads := b.Subscribe(10, "download.*")
	defer unsubDownloads()
	exact, unsubExact := b.Subscribe(10, "movie.added")
	defer unsubExact()

	b.Publish("download.progress", 42)
	b.Publish("movie.added", "dune")
	b.Publish("downloads", nil) // no dot: must not match "download.*"

	for _, want := range []string{"download.progress", "movie.added", "downloads"} {
		if ev, ok := receive(t, all); !ok || ev.Type != want {
			t.Errorf("all: got %q (ok=%v), want %q", ev.Type, ok, want)
		}
	}
	if ev, ok := receive(t, downloads); !ok || ev.Type != "download.progress" || ev.Data != 42 {
		t.Errorf("downloads: got %+v (ok=%v)", ev, ok)
	}
	if ev, ok := receive(t, downloads); ok {
		t.Errorf("downloads: unexpected %+v", ev)
	}
	if ev, ok := receive(t, exact); !ok || ev.Type != "movie.added" {
		t.Errorf("exact: got %+v (ok=%v)", ev, ok)
	}
}

func TestSlowSubscriberDropsInsteadOfBlocking(t *testing.T) {
	b := newBus()
	ch, unsub := b.Subscribe(1)
	defer unsub()

	b.Publish("a", nil)
	b.Publish("b", nil) // buffer full: dropped, Publish must return

	if ev, _ := receive(t, ch); ev.Type != "a" {
		t.Errorf("got %q, want a", ev.Type)
	}
	if ev, ok := receive(t, ch); ok {
		t.Errorf("unexpected %+v", ev)
	}
}

func TestUnsubscribeClosesAndIsIdempotent(t *testing.T) {
	b := newBus()
	ch, unsub := b.Subscribe(1)
	unsub()
	unsub()
	if _, ok := <-ch; ok {
		t.Error("channel not closed")
	}
	b.Publish("after", nil) // must not panic
}
