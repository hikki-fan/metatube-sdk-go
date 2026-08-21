package javdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

const cookieCloudLegacyFixture = "U2FsdGVkX18BAgMEBQYHCP3X5ie1vx0F9EQM483GQnqRXnh7ACds9NbVTbYYcPfzNCeA0sv5BBWo8zMHEl3ZQ/Fe59TXk3KBkM+wzQMn41DaZX0NUUnA4SgjHjKyHg2ICgKW9qRObje+nOCwPomcyj9lZ3tfE76w+YAr0UqxBeXglb+YPnSIqd0LpYiDqcHVwQwQ+jvkYWzp9hsi58Dd0e1fJepgj3WjJtXkcFrZWxA3nzOWc7upXIOHJ4/HHtxIFnbGdjwMqSOjOVtlf1OY1oDTzsabb70QEZnSvFCw+ByX6IjHAlvc+hdjkWDbHA3/PSVELE6cqNgCcWI6JD5S/0azUdnU6gqzCL/5fnTM+5MP2og0deHEUVWG7yyLPloh"

const cookieCloudFixedFixture = "mMC74DKRghUinB7WUZ3LNQ8r9pC/Siaji+QUt+E3o5OUD7a5sxOF7XUyrJ4lsd/c4hPlZQmi85gRrAXeRVdKctPCKTG5tqOTjHMzZivyASiJmeU4Qx9oclIeQXPNWq0yz0EFwJTmH+l/S6JzbjWvxQ=="

func TestDecryptCookieCloudLegacy(t *testing.T) {
	plain, err := decryptCookieCloud(cookieCloudLegacyFixture, "test-uuid", "test-password", "")
	if err != nil {
		t.Fatalf("decrypt CookieCloud fixture: %v", err)
	}
	var payload cookieCloudPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		t.Fatalf("decode decrypted payload: %v", err)
	}
	if got := payload.CookieData[".javdb.com"][0].Value; got != "abc123" {
		t.Fatalf("unexpected cookie value: %q", got)
	}
}

func TestDecryptCookieCloudFixedAndUntypedFallback(t *testing.T) {
	for _, cryptoType := range []string{"aes-128-cbc-fixed", ""} {
		plain, err := decryptCookieCloud(cookieCloudFixedFixture, "fixed-uuid", "fixed-password", cryptoType)
		if err != nil {
			t.Fatalf("decrypt fixed CookieCloud fixture (%q): %v", cryptoType, err)
		}
		var payload cookieCloudPayload
		if err := json.Unmarshal(plain, &payload); err != nil {
			t.Fatalf("decode fixed payload (%q): %v", cryptoType, err)
		}
		if got := payload.CookieData[".javdb.com"][0].Value; got != "fixed123" {
			t.Fatalf("unexpected fixed cookie value (%q): %q", cryptoType, got)
		}
	}
}

func TestCookieCloudClientFetchesAndFiltersCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get/test-uuid" {
			t.Fatalf("unexpected CookieCloud path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer server.Close()

	client := &cookieCloudClient{
		host:       server.URL,
		uuid:       "test-uuid",
		password:   "test-password",
		httpClient: server.Client(),
	}
	cookies, err := client.fetchCookies()
	if err != nil {
		t.Fatalf("fetch CookieCloud cookies: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected one JavDB cookie, got %d", len(cookies))
	}
	if cookies[0].Name != "cf_clearance" || cookies[0].Domain != ".javdb.com" {
		t.Fatalf("unexpected cookie: %#v", cookies[0])
	}
	if !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags were not preserved: %#v", cookies[0])
	}
	if !cookies[0].Expires.Equal(time.Unix(1893456000, 0)) {
		t.Fatalf("cookie expiration was not preserved: %v", cookies[0].Expires)
	}
}

func TestCookieCloudDomainAllowed(t *testing.T) {
	for domain, want := range map[string]bool{
		"javdb.com":         true,
		".javdb.com":        true,
		"img.javdb.com":     true,
		"jdbstatic.com":     true,
		"cdn.jdbstatic.com": true,
		"example.com":       false,
		"javdb.com.evil":    false,
	} {
		if got := cookieCloudDomainAllowed(domain); got != want {
			t.Errorf("cookieCloudDomainAllowed(%q) = %v, want %v", domain, got, want)
		}
	}
}

func TestCookieCloudCookiesRejectsForeignAndExpiredEntries(t *testing.T) {
	cookies := cookieCloudCookies(map[string][]cookieCloudCookie{
		"javdb.com": {
			{Name: "active", Value: "ok", Domain: ".javdb.com"},
			{Name: "expired", Value: "old", Domain: ".javdb.com", ExpirationDate: float64(time.Now().Add(-time.Hour).Unix())},
			{Name: "foreign", Value: "nope", Domain: ".example.com"},
		},
	})
	if len(cookies) != 1 || cookies[0].Name != "active" {
		t.Fatalf("unexpected filtered cookies: %#v", cookies)
	}
}

func TestJavDBCookieCloudConfigValidation(t *testing.T) {
	newConfig := func(values map[string]string) *envconfig.Config {
		config := envconfig.NewConfig()
		for key, value := range values {
			config.Set(key, value)
		}
		return config
	}

	if err := New().SetConfig(newConfig(map[string]string{"cookiecloud_host": "https://cookiecloud.example"})); err == nil {
		t.Fatal("expected missing CookieCloud credentials to fail")
	}
	if err := New().SetConfig(newConfig(map[string]string{
		"cookiecloud_host":     "ftp://cookiecloud.example",
		"cookiecloud_uuid":     "uuid",
		"cookiecloud_password": "password",
	})); err == nil {
		t.Fatal("expected non-HTTP CookieCloud URL to fail")
	}
	if err := New().SetConfig(newConfig(map[string]string{
		"cookiecloud_host":             "https://cookiecloud.example",
		"cookiecloud_uuid":             "uuid",
		"cookiecloud_password":         "password",
		"cookiecloud_refresh_interval": "500ms",
	})); err == nil {
		t.Fatal("expected too-short CookieCloud refresh interval to fail")
	}
}

func TestJavDBCookieCloudRefreshIsCached(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer server.Close()

	config := envconfig.NewConfig()
	config.Set("cookiecloud_host", server.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")
	jdb := New()
	if err := jdb.SetConfig(config); err != nil {
		t.Fatalf("configure CookieCloud: %v", err)
	}
	jdb.cookieCloudHTTPClient = server.Client()
	if err := jdb.ensureCookieCloudCookies(); err != nil {
		t.Fatalf("first CookieCloud refresh: %v", err)
	}
	if err := jdb.ensureCookieCloudCookies(); err != nil {
		t.Fatalf("cached CookieCloud refresh: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected one CookieCloud request while cached, got %d", got)
	}
}
