package javdb

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

func TestWesternSearchLimiter_Wait_Serialization(t *testing.T) {
	const interval = 20 * time.Millisecond
	limiter := NewWesternSearchLimiter(interval)

	const goroutines = 3
	var wg sync.WaitGroup
	startTimes := make([]time.Time, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			limiter.Wait()
			startTimes[idx] = time.Now()
		}(i)
	}
	wg.Wait()

	// Verify all goroutines executed
	for i := 0; i < goroutines; i++ {
		assert.False(t, startTimes[i].IsZero())
	}
	slices.SortFunc(startTimes, func(a, b time.Time) int { return a.Compare(b) })
	for i := 1; i < len(startTimes); i++ {
		assert.GreaterOrEqual(t, startTimes[i].Sub(startTimes[i-1]), interval-time.Millisecond)
	}
}

func TestWesternSearchLimiter_SimulatedClock(t *testing.T) {
	currentTime := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	var timeMu sync.Mutex
	getTime := func() time.Time {
		timeMu.Lock()
		defer timeMu.Unlock()
		return currentTime
	}

	var sleepCalls int64
	var totalSlept time.Duration
	limiter := &WesternSearchLimiter{
		interval: 2 * time.Second,
		now:      getTime,
		sleep: func(d time.Duration) {
			atomic.AddInt64(&sleepCalls, 1)
			timeMu.Lock()
			currentTime = currentTime.Add(d)
			totalSlept += d
			timeMu.Unlock()
		},
	}

	// First call should not sleep
	limiter.Wait()
	assert.Equal(t, int64(0), atomic.LoadInt64(&sleepCalls))

	// Second immediate call must sleep for the full 2s interval
	limiter.Wait()
	assert.Equal(t, int64(1), atomic.LoadInt64(&sleepCalls))
	assert.Equal(t, 2*time.Second, totalSlept)

	// Advance time by 5s (> 2s interval), next call should not sleep
	timeMu.Lock()
	currentTime = currentTime.Add(5 * time.Second)
	timeMu.Unlock()

	limiter.Wait()
	assert.Equal(t, int64(1), atomic.LoadInt64(&sleepCalls))
}

func TestWesternSearchLimiter_IntervalOverride(t *testing.T) {
	jdb := New()
	assert.Equal(t, DefaultWesternSearchInterval, jdb.getWesternLimiter().Interval())
	minInterval, maxInterval := jdb.getWesternLimiter().IntervalRange()
	assert.Equal(t, DefaultWesternSearchMinInterval, minInterval)
	assert.Equal(t, DefaultWesternSearchMaxInterval, maxInterval)

	jdb.SetWesternSearchInterval(10 * time.Millisecond)
	assert.Equal(t, 10*time.Millisecond, jdb.getWesternLimiter().Interval())
	minInterval, maxInterval = jdb.getWesternLimiter().IntervalRange()
	assert.Equal(t, 10*time.Millisecond, minInterval)
	assert.Equal(t, 10*time.Millisecond, maxInterval)

	customLimiter := NewWesternSearchLimiter(5 * time.Millisecond)
	jdb.SetWesternSearchLimiter(customLimiter)
	assert.Equal(t, 5*time.Millisecond, jdb.getWesternLimiter().Interval())
}

func TestWesternSearchLimiter_ConfigOverride(t *testing.T) {
	newLimiterConfig := func(value string) *envconfig.Config {
		config := envconfig.NewConfig()
		config.Set("western_search_interval", value)
		return config
	}

	jdb := New()
	err := jdb.SetConfig(newLimiterConfig("12ms"))
	assert.NoError(t, err)
	assert.Equal(t, 12*time.Millisecond, jdb.getWesternLimiter().Interval())
	minInterval, maxInterval := jdb.getWesternLimiter().IntervalRange()
	assert.Equal(t, 12*time.Millisecond, minInterval)
	assert.Equal(t, 12*time.Millisecond, maxInterval)

	err = jdb.SetConfig(newLimiterConfig("10s-25s"))
	assert.NoError(t, err)
	minInterval, maxInterval = jdb.getWesternLimiter().IntervalRange()
	assert.Equal(t, 10*time.Second, minInterval)
	assert.Equal(t, 25*time.Second, maxInterval)

	err = jdb.SetConfig(newLimiterConfig("not-a-duration"))
	assert.Error(t, err)
}

func TestParseWesternSearchInterval(t *testing.T) {
	tests := []struct {
		value       string
		minInterval time.Duration
		maxInterval time.Duration
		wantErr     bool
	}{
		{value: "10s", minInterval: 10 * time.Second, maxInterval: 10 * time.Second},
		{value: " 10s - 25s ", minInterval: 10 * time.Second, maxInterval: 25 * time.Second},
		{value: "25s-10s", wantErr: true},
		{value: "-1s-10s", wantErr: true},
		{value: "not-a-duration", wantErr: true},
	}

	for _, tc := range tests {
		minInterval, maxInterval, err := parseWesternSearchInterval(tc.value)
		if tc.wantErr {
			assert.Error(t, err, tc.value)
			continue
		}
		assert.NoError(t, err, tc.value)
		assert.Equal(t, tc.minInterval, minInterval, tc.value)
		assert.Equal(t, tc.maxInterval, maxInterval, tc.value)
	}
}

func TestWesternSearchLimiter_RandomRange(t *testing.T) {
	currentTime := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	limiter := NewRandomWesternSearchLimiter(10*time.Second, 25*time.Second)
	limiter.now = func() time.Time { return currentTime }
	var slept time.Duration
	limiter.sleep = func(d time.Duration) {
		slept += d
		currentTime = currentTime.Add(d)
	}

	// Select the lower bound, then the upper bound, to verify the inclusive
	// range without making the test depend on the random seed.
	limiter.randomInt63n = func(n int64) int64 { return 0 }
	limiter.Wait()
	limiter.Wait()
	assert.Equal(t, 10*time.Second, slept)

	limiter.randomInt63n = func(n int64) int64 { return n - 1 }
	limiter.Wait()
	assert.Equal(t, 35*time.Second, slept)
}

func TestWesternSearchLimiter_ZeroInterval(t *testing.T) {
	limiter := NewWesternSearchLimiter(0)
	start := time.Now()
	for i := 0; i < 5; i++ {
		limiter.Wait()
	}
	assert.Less(t, time.Since(start), 50*time.Millisecond)
}

func TestWesternSearchLimiter_Scope(t *testing.T) {
	westernCases := []string{
		"BrazzersExxtras.24.01.15",
		"BrazzersExxtras.24.01.15-C",
		"BrazzersExxtras.24.01.15-UC",
		"brazzersexxtras.24.01.15",
		"Twistys.23.11.05",
		"Tushy.22.03.14",
		"Sweet-Sinner.24.01.15",
		"21Sextury.20.05.10",
	}

	for _, tc := range westernCases {
		assert.True(t, number.IsWestern(tc), "Expected %s to be recognized as Western scene", tc)
	}

	nonWesternCases := []string{
		"MGMJ-070",
		"GANA-1928",
		"FC2-123456",
		"FC2-PPV-123456",
		"ABP-030",
		"ABP-030-C",
		"ssis00123",
		"020317_001",
		"ordinary movie title",
		"1080p",
		"192.168.50.100",
		"Studio.24.13.15",
		"Studio.24.01.32",
		"",
	}

	for _, tc := range nonWesternCases {
		assert.False(t, number.IsWestern(tc), "Expected %s to NOT be recognized as Western scene", tc)
	}
}
