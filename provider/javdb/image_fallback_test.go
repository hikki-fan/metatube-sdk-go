package javdb

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
)

const imageFallbackMovieHTML = `<!DOCTYPE html><html><body>
<div class="video-detail"><h2 class="title"><strong>ABP-123</strong><strong class="current-title">Fallback Movie</strong></h2></div>
<div class="column-video-cover"><img class="video-cover" src="https://images.example/origin-cover.jpg"></div>
<nav class="movie-panel-info"><div class="panel-block"><strong>番號:</strong><span class="value">ABP-123</span></div></nav>
</body></html>`

type countingJavDBMirror struct{ calls atomic.Int32 }

func (m *countingJavDBMirror) SearchMovie(string) ([]*model.MovieSearchResult, error) {
	m.calls.Add(1)
	return nil, nil
}

func (m *countingJavDBMirror) GetMovieInfoByID(string) (*model.MovieInfo, error) {
	m.calls.Add(1)
	return nil, nil
}

func TestJavDBMovieImageFallbackUsesOriginAndCachesProjection(t *testing.T) {
	var originRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		assert.Equal(t, "/v/MixedCase-7", r.URL.Path)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(imageFallbackMovieHTML))
	}))
	t.Cleanup(server.Close)

	jdb := New()
	jdb.movieURL = server.URL + "/v/%s"
	mirror := &countingJavDBMirror{}
	jdb.setMirrorClient(mirror)

	first, err := jdb.GetMovieImageFallbackURLsByID("MixedCase-7")
	require.NoError(t, err)
	require.Equal(t, []string{"https://images.example/origin-cover.jpg"}, first)
	second, err := jdb.GetMovieImageFallbackURLsByID("MixedCase-7")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, int32(1), originRequests.Load())
	assert.Equal(t, int32(0), mirror.calls.Load(), "image projection must bypass the mirror")

	jdb.imageFallbackMu.Lock()
	entry, ok := jdb.imageFallbackCache["MixedCase-7"]
	jdb.imageFallbackMu.Unlock()
	require.True(t, ok)
	assert.True(t, entry.expiresAt.After(time.Now()))
	assert.Equal(t, []string{"https://images.example/origin-cover.jpg"}, entry.urls)
}

func TestJavDBMovieImageFallbackRejectsRedirectToDifferentMovie(t *testing.T) {
	var originRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		if r.URL.Path == "/v/RequestedID" {
			http.Redirect(w, r, "/v/OtherID", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(imageFallbackMovieHTML))
	}))
	t.Cleanup(server.Close)

	jdb := New()
	jdb.movieURL = server.URL + "/v/%s"
	_, err := jdb.GetMovieImageFallbackURLsByID("RequestedID")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatched movie")
	assert.Equal(t, int32(2), originRequests.Load())
}

func TestJavDBMovieImageFallbackMergesConcurrentRequests(t *testing.T) {
	var originRequests atomic.Int32
	const followers = 8
	started := make(chan struct{}, followers+1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		started <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(imageFallbackMovieHTML))
	}))
	t.Cleanup(server.Close)

	jdb := New()
	jdb.movieURL = server.URL + "/v/%s"
	var wg sync.WaitGroup
	errs := make(chan error, followers+1)
	results := make(chan []string, followers+1)
	lookup := func() {
		defer wg.Done()
		urls, err := jdb.GetMovieImageFallbackURLsByID("ConcurrentID")
		results <- urls
		errs <- err
	}

	wg.Add(1)
	go lookup()
	<-started
	for i := 0; i < followers; i++ {
		wg.Add(1)
		go lookup()
	}
	// Keep the origin response open while followers enter the in-flight call.
	time.Sleep(25 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
	for urls := range results {
		assert.Equal(t, []string{"https://images.example/origin-cover.jpg"}, urls)
	}
	assert.Equal(t, int32(1), originRequests.Load())
}

func TestJavDBMovieImageFallbackNegativeCacheAndRejectsInvalidID(t *testing.T) {
	var originRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		originRequests.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>no movie cover</body></html>"))
	}))
	t.Cleanup(server.Close)

	jdb := New()
	jdb.movieURL = server.URL + "/v/%s"
	for i := 0; i < 2; i++ {
		_, err := jdb.GetMovieImageFallbackURLsByID("NoCoverID")
		require.ErrorIs(t, err, provider.ErrImageNotFound)
	}
	assert.Equal(t, int32(1), originRequests.Load(), "a missing image result should be negatively cached")

	for _, id := range []string{"", "../NoCoverID", "Mixed/Case", " NoCoverID "} {
		_, err := jdb.GetMovieImageFallbackURLsByID(id)
		require.ErrorIs(t, err, provider.ErrInvalidID)
	}
	assert.Equal(t, int32(1), originRequests.Load())
}
