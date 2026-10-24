package javdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mirrorExternalAcceptanceSearchMovie struct {
	ID       string `json:"id"`
	Number   string `json:"number"`
	Title    string `json:"title"`
	CoverURL string `json:"cover_url"`
}

type mirrorExternalAcceptanceDetailMovie struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	Number   string `json:"number"`
	Title    string `json:"title"`
	CoverURL string `json:"cover_url"`
}

type mirrorExternalAcceptanceRequest struct {
	Method        string
	Path          string
	RawQuery      string
	Cookie        string
	Authorization string
}

type mirrorExternalAcceptanceRequests struct {
	mu       sync.Mutex
	requests []mirrorExternalAcceptanceRequest
}

func (r *mirrorExternalAcceptanceRequests) add(req mirrorExternalAcceptanceRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

func (r *mirrorExternalAcceptanceRequests) snapshot() []mirrorExternalAcceptanceRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mirrorExternalAcceptanceRequest(nil), r.requests...)
}

func newMirrorExternalAcceptanceServer(
	t *testing.T,
	searchMovies []mirrorExternalAcceptanceSearchMovie,
	details map[string]mirrorExternalAcceptanceDetailMovie,
) (*httptest.Server, *mirrorExternalAcceptanceRequests) {
	t.Helper()

	requests := &mirrorExternalAcceptanceRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.add(mirrorExternalAcceptanceRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			RawQuery:      r.URL.RawQuery,
			Cookie:        r.Header.Get("Cookie"),
			Authorization: r.Header.Get("Authorization"),
		})
		if r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected Cookie header on %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Authorization header on %s %s", r.Method, r.URL.Path)
		}
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/search":
			query := r.URL.Query()
			if _, ok := query["q"]; !ok || query.Get("q") == "" {
				t.Errorf("search request is missing q: %q", r.URL.RawQuery)
			}
			if query.Get("page") != "1" || query.Get("type") != "movie" || query.Get("limit") != "24" {
				t.Errorf("unexpected search parameters: %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(struct {
				Success int `json:"success"`
				Data    struct {
					Movies []mirrorExternalAcceptanceSearchMovie `json:"movies"`
				} `json:"data"`
			}{
				Success: 1,
				Data: struct {
					Movies []mirrorExternalAcceptanceSearchMovie `json:"movies"`
				}{Movies: searchMovies},
			})
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v4/movies/") {
			id := strings.TrimPrefix(r.URL.Path, "/v4/movies/")
			movie, ok := details[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(struct {
				Success int `json:"success"`
				Data    struct {
					Movie mirrorExternalAcceptanceDetailMovie `json:"movie"`
				} `json:"data"`
			}{
				Success: 1,
				Data: struct {
					Movie mirrorExternalAcceptanceDetailMovie `json:"movie"`
				}{Movie: movie},
			})
			return
		}

		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestMirrorClientExternalSearchNormalizesFC2Number(t *testing.T) {
	const id = "Fc2OpaqueID"
	server, requests := newMirrorExternalAcceptanceServer(t,
		[]mirrorExternalAcceptanceSearchMovie{{
			ID:       id,
			Number:   "FC2-3972042",
			Title:    "FC2 acceptance fixture",
			CoverURL: "https://images.example/fc2.jpg",
		}},
		map[string]mirrorExternalAcceptanceDetailMovie{
			id: {
				ID:       id,
				Type:     3,
				Number:   "FC2-3972042",
				Title:    "FC2 acceptance fixture",
				CoverURL: "https://images.example/fc2.jpg",
			},
		},
	)

	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("FC2-PPV-3972042")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "FC2-3972042", results[0].Number)
	assert.Equal(t, id, results[0].ID)
	assert.Equal(t, "JavDB", results[0].Provider)

	gotRequests := requests.snapshot()
	require.GreaterOrEqual(t, len(gotRequests), 2, "search should fetch the movie detail")
	assert.Equal(t, "/v2/search", gotRequests[0].Path)
	assert.Equal(t, "/v4/movies/"+id, gotRequests[1].Path)
}

func TestMirrorClientExternalSearchRejectsNearNumber(t *testing.T) {
	const id = "T38MovieID"
	server, _ := newMirrorExternalAcceptanceServer(t,
		[]mirrorExternalAcceptanceSearchMovie{{
			ID:       id,
			Number:   "T38-072",
			Title:    "Different T38 scene",
			CoverURL: "https://images.example/t38.jpg",
		}},
		map[string]mirrorExternalAcceptanceDetailMovie{
			id: {
				ID:       id,
				Type:     0,
				Number:   "T38-072",
				Title:    "Different T38 scene",
				CoverURL: "https://images.example/t38.jpg",
			},
		},
	)

	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	results, err := client.SearchMovie("T38-047")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestMirrorClientExternalDetailRejectsMismatchedID(t *testing.T) {
	server, _ := newMirrorExternalAcceptanceServer(t, nil,
		map[string]mirrorExternalAcceptanceDetailMovie{
			"RequestedOpaqueID": {
				ID:       "DifferentOpaqueID",
				Type:     0,
				Number:   "ABP-123",
				Title:    "Mismatched detail fixture",
				CoverURL: "https://images.example/mismatch.jpg",
			},
		},
	)

	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	info, err := client.GetMovieInfoByID("RequestedOpaqueID")
	require.Error(t, err)
	assert.Nil(t, info)
}

func TestMirrorClientExternalDetailPreservesOpaqueIDAndProvider(t *testing.T) {
	const id = "MiXeDopaqueID"
	server, requests := newMirrorExternalAcceptanceServer(t, nil,
		map[string]mirrorExternalAcceptanceDetailMovie{
			id: {
				ID:       id,
				Type:     1,
				Number:   "ABP-123",
				Title:    "Case preserving detail fixture",
				CoverURL: "https://images.example/case.jpg",
			},
		},
	)

	client, err := newMirrorClient(server.URL, "", time.Second)
	require.NoError(t, err)

	info, err := client.GetMovieInfoByID(id)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, id, info.ID)
	assert.Equal(t, "JavDB", info.Provider)
	assert.Equal(t, "ABP-123", info.Number)
	assert.Equal(t, "https://images.example/case.jpg", info.CoverURL)

	gotRequests := requests.snapshot()
	require.Len(t, gotRequests, 1)
	assert.Equal(t, "/v4/movies/"+id, gotRequests[0].Path)
	assert.Empty(t, gotRequests[0].Cookie)
	assert.Empty(t, gotRequests[0].Authorization)
}
