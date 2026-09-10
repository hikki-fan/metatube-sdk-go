package chrometransport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransportHTTPFallbackPreservesHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	transport, err := New()
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, server.URL+"/probe", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", ChromeUserAgent)
	req.Header.Set("Sec-CH-UA", ChromeSecCHUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "ok", string(body))
	require.Equal(t, ChromeUserAgent, got.Get("User-Agent"))
	require.Equal(t, ChromeSecCHUA, got.Get("Sec-CH-UA"))
	require.Equal(t, "zh-CN,zh;q=0.9", got.Get("Accept-Language"))
}

func TestTransportSetProxyRejectsInvalidURL(t *testing.T) {
	transport, err := New()
	require.NoError(t, err)
	require.Error(t, transport.SetProxy("://bad"))
}

func TestTransportSetProxyEmptyRestoresEnvironmentProxy(t *testing.T) {
	transport, err := New()
	require.NoError(t, err)
	require.NoError(t, transport.SetProxy("http://127.0.0.1:7890"))
	require.NoError(t, transport.SetProxy(""))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
