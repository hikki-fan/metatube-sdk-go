package javdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

func TestMirrorClientMapsActorDetailAndAvatarWithoutSourceCredentials(t *testing.T) {
	const actorID = "MiXeDActorID"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/actors/"+actorID, r.URL.Path)
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": 1,
			"data": map[string]any{
				"actor": map[string]any{
					"id": actorID, "name": "Actor Name", "summary": "Actor summary",
					"avatar_url": "https://images.example/actor.jpg",
					"aliases":    []string{"Alias A"},
				},
				"tags": []map[string]any{{"id": "1", "name": "Tag"}},
			},
		})
	}))
	t.Cleanup(server.Close)

	client, err := newMirrorClient(server.URL+"/api", "", time.Second)
	require.NoError(t, err)
	first, err := client.GetActorInfoByID(actorID)
	require.NoError(t, err)
	second, err := client.GetActorInfoByID(actorID)
	require.NoError(t, err)

	assert.Equal(t, actorID, first.ID)
	assert.Equal(t, ActorProviderName, first.Provider)
	assert.Equal(t, "https://javdb.com/actors/"+actorID, first.Homepage)
	assert.Equal(t, "Actor Name", first.Name)
	assert.Equal(t, "Actor summary", first.Summary)
	assert.Equal(t, []string{"Alias A"}, []string(first.Aliases))
	assert.Equal(t, []string{"https://images.example/actor.jpg"}, []string(first.Images))
	assert.Equal(t, first.Images, second.Images)
	assert.Equal(t, int32(1), requests.Load(), "actor details should use the mirror cache")
}

func TestJavDBActorMirrorOnlyUsesMirrorForDetailAndSearch(t *testing.T) {
	const actorID = "MirrorActorID"
	var sourceRequests atomic.Int32
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sourceRequests.Add(1)
		http.Error(w, "source must not be called", http.StatusInternalServerError)
	}))
	t.Cleanup(sourceServer.Close)

	mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/actors/" + actorID:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": 1,
				"data": map[string]any{"actor": map[string]any{
					"id": actorID, "name": "Mirror Actor", "avatar_url": "https://images.example/avatar.jpg",
				}},
			})
		case "/v2/search":
			assert.Equal(t, "水野朝陽", r.URL.Query().Get("q"))
			assert.Equal(t, "1", r.URL.Query().Get("page"))
			assert.Equal(t, "actor", r.URL.Query().Get("type"))
			assert.Equal(t, "24", r.URL.Query().Get("limit"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": 1,
				"data": map[string]any{"actors": []map[string]any{
					{
						"id": "35re", "name": "Asahi Mizuno", "name_zht": "水野朝陽",
						"other_name": "水野朝陽,Asahi Mizuno", "gender": 0,
						"avatar_url": "https://images.example/35re.jpg",
					},
					{
						"id": "X9ne", "name": "Asahi Mizuno II", "name_zht": "水野朝陽二",
						"other_name": []string{"Alias B", "Alias C"}, "gender": 0,
						"avatar_url": "https://images.example/X9ne.jpg",
					},
					{"id": "MaleActor", "name": "Male actor", "gender": 1},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mirrorServer.Close)

	config := envconfig.NewConfig()
	config.Set("mirror_base_url", mirrorServer.URL)
	config.Set("mirror_timeout", "1s")
	actor := NewActor()
	actor.pageEndpoint = sourceServer.URL + "/actors/%s"
	actor.searchEndpoint = sourceServer.URL + "/search?f=actor&q=%s"
	require.NoError(t, actor.SetConfig(config))

	info, err := actor.GetActorInfoByID(actorID)
	require.NoError(t, err)
	assert.Equal(t, actorID, info.ID)
	assert.Equal(t, []string{"https://images.example/avatar.jpg"}, []string(info.Images))
	results, err := actor.SearchActor("水野朝陽")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, []string{"35re", "X9ne"}, []string{results[0].ID, results[1].ID})
	assert.Equal(t, ActorProviderName, results[0].Provider)
	assert.Equal(t, "https://javdb.com/actors/35re", results[0].Homepage)
	assert.Equal(t, []string{"水野朝陽"}, []string(results[0].Aliases))
	assert.Equal(t, []string{"https://images.example/35re.jpg"}, []string(results[0].Images))
	assert.Equal(t, []string{"水野朝陽二", "Alias B", "Alias C"}, []string(results[1].Aliases))
	assert.Zero(t, sourceRequests.Load())
}

func TestJavDBActorMirrorOnlyPropagatesSearchErrorsWithoutSourceFallback(t *testing.T) {
	var sourceRequests atomic.Int32
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sourceRequests.Add(1)
		http.Error(w, "source must not be called", http.StatusInternalServerError)
	}))
	t.Cleanup(sourceServer.Close)

	mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "mirror unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(mirrorServer.Close)

	config := envconfig.NewConfig()
	config.Set("mirror_base_url", mirrorServer.URL)
	config.Set("mirror_timeout", "1s")
	actor := NewActor()
	actor.searchEndpoint = sourceServer.URL + "/search?f=actor&q=%s"
	require.NoError(t, actor.SetConfig(config))

	_, err := actor.SearchActor("lookup")
	require.ErrorIs(t, err, ErrMirrorInvalidResponse)
	assert.Zero(t, sourceRequests.Load())
}

func TestJavDBActorMirrorFallbackSearchUsesSourceAfterMirrorError(t *testing.T) {
	var sourceRequests atomic.Int32
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sourceRequests.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><div class="actors"><div class="box">
			<a href="/actors/NativeID"><strong class="name">Native Actor</strong></a>
		</div></div></body></html>`))
	}))
	t.Cleanup(sourceServer.Close)

	mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "mirror unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(mirrorServer.Close)

	config := envconfig.NewConfig()
	config.Set("mirror_base_url", mirrorServer.URL)
	config.Set("mirror_timeout", "1s")
	config.Set("mirror_mode", mirrorModeFallback)
	actor := NewActor()
	actor.searchEndpoint = sourceServer.URL + "/search?f=actor&q=%s"
	require.NoError(t, actor.SetConfig(config))

	results, err := actor.SearchActor("Native Actor")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "NativeID", results[0].ID)
	assert.Equal(t, ActorProviderName, results[0].Provider)
	assert.Equal(t, int32(1), sourceRequests.Load())
}
