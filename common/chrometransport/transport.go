// Package chrometransport provides an http.RoundTripper that impersonates
// Chrome's TLS and HTTP/2 fingerprints via bogdanfinn/tls-client.
//
// Plain HTTP (including httptest servers) uses the standard library so unit
// tests keep working. HTTPS requests use a Chrome 150 ClientHello, HTTP/2
// SETTINGS, and Chrome-like header order. HTTP/3 is disabled because HTTP
// proxies cannot tunnel QUIC and would leak the real egress IP.
package chrometransport

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	// ChromeUserAgent matches profiles.Chrome_150. Keep JavDB request headers
	// on the same major version so the UA and ClientHello do not disagree.
	ChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
	ChromeSecCHUA   = `"Not;A=Brand";v="8", "Chromium";v="150", "Google Chrome";v="150"`

	defaultTimeout = 60 * time.Second
)

// Transport impersonates Chrome for HTTPS and falls back to net/http for HTTP.
type Transport struct {
	mu      sync.Mutex
	client  tls_client.HttpClient
	plain   *http.Transport
	proxy   string
	timeout time.Duration
}

var (
	_ http.RoundTripper = (*Transport)(nil)
)

// New returns a Chrome-150 impersonating RoundTripper. The initial proxy is
// taken from HTTPS_PROXY/HTTP_PROXY so a container-wide proxy is honored
// without a provider-specific override.
func New() (*Transport, error) {
	t := &Transport{
		timeout: defaultTimeout,
		proxy:   envProxyURL(),
		plain: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	if err := t.rebuildLocked(); err != nil {
		return nil, err
	}
	return t, nil
}

// SetProxy updates the outbound proxy for both HTTPS impersonation and the
// HTTP fallback. An empty value restores environment-based proxy selection.
func (t *Transport) SetProxy(proxyURL string) error {
	if t == nil {
		return fmt.Errorf("chrome transport is not initialized")
	}
	proxyURL = strings.TrimSpace(proxyURL)
	var parsed *url.URL
	if proxyURL != "" {
		var err error
		parsed, err = parseProxyURL(proxyURL)
		if err != nil {
			return err
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.proxy = proxyURL
	if parsed == nil {
		t.plain.Proxy = http.ProxyFromEnvironment
	} else {
		t.plain.Proxy = http.ProxyURL(parsed)
	}
	return t.rebuildLocked()
}

// SetTimeout rebuilds the impersonating client with a new request deadline.
func (t *Transport) SetTimeout(timeout time.Duration) error {
	if t == nil {
		return fmt.Errorf("chrome transport is not initialized")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if timeout > 0 {
		t.timeout = timeout
	} else {
		t.timeout = defaultTimeout
	}
	return t.rebuildLocked()
}

// RoundTrip implements http.RoundTripper. Non-HTTPS URLs skip impersonation
// so httptest servers and local HTTP endpoints keep using net/http.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil {
		return nil, fmt.Errorf("chrome transport is not initialized")
	}
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid HTTP request")
	}
	if req.URL.Scheme != "https" {
		return t.plain.RoundTrip(req)
	}

	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("chrome TLS client is not initialized")
	}

	fReq, err := toFHTTPRequest(req)
	if err != nil {
		return nil, err
	}
	fResp, err := client.Do(fReq)
	if err != nil {
		return nil, err
	}
	return fromFHTTPResponse(fResp, req), nil
}

func (t *Transport) rebuildLocked() error {
	timeout := t.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutMilliseconds(int(timeout / time.Millisecond)),
		tls_client.WithClientProfile(profiles.Chrome_150),
		tls_client.WithRandomTLSExtensionOrder(),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithDisableHttp3(),
	}
	if t.proxy != "" {
		options = append(options, tls_client.WithProxyUrl(t.proxy))
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return fmt.Errorf("create chrome TLS client: %w", err)
	}
	if t.client != nil {
		t.client.CloseIdleConnections()
	}
	t.client = client
	return nil
}

func parseProxyURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		scheme = "http"
		parsed.Scheme = scheme
	}
	switch scheme {
	case "http", "https", "socks4", "socks5":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid proxy URL")
	}
	return parsed, nil
}

func envProxyURL() string {
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
