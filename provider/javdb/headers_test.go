package javdb

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJavDBBrowserHeadersForPageAndImageRequests(t *testing.T) {
	var pageHeaders, imageHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			pageHeaders = r.Header.Clone()
		case "/cover.jpg":
			imageHeaders = r.Header.Clone()
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer server.Close()

	jdb := New()
	jdb.searchURL = server.URL + "/search?q=%s"
	_, _, err := jdb.searchMovie("probe")
	require.NoError(t, err)

	resp, err := jdb.Fetch(server.URL + "/cover.jpg")
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t, defaultJavDBUserAgent, pageHeaders.Get("User-Agent"))
	require.Equal(t, javDBDocumentAccept, pageHeaders.Get("Accept"))
	require.Equal(t, javDBAcceptLanguage, pageHeaders.Get("Accept-Language"))
	require.Equal(t, "document", pageHeaders.Get("Sec-Fetch-Dest"))
	require.Equal(t, "navigate", pageHeaders.Get("Sec-Fetch-Mode"))
	require.Equal(t, "same-origin", pageHeaders.Get("Sec-Fetch-Site"))
	require.Equal(t, "?1", pageHeaders.Get("Sec-Fetch-User"))
	require.Equal(t, javDBSecCHUA, pageHeaders.Get("Sec-CH-UA"))

	require.Equal(t, defaultJavDBUserAgent, imageHeaders.Get("User-Agent"))
	require.Equal(t, javDBImageAccept, imageHeaders.Get("Accept"))
	require.Equal(t, "image", imageHeaders.Get("Sec-Fetch-Dest"))
	require.Equal(t, "no-cors", imageHeaders.Get("Sec-Fetch-Mode"))
	require.Equal(t, "same-site", imageHeaders.Get("Sec-Fetch-Site"))
}

func TestJavDB_SetProxyRejectsInvalidURL(t *testing.T) {
	require.Error(t, New().SetProxy("://bad"))
}

func TestJavDB_SetProxyClearsBackToDirectHTTPFixtures(t *testing.T) {
	var pageHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer server.Close()

	jdb := New()
	require.NoError(t, jdb.SetProxy("http://127.0.0.1:7890"))
	require.NoError(t, jdb.SetProxy(""))
	jdb.searchURL = server.URL + "/search?q=%s"
	_, _, err := jdb.searchMovie("probe")
	require.NoError(t, err)
	require.Equal(t, defaultJavDBUserAgent, pageHeaders.Get("User-Agent"))
	require.Equal(t, javDBSecCHUA, pageHeaders.Get("Sec-CH-UA"))
}
