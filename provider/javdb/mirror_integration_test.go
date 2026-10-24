package javdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

func newJavDBMirrorIntegrationServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		movie := map[string]any{
			"id":        "MiXeDopaqueID",
			"type":      0,
			"number":    "ABP-123",
			"title":     "Mirror integration fixture",
			"cover_url": "https://images.example/mirror.jpg",
		}
		switch r.URL.Path {
		case "/api/v2/search":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": 1,
				"data": map[string]any{
					"movies": []map[string]any{movie},
				},
			})
		case "/api/v4/movies/MiXeDopaqueID":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": 1,
				"data":    map[string]any{"movie": movie},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestJavDBMirrorDefaultsAndCustomOriginOptIn(t *testing.T) {
	jdb := New()
	assert.True(t, jdb.mirrorEnabled)
	assert.Equal(t, defaultMirrorBaseURL, jdb.mirrorBaseURL)
	assert.Equal(t, 8*time.Second, jdb.mirrorTimeout)
	assert.False(t, jdb.shouldUseMirror("http://127.0.0.1:12345/v/Fixture"))
	assert.True(t, jdb.shouldUseMirror("https://javdb.com/v/Fixture"))
	assert.True(t, jdb.shouldUseMirror(jdb.searchURL))

	server, _ := newJavDBMirrorIntegrationServer(t)
	config := envconfig.NewConfig()
	config.Set("mirror_base_url", server.URL+"/api")
	config.Set("mirror_timeout", "250ms")
	require.NoError(t, jdb.SetConfig(config))
	assert.Equal(t, 250*time.Millisecond, jdb.mirrorTimeout)
	assert.True(t, jdb.shouldUseMirror("http://127.0.0.1:12345/v/Fixture"))

	disabled := envconfig.NewConfig()
	disabled.Set("mirror_enabled", "false")
	require.NoError(t, jdb.SetConfig(disabled))
	assert.False(t, jdb.shouldUseMirror("https://javdb.com/v/Fixture"))
}

func TestJavDBMirrorHitSkipsOriginAndCookieCloud(t *testing.T) {
	mirrorServer, mirrorRequests := newJavDBMirrorIntegrationServer(t)
	var originRequests atomic.Int32
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		http.Error(w, "origin must not be called on mirror hit", http.StatusInternalServerError)
	}))
	t.Cleanup(originServer.Close)

	var cookieCloudRequests atomic.Int32
	cookieCloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCloudRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	t.Cleanup(cookieCloudServer.Close)

	config := envconfig.NewConfig()
	config.Set("mirror_base_url", mirrorServer.URL+"/api")
	config.Set("mirror_timeout", "1s")
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")
	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.searchURL = originServer.URL + "/search?f=all&q=%s"
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()

	results, err := jdb.SearchMovie("ABP-123")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "MiXeDopaqueID", results[0].ID)
	assert.Equal(t, "JavDB", results[0].Provider)

	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/MiXeDopaqueID", originServer.URL))
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "MiXeDopaqueID", info.ID)
	assert.Equal(t, "JavDB", info.Provider)

	assert.Zero(t, originRequests.Load())
	assert.Zero(t, cookieCloudRequests.Load())
	assert.GreaterOrEqual(t, mirrorRequests.Load(), int32(2), "search and direct detail should use mirror")
}
