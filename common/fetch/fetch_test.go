package fetch

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetcherSetCookiesCreatesJar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value, err := r.Cookie("session"); err != nil || value.Value != "ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fetcher := Default(&Config{})
	if err := fetcher.SetCookies(server.URL, []*http.Cookie{{Name: "session", Value: "ok", Path: "/"}}); err != nil {
		t.Fatalf("set cookies: %v", err)
	}
	response, err := fetcher.Fetch(server.URL)
	if err != nil {
		t.Fatalf("fetch with cookies: %v", err)
	}
	response.Body.Close()
}
