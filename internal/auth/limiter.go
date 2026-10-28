package auth

import (
	"sync"
	"time"
)

// Limiter counts failed attempts per key (usually the client IP) within a
// sliding window and blocks further attempts once the limit is reached.
type Limiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	now      func() time.Time
	failures map[string][]time.Time
}

// NewLimiter allows max failures per key within window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, failures: make(map[string][]time.Time)}
}

// Allow reports whether key may attempt again, and if not, how long to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.recent(key)
	if len(recent) < l.max {
		return true, 0
	}
	return false, recent[0].Add(l.window).Sub(l.now())
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[key] = append(l.recent(key), l.now())
}

// Reset forgets the failures of key, e.g. after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// Prune drops keys without recent failures to bound memory use.
func (l *Limiter) Prune() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.failures {
		l.recent(key)
	}
}

// recent drops expired failures of key and returns the remaining ones.
// The caller holds l.mu.
func (l *Limiter) recent(key string) []time.Time {
	cutoff := l.now().Add(-l.window)
	times := l.failures[key]
	i := 0
	for i < len(times) && !times[i].After(cutoff) {
		i++
	}
	times = times[i:]
	if len(times) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = times
	return times
}
