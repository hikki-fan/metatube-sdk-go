package javdb

import (
	"sync"
	"time"
)

const (
	// DefaultWesternSearchInterval is a conservative delay between concurrent
	// Western scene searches against JavDB to prevent temporary 403 Forbidden
	// responses during bulk refresh.
	DefaultWesternSearchInterval = 10 * time.Second
)

// WesternSearchLimiter serializes and spaces out Western scene searches.
type WesternSearchLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	lastTime time.Time
	now      func() time.Time
	sleep    func(time.Duration)
}

// NewWesternSearchLimiter returns a new limiter with the specified interval.
func NewWesternSearchLimiter(interval time.Duration) *WesternSearchLimiter {
	return &WesternSearchLimiter{
		interval: interval,
		now:      time.Now,
		sleep:    time.Sleep,
	}
}

// Interval returns the current configured interval.
func (l *WesternSearchLimiter) Interval() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interval
}

// SetInterval updates the minimum spacing between consecutive Western searches.
func (l *WesternSearchLimiter) SetInterval(interval time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.interval = interval
}

// Wait blocks until the configured interval has elapsed since the last search.
func (l *WesternSearchLimiter) Wait() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.interval <= 0 {
		l.lastTime = l.now()
		return
	}

	now := l.now()
	if !l.lastTime.IsZero() {
		elapsed := now.Sub(l.lastTime)
		if elapsed < l.interval {
			l.sleep(l.interval - elapsed)
		}
	}
	l.lastTime = l.now()
}
