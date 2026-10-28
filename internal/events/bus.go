// Package events is an in-process publish/subscribe bus for live updates
// (download progress, state changes) that are pushed to the web UI.
//
// Delivery is best effort: a subscriber that does not keep up loses events.
// Work that must not be lost goes through the scheduler instead.
package events

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Event is a single message on the bus.
type Event struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
}

// Bus fans events out to subscribers.
type Bus struct {
	log  *slog.Logger
	mu   sync.RWMutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	ch       chan Event
	patterns []string
	once     sync.Once
}

// New returns an empty bus.
func New(log *slog.Logger) *Bus {
	return &Bus{log: log, subs: make(map[*subscriber]struct{})}
}

// Publish sends an event to every matching subscriber without blocking.
func (b *Bus) Publish(eventType string, data any) {
	ev := Event{Type: eventType, Time: time.Now(), Data: data}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subs {
		if !sub.matches(eventType) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			b.log.Warn("events: subscriber too slow, dropping event", "type", eventType)
		}
	}
}

// Subscribe returns a channel receiving events whose type matches one of the
// patterns: an exact type ("download.completed") or a prefix ending in ".*"
// ("download.*"). Without patterns every event matches. The returned function
// unsubscribes and closes the channel; it is safe to call more than once.
func (b *Bus) Subscribe(buffer int, patterns ...string) (<-chan Event, func()) {
	sub := &subscriber{ch: make(chan Event, max(buffer, 1)), patterns: patterns}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub.ch, func() {
		sub.once.Do(func() {
			b.mu.Lock()
			delete(b.subs, sub)
			b.mu.Unlock()
			close(sub.ch)
		})
	}
}

func (s *subscriber) matches(eventType string) bool {
	if len(s.patterns) == 0 {
		return true
	}
	for _, p := range s.patterns {
		if p == eventType {
			return true
		}
		if prefix, ok := strings.CutSuffix(p, "*"); ok && strings.HasSuffix(prefix, ".") && strings.HasPrefix(eventType, prefix) {
			return true
		}
	}
	return false
}
