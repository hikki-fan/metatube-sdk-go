package fetch

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/go-retryablehttp"

	"github.com/metatube-community/metatube-sdk-go/common/random"
	"github.com/metatube-community/metatube-sdk-go/errors"
)

var DefaultFetcher = Default(&Config{RandomUserAgent: true})

type Config struct {
	// Set User-Agent Header.
	UserAgent string

	// Set Referer Header.
	Referer string

	// Headers contains default headers applied to every request. Explicit
	// request options still take precedence over these defaults.
	Headers map[string]string

	// Enable cookies.
	EnableCookies bool

	// Use random User-Agent.
	RandomUserAgent bool

	// Return error when status is not OK.
	RaiseForStatus bool

	// HTTP Request timeout.
	Timeout time.Duration

	// Custom HTTP Transport.
	Transport http.RoundTripper

	// Skip TLS verification. Applies only
	// to *http.Transport based transport.
	SkipVerify bool
}

type Fetcher struct {
	client *http.Client
	config *Config
}

func New(c *http.Client, cfg *Config) *Fetcher {
	if cfg.RandomUserAgent {
		// assign a random user-agent.
		cfg.UserAgent = random.UserAgent()
	}
	if cfg.EnableCookies {
		jar, _ := cookiejar.New(nil)
		c.Jar = jar // assign a cookie jar.
	}
	return &Fetcher{
		client: c,
		config: cfg,
	}
}

func Default(cfg *Config) *Fetcher {
	if cfg == nil /* init if nil */ {
		cfg = new(Config)
	}
	// Enable status check by default.
	cfg.RaiseForStatus = true
	// Enable random UA if not set.
	if cfg.UserAgent == "" {
		cfg.RandomUserAgent = true
	}
	c := &retryablehttp.Client{
		HTTPClient:   cleanhttp.DefaultPooledClient(),
		RetryWaitMin: 1 * time.Second,
		RetryWaitMax: 3 * time.Second,
		RetryMax:     3,
		CheckRetry:   retryablehttp.DefaultRetryPolicy,
		Backoff:      retryablehttp.DefaultBackoff,
	}
	if cfg.Timeout > time.Second {
		c.HTTPClient.Timeout = cfg.Timeout
	}
	if cfg.Transport != nil {
		c.HTTPClient.Transport = cfg.Transport
	}
	if cfg.SkipVerify {
		if transport, ok := c.HTTPClient.Transport.(*http.Transport); ok {
			if transport.TLSClientConfig == nil {
				// init TLS config if is nil.
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}
	return New(c.StandardClient(), cfg)
}

func (f *Fetcher) Fetch(url string) (resp *http.Response, err error) {
	return f.Get(url)
}

// SetCookies adds cookies to the fetcher's jar, creating a jar on demand for
// fetchers that were initialized without cookie support. Providers that sync a
// browser session can call this before fetching protected images or pages.
func (f *Fetcher) SetCookies(rawURL string, cookies []*http.Cookie) error {
	if f == nil || f.client == nil {
		return fmt.Errorf("fetcher is not initialized")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid cookie URL")
	}
	if f.client.Jar == nil {
		f.client.Jar, err = cookiejar.New(nil)
		if err != nil {
			return fmt.Errorf("create cookie jar: %w", err)
		}
	}
	f.client.Jar.SetCookies(u, cookies)
	return nil
}

// SetUserAgent overrides the default User-Agent used by future requests.
func (f *Fetcher) SetUserAgent(userAgent string) {
	if f == nil || f.config == nil {
		return
	}
	f.config.UserAgent = userAgent
	f.config.RandomUserAgent = false
}

// SetReferer overrides the default Referer used by future requests.
func (f *Fetcher) SetReferer(referer string) {
	if f == nil || f.config == nil {
		return
	}
	f.config.Referer = referer
}

// SetHeaders replaces the default headers used by future requests.
func (f *Fetcher) SetHeaders(headers map[string]string) {
	if f == nil || f.config == nil {
		return
	}
	f.config.Headers = make(map[string]string, len(headers))
	for key, value := range headers {
		f.config.Headers[key] = value
	}
}

// SetTransport replaces the underlying RoundTripper, keeping the cookie jar
// and timeout. Providers that impersonate a browser TLS fingerprint can share
// one transport with the scraper.
func (f *Fetcher) SetTransport(rt http.RoundTripper) {
	if f == nil || f.client == nil || rt == nil {
		return
	}
	f.client.Transport = rt
}

func (f *Fetcher) Get(url string, opts ...Option) (resp *http.Response, err error) {
	return f.Request(http.MethodGet, url, nil, opts...)
}

func (f *Fetcher) Post(url string, body io.Reader, opts ...Option) (resp *http.Response, err error) {
	return f.Request(http.MethodPost, url, body, opts...)
}

func (f *Fetcher) Request(method, url string, body io.Reader, opts ...Option) (resp *http.Response, err error) {
	var req *http.Request
	if req, err = http.NewRequest(method, url, body); err != nil {
		return
	}
	c := &Context{
		req:    req,
		Config: *f.config, /* clone */
	}
	// compose options.
	var options []Option
	if len(c.Headers) > 0 {
		options = append(options, WithHeaders(c.Headers))
	}
	if c.UserAgent != "" {
		options = append(options, WithUserAgent(c.UserAgent))
	}
	if c.Referer != "" {
		options = append(options, WithReferer(c.Referer))
	}
	// apply options.
	for _, option := range append(options, opts...) {
		option.apply(c)
	}
	// make HTTP request.
	if resp, err = f.client.Do(req); err != nil {
		return
	}
	if c.RaiseForStatus && resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, errors.FromCode(resp.StatusCode)
	}
	return
}

func Fetch(url string) (*http.Response, error) {
	return DefaultFetcher.Fetch(url)
}

func Get(url string, opts ...Option) (*http.Response, error) {
	return DefaultFetcher.Get(url, opts...)
}

func Post(url string, body io.Reader, opts ...Option) (*http.Response, error) {
	return DefaultFetcher.Post(url, body, opts...)
}

func Request(method, url string, body io.Reader, opts ...Option) (*http.Response, error) {
	return DefaultFetcher.Request(method, url, body, opts...)
}

var (
	_ = Fetch
	_ = Get
	_ = Post
	_ = Request
)
