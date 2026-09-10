package scraper

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gocolly/colly/v2"
	"go.uber.org/atomic"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

var (
	_ provider.Provider             = (*Scraper)(nil)
	_ provider.ProxySetter          = (*Scraper)(nil)
	_ provider.RequestTimeoutSetter = (*Scraper)(nil)
)

// Scraper implements the basic Provider interface.
type Scraper struct {
	name     string
	baseURL  *url.URL
	priority *atomic.Float64
	language language.Tag
	c        *colly.Collector
}

// NewScraper returns a *Scraper that implements provider.Provider.
func NewScraper(name, base string, priority float64, lang language.Tag, opts ...Option) *Scraper {
	baseURL, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	s := &Scraper{
		name:     name,
		baseURL:  baseURL,
		priority: atomic.NewFloat64(priority),
		language: lang,
		c:        colly.NewCollector(),
	}
	for _, opt := range opts {
		// Apply options.
		if err := opt(s); err != nil {
			panic(err)
		}
	}
	return s
}

// NewDefaultScraper returns a *Scraper with default options enabled.
func NewDefaultScraper(name, baseURL string, priority float64, lang language.Tag, opts ...Option) *Scraper {
	return NewScraper(name, baseURL, priority, lang, append([]Option{
		WithAllowURLRevisit(),
		WithIgnoreRobotsTxt(),
		WithRandomUserAgent(),
	}, opts...)...)
}

func (s *Scraper) Name() string { return s.name }

func (s *Scraper) URL() *url.URL { return s.baseURL }

func (s *Scraper) Priority() float64 { return s.priority.Load() }

func (s *Scraper) SetPriority(v float64) { s.priority.Store(v) }

func (s *Scraper) Language() language.Tag { return s.language }

func (s *Scraper) NormalizeMovieID(id string) string { return id /* AS IS */ }

func (s *Scraper) ParseMovieIDFromURL(string) (string, error) { panic("unimplemented") }

func (s *Scraper) NormalizeActorID(id string) string { return id /* AS IS */ }

func (s *Scraper) ParseActorIDFromURL(string) (string, error) { panic("unimplemented") }

// ClonedCollector returns cloned internal collector.
func (s *Scraper) ClonedCollector() *colly.Collector { return s.c.Clone() }

// SetProxy sets http or socks5 proxy for HTTP requests.
func (s *Scraper) SetProxy(proxyURL string) error { return s.c.SetProxy(proxyURL) }

// SetCookies adds cookies to the collector's cookie jar. Providers that
// refresh browser sessions at runtime can use this method before cloning a
// collector for a request.
func (s *Scraper) SetCookies(rawURL string, cookies []*http.Cookie) error {
	return s.c.SetCookies(rawURL, cookies)
}

// SetUserAgent overrides the collector's User-Agent header.
func (s *Scraper) SetUserAgent(userAgent string) {
	s.c.UserAgent = userAgent
}

// SetReferer overrides the Referer header for future scraper requests.
func (s *Scraper) SetReferer(referer string) {
	s.c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Referer", referer)
	})
}

// SetRequestTimeout sets timeout for HTTP requests.
func (s *Scraper) SetRequestTimeout(timeout time.Duration) { s.c.SetRequestTimeout(timeout) }
