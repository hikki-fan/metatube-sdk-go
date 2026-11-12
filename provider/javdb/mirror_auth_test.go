package javdb

import (
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

func newMirrorAuthTestClient(t *testing.T, handler http.HandlerFunc) *mirrorClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := newMirrorClientWithCredentials(server.URL+"/api", "", 5*time.Second, "fixture-user", "fixture-password")
	require.NoError(t, err)
	return client
}

func mirrorAuthTestLogin(w http.ResponseWriter) {
	mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"token": "fixture-session-token"}})
}

func mirrorAuthTestJWTExpired(w http.ResponseWriter) {
	mirrorTestWriteJSON(w, map[string]any{
		"success": 0, "action": "JWTVerificationError", "message": "login required", "data": map[string]any{},
	})
}

func TestMirrorAuthLoginMultipartAndIsolatedToken(t *testing.T) {
	var logins, lookups atomic.Int32
	client := newMirrorAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.NotEmpty(t, r.Header.Get("jdsignature"))
		if r.URL.Path == "/api/v1/sessions" {
			logins.Add(1)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Empty(t, r.Header.Get("Authorization"))
			if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
				return
			}
			expected := map[string][]string{
				"username": {"fixture-user"}, "password": {"fixture-password"},
				"device_uuid": {"web"}, "device_name": {"Web"}, "device_model": {"Browser"},
				"platform": {"android"}, "system_version": {"Mozilla/5.0"},
				"app_channel": {"official"}, "app_version": {"web"}, "app_version_number": {"1.0.0"},
			}
			assert.Equal(t, expected, r.MultipartForm.Value)
			assert.Empty(t, r.MultipartForm.File)
			w.Header().Set("Set-Cookie", "foreign=must-not-return")
			mirrorAuthTestLogin(w)
			return
		}
		lookups.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "fixture-session-token", r.Header.Get("Authorization"))
		mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"movie": mirrorTestMovie("yzx6Z", "HEYDOUGA-4030-1938", 1)}})
	})
	for i := 0; i < 2; i++ {
		info, err := client.GetMovieInfoByID("yzx6Z")
		require.NoError(t, err)
		assert.Equal(t, "HEYDOUGA-4030-1938", info.Number)
	}
	assert.EqualValues(t, 1, logins.Load())
	assert.EqualValues(t, 1, lookups.Load())
	assert.Nil(t, client.httpClient.Jar)
}

func TestMirrorAuthRefreshOnlyOnExplicitSessionFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		payload    any
		wantErr    error
		wantLogins int32
	}{
		{"unauthorized", 401, nil, nil, 2},
		{"session-expired", 200, map[string]any{"success": 0, "action": "session_expired"}, nil, 2},
		{"login-required", 200, map[string]any{"success": 0, "action": "login"}, nil, 2},
		{"jwt-verification-error-case-insensitive", 200, map[string]any{"success": 0, "action": "jwtverificationerror"}, nil, 2},
		{"jwt-verification-error-substring-is-business", 200, map[string]any{"success": 0, "action": "JWTVerificationErrorExpired"}, ErrMirrorBusiness, 1},
		{"generic-business-error", 200, map[string]any{"success": 0, "message": "fixture-password"}, ErrMirrorBusiness, 1},
		{"boolean-success", 200, map[string]any{"success": false, "action": "login"}, ErrMirrorBusiness, 1},
		{"forbidden", 403, nil, ErrMirrorRateLimited, 1},
		{"rate-limited", 429, nil, ErrMirrorRateLimited, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logins, lookups atomic.Int32
			client := newMirrorAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/sessions" {
					logins.Add(1)
					mirrorAuthTestLogin(w)
					return
				}
				if lookups.Add(1) == 1 {
					w.Header().Set("Retry-After", "45")
					w.WriteHeader(tc.status)
					if tc.payload != nil {
						mirrorTestWriteJSON(w, tc.payload)
					}
					return
				}
				mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"ok": true}})
			})
			var data map[string]any
			err := client.requestJSON("/v2/search", nil, &data)
			if tc.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, true, data["ok"])
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				assert.NotContains(t, err.Error(), "fixture-password")
			}
			assert.Equal(t, tc.wantLogins, logins.Load())
			if tc.wantErr == ErrMirrorRateLimited {
				require.ErrorIs(t, client.requestJSON("/v2/search", nil, &data), ErrMirrorRateLimited)
				assert.EqualValues(t, 1, lookups.Load())
				assert.EqualValues(t, 1, logins.Load())
				assert.Greater(t, time.Until(client.cooldownUntil), 40*time.Second)
			}
		})
	}
}

func TestMirrorAuthPersistentSessionFailureRefreshesAtMostOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		jwtFail bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "jwt-verification-error", status: http.StatusOK, jwtFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logins, lookups atomic.Int32
			client := newMirrorAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/sessions" {
					logins.Add(1)
					mirrorAuthTestLogin(w)
					return
				}
				lookups.Add(1)
				if tc.jwtFail {
					mirrorAuthTestJWTExpired(w)
					return
				}
				w.WriteHeader(tc.status)
			})
			var data any
			require.ErrorIs(t, client.requestJSON("/v2/search", nil, &data), ErrMirrorAuth)
			assert.EqualValues(t, 2, logins.Load())
			assert.EqualValues(t, 2, lookups.Load())
			assert.Empty(t, client.session.token)
		})
	}
}

func TestMirrorAuthJWTVerificationErrorRefreshesSearchAndDetail(t *testing.T) {
	const movieID = "2mO9qq"
	for _, tc := range []struct {
		name       string
		expiredURL string
		search     bool
	}{
		{name: "search", expiredURL: "/api/v2/search", search: true},
		{name: "detail", expiredURL: "/api/v4/movies/" + movieID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logins, searchRequests, detailRequests, expiredResponses atomic.Int32
			client := newMirrorAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/sessions":
					loginNumber := logins.Add(1)
					mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{
						"token": fmt.Sprintf("fixture-session-token-%d", loginNumber),
					}})
				case "/api/v2/search":
					searchRequests.Add(1)
					assert.Equal(t, "MIUM-1434", r.URL.Query().Get("q"))
					if r.URL.Path == tc.expiredURL && expiredResponses.CompareAndSwap(0, 1) {
						assert.Equal(t, "fixture-session-token-1", r.Header.Get("Authorization"))
						mirrorAuthTestJWTExpired(w)
						return
					}
					assert.Equal(t, "fixture-session-token-2", r.Header.Get("Authorization"))
					mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{
						"movies": []map[string]any{mirrorTestMovie(movieID, "MIUM-1434", 0)},
					}})
				case "/api/v4/movies/" + movieID:
					detailRequests.Add(1)
					if r.URL.Path == tc.expiredURL && expiredResponses.CompareAndSwap(0, 1) {
						assert.Equal(t, "fixture-session-token-1", r.Header.Get("Authorization"))
						mirrorAuthTestJWTExpired(w)
						return
					}
					assert.Equal(t, "fixture-session-token-2", r.Header.Get("Authorization"))
					mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{
						"movie": mirrorTestMovie(movieID, "MIUM-1434", 0),
					}})
				default:
					http.NotFound(w, r)
				}
			})

			if tc.search {
				results, err := client.SearchMovie("MIUM-1434")
				require.NoError(t, err)
				require.Len(t, results, 1)
				assert.Equal(t, movieID, results[0].ID)
				assert.Equal(t, "MIUM-1434", results[0].Number)
				assert.EqualValues(t, 2, searchRequests.Load())
				assert.EqualValues(t, 1, detailRequests.Load())
			} else {
				info, err := client.GetMovieInfoByID(movieID)
				require.NoError(t, err)
				assert.Equal(t, movieID, info.ID)
				assert.Equal(t, "MIUM-1434", info.Number)
				assert.EqualValues(t, 0, searchRequests.Load())
				assert.EqualValues(t, 2, detailRequests.Load())
			}
			assert.EqualValues(t, 2, logins.Load())
			assert.EqualValues(t, 1, expiredResponses.Load())
		})
	}
}

func TestMirrorAuthConcurrentLoginAndRecoveryAreMerged(t *testing.T) {
	const workers = 12
	var logins, oldRequests atomic.Int32
	var seen sync.Map
	allOld := make(chan struct{})
	client := newMirrorAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			logins.Add(1)
			// Deliberately reuse a token: session generation must separate logins.
			mirrorAuthTestLogin(w)
			return
		}
		if _, loaded := seen.LoadOrStore(r.URL.Path, true); !loaded {
			if oldRequests.Add(1) == workers {
				close(allOld)
			}
			<-allOld
			mirrorAuthTestJWTExpired(w)
			return
		}
		assert.Equal(t, "fixture-session-token", r.Header.Get("Authorization"))
		mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"ok": true}})
	})
	errors := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			var data any
			errors <- client.requestJSON(fmt.Sprintf("/v2/fixture/%d", i), nil, &data)
		}(i)
	}
	for i := 0; i < workers; i++ {
		require.NoError(t, <-errors)
	}
	assert.EqualValues(t, 2, logins.Load())
}

func TestMirrorAuthLoginFailureDoesNotLeakOrRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		payload any
		wantErr error
	}{
		{"bad-password", 401, nil, ErrMirrorAuth},
		{"business-error", 200, map[string]any{"success": 0, "message": "fixture-user fixture-password"}, ErrMirrorBusiness},
		{"missing-token", 200, map[string]any{"success": 1, "data": map[string]any{}}, ErrMirrorAuth},
		{"invalid-token", 200, map[string]any{"success": 1, "data": map[string]any{"token": "fixture-password\n"}}, ErrMirrorAuth},
		{"rate-limited", 429, nil, ErrMirrorRateLimited},
		{"forbidden", 403, nil, ErrMirrorRateLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			client := newMirrorAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, "/api/v1/sessions", r.URL.Path)
				w.WriteHeader(tc.status)
				if tc.payload != nil {
					mirrorTestWriteJSON(w, tc.payload)
				}
			})
			var data any
			err := client.requestJSON("/v2/search", nil, &data)
			require.ErrorIs(t, err, tc.wantErr)
			for _, secret := range []string{"fixture-user", "fixture-password"} {
				assert.NotContains(t, err.Error(), secret)
			}
			assert.EqualValues(t, 1, requests.Load())
			assert.Empty(t, client.session.token)
		})
	}
}

func TestMirrorAuthRejectsLoginAndAuthenticatedRedirects(t *testing.T) {
	for _, loginRedirect := range []bool{true, false} {
		t.Run(fmt.Sprintf("login=%t", loginRedirect), func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected.Add(1)
			}))
			t.Cleanup(target.Close)
			client := newMirrorAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/sessions" && !loginRedirect {
					mirrorAuthTestLogin(w)
					return
				}
				http.Redirect(w, r, target.URL+"/credentials", http.StatusTemporaryRedirect)
			})
			var data any
			require.ErrorIs(t, client.requestJSON("/v2/search", nil, &data), ErrMirrorRedirect)
			assert.Zero(t, redirected.Load())
		})
	}
}

func TestMirrorAuthConfigurationAndCredentialChanges(t *testing.T) {
	for _, tc := range []struct{ username, password string }{
		{"fixture-user", ""}, {"", "fixture-password"}, {" ", "fixture-password"},
	} {
		_, err := newMirrorClientWithCredentials("http://127.0.0.1", "", time.Second, tc.username, tc.password)
		require.ErrorIs(t, err, ErrMirrorAuth)
	}
	jdb := New()
	for _, key := range []string{"mirror_username", "mirror_password"} {
		config := envconfig.NewConfig()
		config.Set(key, "fixture-secret")
		err := jdb.SetConfig(config)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "fixture-secret")
	}
	var logins, lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			loginNumber := logins.Add(1)
			_ = r.ParseMultipartForm(1 << 20)
			assert.True(t, strings.HasPrefix(r.FormValue("username"), "fixture-user"))
			if loginNumber == 1 {
				assert.Equal(t, " fixture-password-one ", r.FormValue("password"))
			} else {
				assert.Equal(t, "fixture-password-two", r.FormValue("password"))
			}
			mirrorAuthTestLogin(w)
			return
		}
		lookups.Add(1)
		mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"movie": mirrorTestMovie("yzx6Z", "HEYDOUGA-4030-1938", 1)}})
	}))
	t.Cleanup(server.Close)
	config := envconfig.NewConfig()
	config.Set("mirror_base_url", server.URL+"/api")
	config.Set("mirror_username", "fixture-user-one")
	config.Set("mirror_password", " fixture-password-one ")
	require.NoError(t, jdb.SetConfig(config))
	first, err := jdb.getMirrorClient()
	require.NoError(t, err)
	assert.Equal(t, " fixture-password-one ", first.(*mirrorClient).password)
	_, err = first.GetMovieInfoByID("yzx6Z")
	require.NoError(t, err)
	config.Set("mirror_username", "fixture-user-two")
	config.Set("mirror_password", "fixture-password-two")
	require.NoError(t, jdb.SetConfig(config))
	second, err := jdb.getMirrorClient()
	require.NoError(t, err)
	assert.NotSame(t, first, second)
	_, err = second.GetMovieInfoByID("yzx6Z")
	require.NoError(t, err)
	assert.EqualValues(t, 2, logins.Load())
	assert.EqualValues(t, 2, lookups.Load())

	config.Set("mirror_username", "")
	config.Set("mirror_password", "")
	require.NoError(t, jdb.SetConfig(config))
	anonymous, err := jdb.getMirrorClient()
	require.NoError(t, err)
	assert.Empty(t, anonymous.(*mirrorClient).username)
	assert.Empty(t, anonymous.(*mirrorClient).session.token)
}

func TestMirrorAuthActorProviderConfiguration(t *testing.T) {
	actor := NewActor()
	for _, key := range []string{"mirror_username", "mirror_password"} {
		config := envconfig.NewConfig()
		config.Set(key, "fixture-secret")
		require.ErrorIs(t, actor.SetConfig(config), ErrMirrorAuth)
	}
	var logins, lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Cookie"))
		if r.URL.Path == "/api/v1/sessions" {
			logins.Add(1)
			assert.Empty(t, r.Header.Get("Authorization"))
			if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
				return
			}
			assert.Equal(t, "fixture-user", r.FormValue("username"))
			assert.Equal(t, " fixture-password ", r.FormValue("password"))
			mirrorAuthTestLogin(w)
			return
		}
		lookups.Add(1)
		assert.Equal(t, "/api/v1/actors/ActorFixture", r.URL.Path)
		assert.Equal(t, "fixture-session-token", r.Header.Get("Authorization"))
		mirrorTestWriteJSON(w, map[string]any{"success": 1, "data": map[string]any{"actor": map[string]any{
			"id": "ActorFixture", "name": "Fixture Actor", "avatar_url": "https://images.example/actor.jpg",
		}}})
	}))
	t.Cleanup(server.Close)
	config := envconfig.NewConfig()
	config.Set("mirror_base_url", server.URL+"/api")
	config.Set("mirror_username", " fixture-user ")
	config.Set("mirror_password", " fixture-password ")
	require.NoError(t, actor.SetConfig(config))
	info, err := actor.GetActorInfoByID("ActorFixture")
	require.NoError(t, err)
	assert.Equal(t, ActorProviderName, info.Provider)
	assert.Equal(t, "Fixture Actor", info.Name)
	assert.EqualValues(t, 1, logins.Load())
	assert.EqualValues(t, 1, lookups.Load())
}
