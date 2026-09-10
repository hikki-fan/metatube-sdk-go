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

func TestFetcherSetTransportIsUsed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transport") != "ok" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fetcher := Default(&Config{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("X-Transport", "ok")
			return http.DefaultTransport.RoundTrip(req)
		}),
	})
	response, err := fetcher.Fetch(server.URL)
	if err != nil {
		t.Fatalf("fetch with custom transport: %v", err)
	}
	response.Body.Close()
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
