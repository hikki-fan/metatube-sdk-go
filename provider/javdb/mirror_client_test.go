package javdb

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mirrorTestMovie(id, movieNumber string, movieType int) map[string]any {
	return map[string]any{
		"id": id, "type": movieType, "number": movieNumber,
		"title": "Mirror fixture", "cover_url": "https://images.example/cover.jpg",
	}
}

func mirrorTestWriteJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestMirrorClientMapsDetailAndUsesIsolatedSignedRequest(t *testing.T) {
	const id = "MiXeDopaqueID"
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		detailRequests.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v4/movies/"+id, r.URL.Path)
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		assert.Equal(t, "https://catembylegacy.fastcdn.dpdns.org", r.Header.Get("Origin"))
		signature := strings.Split(r.Header.Get("jdsignature"), ".")
		if len(signature) != 3 {
			assert.Len(t, signature, 3)
			return
		}
		assert.Equal(t, mirrorClientID, signature[1])
		timestamp, err := strconv.ParseInt(signature[0], 10, 64)
		if !assert.NoError(t, err) {
			return
		}
		digest := md5.Sum([]byte(signature[0] + mirrorSignatureSalt))
		assert.Equal(t, hex.EncodeToString(digest[:]), signature[2])
		assert.WithinDuration(t, time.Now(), time.Unix(timestamp, 0), time.Minute)

		mirrorTestWriteJSON(w, map[string]any{
			"success": 1,
			"data": map[string]any{"movie": map[string]any{
				"id": id, "type": 1, "number": "HEYZO-1234", "title": "潮吹き床上浸水",
				"cover_url": "https://images.example/cover.jpg", "thumb_url": "https://images.example/thumb.jpg",
				"score": "4.57", "duration": 61, "release_date": "2014-05-10",
				"summary": "summary", "maker_name": "maker", "director_name": "director",
				"publisher_name": "publisher", "series_name": "series",
				"preview_video_url": "https://video.example/preview.mp4",
				"actors": []map[string]any{
					{"id": "female", "name": "二宮ナナ", "gender": 0, "avatar_url": "https://images.example/actor.jpg"},
					{"id": "male", "name": "male actor", "gender": 1},
					{"id": "unknown", "name": "unknown gender", "avatar_url": "https://images.example/unknown.jpg"},
				},
				"tags":           []map[string]any{{"id": "73", "name": "痴女"}, {"id": "74", "name": "企畫"}},
				"preview_images": []map[string]any{{"thumb_url": "https://images.example/preview-small.jpg", "large_url": "https://images.example/preview-large.jpg"}},
			}},
		})
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL+"/api", "", time.Second)
	require.NoError(t, err)
	assert.NotNil(t, client.httpClient.Transport.(*http.Transport).Proxy)

	info, err := client.GetMovieInfoByID(id)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, id, info.ID)
	assert.Equal(t, "JavDB", info.Provider)
	assert.Equal(t, "https://javdb.com/v/"+id, info.Homepage)
	assert.Equal(t, "HEYZO-1234", info.Number)
	assert.Equal(t, "https://images.example/cover.jpg", info.CoverURL)
	assert.Equal(t, "https://images.example/thumb.jpg", info.ThumbURL)
	assert.Equal(t, 4.57, info.Score)
	assert.Equal(t, 61, info.Runtime)
	assert.Equal(t, "director", info.Director)
	assert.Equal(t, "maker", info.Maker)
	assert.Equal(t, "publisher", info.Label)
	assert.Equal(t, "series", info.Series)
	assert.Equal(t, []string{"二宮ナナ"}, []string(info.Actors))
	assert.Equal(t, []string{"痴女", "企划"}, []string(info.Genres))
	assert.Equal(t, []string{"https://images.example/preview-large.jpg"}, []string(info.PreviewImages))
	assert.True(t, info.IsValid())
	assert.Equal(t, int32(1), detailRequests.Load(), "positive detail results should be cached")
}

func TestMirrorClientSearchNormalizesFC2AndFetchesExactDetail(t *testing.T) {
	const id = "Fc2OpaqueID"
	searchQuery := make(chan string, 1)
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/search":
			searchQuery <- r.URL.Query().Get("q")
			assert.Equal(t, "1", r.URL.Query().Get("page"))
			assert.Equal(t, "movie", r.URL.Query().Get("type"))
			assert.Equal(t, "24", r.URL.Query().Get("limit"))
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data":    map[string]any{"movies": []map[string]any{mirrorTestMovie(id, "FC2-3972042", 3)}},
			})
		case "/api/v4/movies/" + id:
			detailRequests.Add(1)
			movie := mirrorTestMovie(id, "FC2-3972042", 3)
			movie["thumb_url"] = "https://images.example/fc2-thumb.jpg"
			mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"movie": movie}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL+"/api", "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("FC2-PPV-3972042-C")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "FC2-3972042", <-searchQuery)
	assert.Equal(t, id, results[0].ID)
	assert.Equal(t, "FC2-3972042", results[0].Number)
	assert.Equal(t, "JavDB", results[0].Provider)
	assert.Equal(t, "https://javdb.com/v/"+id, results[0].Homepage)
	assert.Equal(t, int32(1), detailRequests.Load())
}

func TestMirrorClientSearchKeepsFullNumberIdentity(t *testing.T) {
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data": map[string]any{"movies": []map[string]any{
					mirrorTestMovie("NearMovie", "T38-072", 0),
				}},
			})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v4/movies/") {
			detailRequests.Add(1)
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("T38-047")
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Zero(t, detailRequests.Load())
}

func TestMirrorClientAllowsUncensoredGenericStudioType(t *testing.T) {
	const id = "SkyMovie"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data":    map[string]any{"movies": []map[string]any{mirrorTestMovie(id, "SKYHD-104", 1)}},
			})
			return
		}
		mirrorTestWriteJSON(w, map[string]any{
			"success": 1,
			"data":    map[string]any{"movie": mirrorTestMovie(id, "SKYHD-104", 1)},
		})
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("SKYHD-104")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, id, results[0].ID)
}

func TestMirrorClientAllowsKnownUncensoredPrefixType(t *testing.T) {
	const id = "CWPBDMovie"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data":    map[string]any{"movies": []map[string]any{mirrorTestMovie(id, "CWPBD-104", 1)}},
			})
			return
		}
		mirrorTestWriteJSON(w, map[string]any{
			"success": 1,
			"data":    map[string]any{"movie": mirrorTestMovie(id, "CWPBD-104", 1)},
		})
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("CWPBD-104")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, id, results[0].ID)
}

func TestMirrorClientRejectsAmbiguousExactSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data": map[string]any{"movies": []map[string]any{
					mirrorTestMovie("MovieA", "ABP-123", 0),
					mirrorTestMovie("MovieB", "ABP-123", 0),
				}},
			})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v4/movies/")
		mirrorTestWriteJSON(w, map[string]any{
			"success": 1,
			"data":    map[string]any{"movie": mirrorTestMovie(id, "ABP-123", 0)},
		})
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	_, err = client.SearchMovie("ABP-123")
	assert.ErrorIs(t, err, ErrMirrorAmbiguous)
}

func TestMirrorClientRejectsInvalidSuccessAndResponseLimit(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
	}{
		{
			name: "boolean success",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"success":true,"data":{"movie":{}}}`))
			},
			wantErr: ErrMirrorBusiness,
		},
		{
			name: "oversized response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(mirrorMaxResponse+1))
				w.WriteHeader(http.StatusOK)
			},
			wantErr: ErrMirrorInvalidResponse,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			t.Cleanup(server.Close)
			client, err := newMirrorClient(server.URL, "", time.Second)
			require.NoError(t, err)
			_, err = client.GetMovieInfoByID("MovieID")
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestMirrorClientSearchDuplicateIDsAreFetchedOnce(t *testing.T) {
	const id = "UniqueMovie"
	var searchRequests atomic.Int32
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			searchRequests.Add(1)
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data": map[string]any{"movies": []map[string]any{
					mirrorTestMovie(id, "ABP-123", 0),
					mirrorTestMovie(id, "ABP-123", 0),
				}},
			})
			return
		}
		detailRequests.Add(1)
		mirrorTestWriteJSON(w, map[string]any{
			"success": 1,
			"data":    map[string]any{"movie": mirrorTestMovie(id, "ABP-123", 0)},
		})
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("ABP-123")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, int32(1), searchRequests.Load())
	assert.Equal(t, int32(1), detailRequests.Load())
}

func TestMirrorClientConstructorValidatesBaseAndProxy(t *testing.T) {
	_, err := newMirrorClient("file:///tmp/mirror", "", time.Second)
	assert.Error(t, err)
	_, err = newMirrorClient("https://user:pass@example.com/api", "", time.Second)
	assert.Error(t, err)
	_, err = newMirrorClient("https://example.com/api", "invalid proxy", time.Second)
	assert.Error(t, err)
	client, err := newMirrorClient("https://example.com/api", "http://127.0.0.1:8080", time.Second)
	assert.NoError(t, err)
	assert.NotNil(t, client.httpClient.Transport.(*http.Transport).Proxy)
}

func TestMirrorErrorSentinelsAreComparable(t *testing.T) {
	err := &mirrorClientError{kind: ErrMirrorMismatch, detail: "test"}
	assert.True(t, errors.Is(err, ErrMirrorMismatch))
}

func TestMirrorClientRejectsRedirectAndCoolsDownAfterRateLimit(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		w.Header().Set("Retry-After", "30")
		http.Error(w, "limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)
	var redirectResponse mirrorDataEnvelope
	err = client.requestJSON("/redirect", nil, &redirectResponse)
	assert.ErrorIs(t, err, ErrMirrorRedirect)

	_, err = client.GetMovieInfoByID("MovieID")
	assert.ErrorIs(t, err, ErrMirrorRateLimited)
	_, err = client.GetMovieInfoByID("AnotherID")
	assert.ErrorIs(t, err, ErrMirrorRateLimited)
	assert.Equal(t, int32(2), requests.Load(), "cooldown should prevent a second rate-limited request")
}

func TestMirrorClientDoesNotCacheErrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err = client.GetMovieInfoByID("MovieID")
		assert.Error(t, err)
	}
	assert.Equal(t, int32(2), requests.Load())
}

func TestNormalizeMirrorNumberAndFamily(t *testing.T) {
	assert.Equal(t, "FC2-3972042", normalizeMirrorNumber("FC2PPV-3972042-UC"))
	assert.Equal(t, "T38-047", normalizeMirrorNumber("t38_047"))
	assert.NotEqual(t, normalizeMirrorNumber("T38-047"), normalizeMirrorNumber("T38-072"))
	assert.Equal(t, "080114_01", normalizeMirrorNumber("080114_01"))
	assert.NotEqual(t, normalizeMirrorNumber("080114_01"), normalizeMirrorNumber("080114-01"))
	assert.NotEqual(t, normalizeMirrorNumber("080114_01"), normalizeMirrorNumber("080114_001"))
	assert.Equal(t, "050815-076", normalizeMirrorNumber("050815_076"))
	assert.Equal(t, "ABP-123", normalizeMirrorNumber("ABP-123-U"))
	assert.Equal(t, 3, mirrorNumberFamily("FC2-3972042"))
	assert.Equal(t, 2, mirrorNumberFamily("STUDIO.24.01.02"))
	assert.Equal(t, 1, mirrorNumberFamily("123456"))
	assert.Equal(t, 1, mirrorNumberFamily("HEYZO-1234"))
	assert.Equal(t, 1, mirrorNumberFamily("N123456"))
	assert.Equal(t, 1, mirrorNumberFamily("1PONDO-123456"))
	assert.Equal(t, 1, mirrorNumberFamily("CARIBBEANCOM-123456"))
	assert.Equal(t, 1, mirrorNumberFamily("10MUSUME-123456"))
	assert.Equal(t, -1, mirrorNumberFamily("CWP-107"))
	for _, code := range []string{"S2MBD-104", "CWPBD-104", "LAFBD-104", "MKBD-104", "SMBD-104", "SKYHD-104", "MKD-104", "CWP-104", "LAF-104", "SMD-104", "SKY-104", "S2M-104", "EMP-104"} {
		assert.Equal(t, -1, mirrorNumberFamily(code), code)
	}
	assert.True(t, mirrorTypeAllowed(-1, 0))
	assert.True(t, mirrorTypeAllowed(-1, 1))
	assert.False(t, mirrorTypeAllowed(-1, 2))
	assert.False(t, mirrorTypeAllowed(-1, 3))
}

func TestMirrorSearchQueryPreservesUnderscoreAndDropsSuffix(t *testing.T) {
	assert.Equal(t, "080114_01", mirrorSearchQuery("080114_01-U", 1))
	assert.Equal(t, "050815-076", mirrorSearchQuery("050815_076", 1))
	assert.Equal(t, "FC2-3972042", mirrorSearchQuery("FC2PPV_3972042-UC", 3))
}

func TestMirrorClientRejectsDateCodeSeparatorMismatch(t *testing.T) {
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data": map[string]any{"movies": []map[string]any{
					mirrorTestMovie("DifferentDateCode", "080114-01", 1),
				}},
			})
			return
		}
		detailRequests.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("080114_01")
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Zero(t, detailRequests.Load())
}

func TestMirrorParsersRejectNullTypeAndNonFiniteScore(t *testing.T) {
	typeID, ok := parseMirrorType(json.RawMessage("null"))
	assert.False(t, ok)
	assert.Zero(t, typeID)
	assert.Zero(t, parseMirrorFloat(json.RawMessage(`"NaN"`)))
	assert.Zero(t, parseMirrorFloat(json.RawMessage(`"+Inf"`)))
}

func TestMirrorClientSearchPreservesUncensoredSeparator(t *testing.T) {
	const id = "NumericMovie"
	query := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			query <- r.URL.Query().Get("q")
			mirrorTestWriteJSON(w, map[string]any{
				"success": 1,
				"data":    map[string]any{"movies": []map[string]any{mirrorTestMovie(id, "080114_01", 1)}},
			})
			return
		}
		mirrorTestWriteJSON(w, map[string]any{
			"success": 1,
			"data":    map[string]any{"movie": mirrorTestMovie(id, "080114_01", 1)},
		})
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("080114_01-U")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "080114_01", <-query)
}

func TestMirrorClientUsesEncodedSearchParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/search", r.URL.Path)
		assert.Equal(t, "ABP-123", r.URL.Query().Get("q"))
		assert.Equal(t, url.Values{"q": {"ABP-123"}, "page": {"1"}, "type": {"movie"}, "limit": {"24"}}.Encode(), r.URL.RawQuery)
		mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"movies": []any{}}})
	}))
	t.Cleanup(server.Close)
	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("ABP-123")
	require.NoError(t, err)
	assert.Empty(t, results)
}
