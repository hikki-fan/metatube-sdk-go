package javdb

import (
	"math/rand"
	"sync"
	"time"
)

const (
	// DefaultWesternSearchMinInterval and DefaultWesternSearchMaxInterval keep
	// bulk Western scene searches below JavDB's rate limit while avoiding a
	// perfectly periodic request pattern.
	DefaultWesternSearchMinInterval = 10 * time.Second
	DefaultWesternSearchMaxInterval = 25 * time.Second

	// DefaultWesternSearchInterval is kept for callers that need the historical
	// fixed interval value. New JavDB instances use the random range above.
	DefaultWesternSearchInterval = DefaultWesternSearchMinInterval
)

// WesternSearchLimiter serializes and spaces out Western scene searches.
type WesternSearchLimiter struct {
	mu           sync.Mutex
	interval     time.Duration
	minInterval  time.Duration
	maxInterval  time.Duration
	lastTime     time.Time
	now          func() time.Time
	sleep        func(time.Duration)
	randomInt63n func(int64) int64
}

// NewWesternSearchLimiter returns a new limiter with the specified interval.
func NewWesternSearchLimiter(interval time.Duration) *WesternSearchLimiter {
	return &WesternSearchLimiter{
		interval:     interval,
		minInterval:  interval,
		maxInterval:  interval,
		now:          time.Now,
		sleep:        time.Sleep,
		randomInt63n: rand.Int63n,
	}
}

// NewRandomWesternSearchLimiter returns a serialized limiter that waits a
// uniformly random duration in [minInterval, maxInterval] between searches.
func NewRandomWesternSearchLimiter(minInterval, maxInterval time.Duration) *WesternSearchLimiter {
	if minInterval < 0 {
		minInterval = 0
	}
	if maxInterval < minInterval {
		maxInterval = minInterval
	}
	return &WesternSearchLimiter{
		interval:     minInterval,
		minInterval:  minInterval,
		maxInterval:  maxInterval,
		now:          time.Now,
		sleep:        time.Sleep,
		randomInt63n: rand.Int63n,
	}
}

// Interval returns the current configured interval.
func (l *WesternSearchLimiter) Interval() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interval
}

// IntervalRange returns the configured inclusive random delay range.
func (l *WesternSearchLimiter) IntervalRange() (time.Duration, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.intervalRangeLocked()
}

// SetInterval updates the minimum spacing between consecutive Western searches.
func (l *WesternSearchLimiter) SetInterval(interval time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.interval = interval
	l.minInterval = interval
	l.maxInterval = interval
}

// SetIntervalRange updates the inclusive random delay range between searches.
func (l *WesternSearchLimiter) SetIntervalRange(minInterval, maxInterval time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if minInterval < 0 {
		minInterval = 0
	}
	if maxInterval < minInterval {
		maxInterval = minInterval
	}
	l.interval = minInterval
	l.minInterval = minInterval
	l.maxInterval = maxInterval
}

func (l *WesternSearchLimiter) intervalRangeLocked() (time.Duration, time.Duration) {
	minInterval, maxInterval := l.minInterval, l.maxInterval
	// Preserve the behavior of zero-value/test literals that only set interval.
	if minInterval == 0 && maxInterval == 0 && l.interval != 0 {
		minInterval, maxInterval = l.interval, l.interval
	}
	if minInterval < 0 {
		minInterval = 0
	}
	if maxInterval < minInterval {
		maxInterval = minInterval
	}
	return minInterval, maxInterval
}

func (l *WesternSearchLimiter) nextIntervalLocked(minInterval, maxInterval time.Duration) time.Duration {
	if maxInterval <= minInterval {
		return minInterval
	}
	span := int64(maxInterval - minInterval)
	if span <= 0 {
		return minInterval
	}
	randomInt63n := l.randomInt63n
	if randomInt63n == nil {
		randomInt63n = rand.Int63n
	}
	return minInterval + time.Duration(randomInt63n(span+1))
}

// Wait blocks until a randomly selected configured interval has elapsed since
// the last search. Calls are serialized so concurrent engine searches cannot
// bypass the delay.
func (l *WesternSearchLimiter) Wait() {
	l.mu.Lock()
	defer l.mu.Unlock()

	minInterval, maxInterval := l.intervalRangeLocked()
	if maxInterval <= 0 {
		l.lastTime = l.now()
		return
	}

	interval := l.nextIntervalLocked(minInterval, maxInterval)
	now := l.now()
	if !l.lastTime.IsZero() {
		elapsed := now.Sub(l.lastTime)
		if elapsed < interval {
			l.sleep(interval - elapsed)
		}
	}
	l.lastTime = l.now()
}
