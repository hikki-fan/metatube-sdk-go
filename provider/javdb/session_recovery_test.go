package javdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

const javDBRecoveryMovieHTML = `
<!DOCTYPE html>
<html><body>
  <div class="video-detail"><h2 class="title">
    <strong>ABP-123</strong><strong class="current-title">Recovered Movie</strong>
  </h2></div>
  <div class="column-video-cover"><img class="video-cover" src="https://images.example/recovered.jpg"></div>
  <nav class="movie-panel-info"><div class="panel-block">
    <strong>番號:</strong><span class="value">ABP-123</span>
  </div></nav>
</body></html>`

const javDBRecoverySearchHTML = `
<!DOCTYPE html>
<html><body><div class="movie-list"><div class="item">
  <a class="box" href="/v/RecoveredID" title="Recovered Search Movie">
    <div class="cover"><img src="https://images.example/search.jpg"></div>
    <div class="video-title"><strong>ABP-123</strong></div>
    <div class="meta">2024-01-02</div>
  </a>
</div></div></body></html>`

const javDBExplicitLoginFormHTML = `<form action="/login" method="post">
  <input type="text" name="username">
  <input type="password" name="password">
</form>`

const javDBExplicitLoginPageHTML = `<!DOCTYPE html><html><body>` + javDBExplicitLoginFormHTML + `</body></html>`

func javDBRecoveryPageWithLoginModal(page string) string {
	return strings.Replace(page, "</body>", javDBExplicitLoginFormHTML+"</body>", 1)
}

func newJavDBRecoveryCookieCloud(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Bool) {
	t.Helper()

	var requests atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if fail.Load() {
			http.Error(w, "CookieCloud unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	t.Cleanup(server.Close)
	return server, &requests, &fail
}

func newJavDBRecoveryClient(
	t *testing.T,
	mirrorURL string,
	cookieCloudServer *httptest.Server,
	cookieCloudRequests *atomic.Int32,
) *JavDB {
	t.Helper()

	config := envconfig.NewConfig()
	config.Set("mirror_base_url", mirrorURL+"/api")
	config.Set("mirror_timeout", "1s")
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")
	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()
	require.NoError(t, jdb.ensureCookieCloudCookies())
	cookieCloudRequests.Store(0)
	return jdb
}

func newJavDBUnavailableMirror(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "mirror unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestJavDBMovieLoginRedirectRefreshRetriesOnce(t *testing.T) {
	mirrorServer, mirrorRequests := newJavDBUnavailableMirror(t)
	cookieCloudServer, cookieCloudRequests, _ := newJavDBRecoveryCookieCloud(t)
	var movieRequests atomic.Int32
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/MixedID":
			if movieRequests.Add(1) == 1 {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(javDBRecoveryPageWithLoginModal(javDBRecoveryMovieHTML)))
		case "/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(javDBExplicitLoginPageHTML))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(originServer.Close)

	jdb := newJavDBRecoveryClient(t, mirrorServer.URL, cookieCloudServer, cookieCloudRequests)
	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/MixedID", originServer.URL))
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "ABP-123", info.Number)
	assert.Equal(t, int32(2), movieRequests.Load())
	assert.Equal(t, int32(1), cookieCloudRequests.Load())
	assert.Equal(t, int32(1), mirrorRequests.Load())
}

func TestJavDBSearchLoginFormRefreshRetriesOnce(t *testing.T) {
	mirrorServer, mirrorRequests := newJavDBUnavailableMirror(t)
	cookieCloudServer, cookieCloudRequests, _ := newJavDBRecoveryCookieCloud(t)
	var searchRequests atomic.Int32
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if searchRequests.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(javDBExplicitLoginFormHTML))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(javDBRecoveryPageWithLoginModal(javDBRecoverySearchHTML)))
	}))
	t.Cleanup(originServer.Close)

	jdb := newJavDBRecoveryClient(t, mirrorServer.URL, cookieCloudServer, cookieCloudRequests)
	jdb.searchURL = originServer.URL + "/search?f=all&q=%s"
	results, err := jdb.SearchMovie("ABP-123")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "RecoveredID", results[0].ID)
	assert.Equal(t, int32(2), searchRequests.Load())
	assert.Equal(t, int32(1), cookieCloudRequests.Load())
	assert.Equal(t, int32(1), mirrorRequests.Load())
}

func TestJavDBLoginRefreshFailureReturnsLoginError(t *testing.T) {
	mirrorServer, mirrorRequests := newJavDBUnavailableMirror(t)
	cookieCloudServer, cookieCloudRequests, failCookieCloud := newJavDBRecoveryCookieCloud(t)
	var movieRequests atomic.Int32
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v/MixedID" {
			movieRequests.Add(1)
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(javDBExplicitLoginPageHTML))
	}))
	t.Cleanup(originServer.Close)

	jdb := newJavDBRecoveryClient(t, mirrorServer.URL, cookieCloudServer, cookieCloudRequests)
	failCookieCloud.Store(true)
	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/MixedID", originServer.URL))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLoginRequired)
	assert.Nil(t, info)
	assert.Equal(t, int32(1), movieRequests.Load(), "failed session refresh must not retry the source")
	assert.Equal(t, int32(1), cookieCloudRequests.Load())
	assert.Equal(t, int32(1), mirrorRequests.Load())
}

func TestJavDBMirrorFallbackPreservesForbiddenRecovery(t *testing.T) {
	mirrorServer, mirrorRequests := newJavDBUnavailableMirror(t)
	cookieCloudServer, cookieCloudRequests, _ := newJavDBRecoveryCookieCloud(t)
	var movieRequests atomic.Int32
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if movieRequests.Add(1) == 1 {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(javDBRecoveryMovieHTML))
	}))
	t.Cleanup(originServer.Close)

	jdb := newJavDBRecoveryClient(t, mirrorServer.URL, cookieCloudServer, cookieCloudRequests)
	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/MixedID", originServer.URL))
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "ABP-123", info.Number)
	assert.Equal(t, int32(2), movieRequests.Load())
	assert.Equal(t, int32(1), cookieCloudRequests.Load())
	assert.Equal(t, int32(1), mirrorRequests.Load())
}

func TestJavDBLoginModalDoesNotHideValidMovieContent(t *testing.T) {
	var movieRequests atomic.Int32
	var searchRequests atomic.Int32
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/v/MixedID":
			movieRequests.Add(1)
			_, _ = w.Write([]byte(javDBRecoveryPageWithLoginModal(javDBRecoveryMovieHTML)))
		case "/search":
			searchRequests.Add(1)
			_, _ = w.Write([]byte(javDBRecoveryPageWithLoginModal(javDBRecoverySearchHTML)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(originServer.Close)

	jdb := New()
	jdb.searchURL = originServer.URL + "/search?f=all&q=%s"
	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/MixedID", originServer.URL))
	require.NoError(t, err)
	assert.Equal(t, "ABP-123", info.Number)
	results, err := jdb.SearchMovie("ABP-123")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "RecoveredID", results[0].ID)
	assert.Equal(t, int32(1), movieRequests.Load())
	assert.Equal(t, int32(1), searchRequests.Load())
}

func TestJavDBLoginRedirectWithoutFormIsDetected(t *testing.T) {
	var movieRequests atomic.Int32
	var loginRequests atomic.Int32
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/MixedID":
			movieRequests.Add(1)
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/login":
			loginRequests.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><html><body>Sign in to continue</body></html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(originServer.Close)

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/MixedID", originServer.URL))
	require.ErrorIs(t, err, ErrLoginRequired)
	assert.Nil(t, info)
	assert.Equal(t, int32(1), movieRequests.Load())
	assert.Equal(t, int32(1), loginRequests.Load())
}

func TestJavDBConcurrentFailedCookieCloudRefreshIsDeduplicated(t *testing.T) {
	mirrorServer, _ := newJavDBUnavailableMirror(t)
	cookieCloudServer, cookieCloudRequests, failCookieCloud := newJavDBRecoveryCookieCloud(t)
	jdb := newJavDBRecoveryClient(t, mirrorServer.URL, cookieCloudServer, cookieCloudRequests)
	failCookieCloud.Store(true)

	const workers = 12
	start := make(chan struct{})
	ready := make(chan struct{}, workers)
	errs := make(chan error, workers)
	attemptStart := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-start
			errs <- jdb.forceCookieCloudRefresh(attemptStart)
		}()
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.Error(t, err)
	}
	assert.Equal(t, int32(1), cookieCloudRequests.Load(), "concurrent callers should share the failed refresh result")
}
