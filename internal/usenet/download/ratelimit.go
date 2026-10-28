package download

import (
	"context"
	"sync"
	"time"
)

// RateLimiter is a token bucket limiting the download speed in bytes per
// second. A rate of 0 means unlimited. It can be shared by several jobs and
// changed while they run.
type RateLimiter struct {
	mu     sync.Mutex
	rate   float64 // bytes per second
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a limiter with the given rate (0: unlimited).
func NewRateLimiter(bytesPerSecond int64) *RateLimiter {
	return &RateLimiter{rate: float64(bytesPerSecond), last: time.Now()}
}

// SetRate changes the rate.
func (l *RateLimiter) SetRate(bytesPerSecond int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rate = float64(bytesPerSecond)
	l.tokens = min(l.tokens, l.rate)
}

// Wait blocks until n bytes may be downloaded.
func (l *RateLimiter) Wait(ctx context.Context, n int64) error {
	l.mu.Lock()
	if l.rate <= 0 {
		l.mu.Unlock()
		return nil
	}
	now := time.Now()
	// Allow a burst of one second.
	l.tokens = min(l.tokens+now.Sub(l.last).Seconds()*l.rate, l.rate)
	l.last = now
	l.tokens -= float64(n)
	wait := time.Duration(0)
	if l.tokens < 0 {
		wait = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if wait == 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
