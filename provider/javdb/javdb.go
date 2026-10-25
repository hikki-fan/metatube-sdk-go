package javdb

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antchfx/htmlquery"
	"github.com/gocolly/colly/v2"
	"github.com/longbridgeapp/opencc"
	"golang.org/x/net/html"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/chrometransport"
	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider              = (*JavDB)(nil)
	_ provider.MovieImageFallbackProvider = (*JavDB)(nil)
	_ provider.MovieSearcher              = (*JavDB)(nil)
	_ provider.Fetcher                    = (*JavDB)(nil)
	_ provider.ProxySetter                = (*JavDB)(nil)
	_ provider.RequestTimeoutSetter       = (*JavDB)(nil)
)

const (
	Name = "JavDB"
	// Prefer JavDB when it returns an exact match. Fuzzy matches from other
	// providers are still ranked by the match score, so they remain fallbacks.
	Priority = 1010
)

var javDBMirrorHostPattern = regexp.MustCompile(`^javdb(?:\d+)?\.com$`)

func normalizeJavDBBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("invalid JavDB base URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if !javDBMirrorHostPattern.MatchString(host) || parsed.Port() != "" {
		return "", fmt.Errorf("invalid JavDB base URL")
	}
	return "https://" + host + "/", nil
}

const (
	baseURL   = "https://javdb.com/"
	movieURL  = "https://javdb.com/v/%s"
	searchURL = "https://javdb.com/search?f=all&q=%s"

	defaultMirrorEnabled = true
	defaultMirrorBaseURL = "https://jdforrepam.com/api"
	defaultMirrorTimeout = 8 * time.Second
)

// ErrLoginRequired is returned when JavDB redirects a request to a login page
// or responds with an explicit login form instead of movie/search content.
var ErrLoginRequired = errors.New("javdb: login required")

type javDBMirror interface {
	SearchMovie(keyword string) ([]*model.MovieSearchResult, error)
	GetMovieInfoByID(id string) (*model.MovieInfo, error)
}

func isJavDBOriginURL(raw string) bool {
	parsed, err := url.Parse(strings.ReplaceAll(strings.TrimSpace(raw), "%s", "fixture"))
	if err != nil || parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return javDBMirrorHostPattern.MatchString(host) || strings.HasSuffix(host, ".javdb.com")
}

func normalizeMirrorBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("invalid JavDB mirror base URL")
	}
	return strings.TrimRight(trimmed, "/"), nil
}

func parseMirrorBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "y":
		return true, nil
	case "0", "false", "no", "off", "n":
		return false, nil
	default:
		return strconv.ParseBool(strings.TrimSpace(value))
	}
}

func parseWesternSearchInterval(value string) (time.Duration, time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, fmt.Errorf("empty interval")
	}

	parts := strings.SplitN(value, "-", 2)
	if len(parts) == 1 {
		interval, err := time.ParseDuration(strings.TrimSpace(parts[0]))
		if err != nil || interval < 0 {
			return 0, 0, fmt.Errorf("invalid interval")
		}
		return interval, interval, nil
	}

	minInterval, err := time.ParseDuration(strings.TrimSpace(parts[0]))
	if err != nil || minInterval < 0 {
		return 0, 0, fmt.Errorf("invalid minimum interval")
	}
	maxInterval, err := time.ParseDuration(strings.TrimSpace(parts[1]))
	if err != nil || maxInterval < minInterval {
		return 0, 0, fmt.Errorf("invalid maximum interval")
	}
	return minInterval, maxInterval, nil
}

var (
	scorePattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

	// JavDB's Chinese locale currently returns Traditional Chinese genres even
	// when zh-CN is requested. Convert only genres to Simplified Chinese here;
	// titles, actor names and other Japanese metadata must remain untouched.
	genreConverter = func() *opencc.OpenCC {
		converter, err := opencc.New("tw2sp")
		if err != nil {
			panic(fmt.Sprintf("initialize JavDB genre converter: %v", err))
		}
		return converter
	}()
)

func simplifyGenre(genre string) string {
	simplified, err := genreConverter.Convert(genre)
	if err != nil {
		return genre
	}
	// OpenCC simplifies 企畫 to 企画; use the Mainland Chinese catalogue term.
	return strings.ReplaceAll(simplified, "企画", "企划")
}

type JavDB struct {
	*fetch.Fetcher
	*scraper.Scraper

	movieURL  string
	searchURL string

	westernLimiterMu     sync.Mutex
	westernSearchLimiter *WesternSearchLimiter

	cookieCloudMu          sync.Mutex
	cookieCloud            *cookieCloudClient
	cookieCloudProxy       string
	cookieCloudHTTPClient  *http.Client
	cookieCloudTimeout     time.Duration
	cookieCloudLastAttempt time.Time
	cookieCloudLastSuccess time.Time
	cookieCloudLastFailure time.Time
	cookieCloudLoaded      bool
	cookieCloudLastError   error

	mirrorMu         sync.Mutex
	mirrorEnabled    bool
	mirrorBaseURL    string
	mirrorProxy      string
	mirrorTimeout    time.Duration
	mirrorConfigured bool
	mirror           javDBMirror

	imageFallbackMu    sync.Mutex
	imageFallbackCache map[string]javDBImageFallbackCacheEntry
	imageFallbackCalls map[string]*javDBImageFallbackCall

	browserTransport *chrometransport.Transport
}

func New() *JavDB {
	fetchCfg := &fetch.Config{
		UserAgent: defaultJavDBUserAgent,
		Referer:   baseURL,
		Headers:   javDBImageHeaders(baseURL),
	}
	scraperOpts := []scraper.Option{
		scraper.WithUserAgent(defaultJavDBUserAgent),
		scraper.WithHeaders(javDBDocumentHeaders(baseURL)),
		scraper.WithCookies(baseURL, []*http.Cookie{
			{Name: "locale", Value: "zh-CN"},
		}),
	}
	browserTransport, _ := chrometransport.New()
	if browserTransport != nil {
		fetchCfg.Transport = browserTransport
		scraperOpts = append(scraperOpts, scraper.WithTransport(browserTransport))
	}
	return &JavDB{
		Fetcher: fetch.Default(fetchCfg),
		Scraper: scraper.NewDefaultScraper(
			Name, baseURL, Priority,
			language.Japanese,
			scraperOpts...),
		westernSearchLimiter: NewRandomWesternSearchLimiter(
			DefaultWesternSearchMinInterval,
			DefaultWesternSearchMaxInterval,
		),
		movieURL:           movieURL,
		searchURL:          searchURL,
		mirrorEnabled:      defaultMirrorEnabled,
		mirrorBaseURL:      defaultMirrorBaseURL,
		mirrorTimeout:      defaultMirrorTimeout,
		imageFallbackCache: make(map[string]javDBImageFallbackCacheEntry),
		imageFallbackCalls: make(map[string]*javDBImageFallbackCall),
		browserTransport:   browserTransport,
	}
}

// SetMirrorEnabled configures whether the public mirror is queried before
// falling back to the JavDB origin scraper.
func (jdb *JavDB) SetMirrorEnabled(enabled bool) {
	jdb.mirrorMu.Lock()
	jdb.mirrorEnabled = enabled
	jdb.mirrorConfigured = true
	if !enabled {
		jdb.mirror = nil
	}
	jdb.mirrorMu.Unlock()
}

func (jdb *JavDB) setMirrorClient(m javDBMirror) {
	jdb.mirrorMu.Lock()
	jdb.mirror = m
	jdb.mirrorEnabled = m != nil
	jdb.mirrorConfigured = true
	jdb.mirrorMu.Unlock()
}

func (jdb *JavDB) shouldUseMirror(targetOriginURL string) bool {
	jdb.mirrorMu.Lock()
	enabled := jdb.mirrorEnabled
	configured := jdb.mirrorConfigured || jdb.mirror != nil
	jdb.mirrorMu.Unlock()
	if !enabled {
		return false
	}
	if configured {
		return true
	}
	return isJavDBOriginURL(targetOriginURL)
}

func (jdb *JavDB) getMirrorClient() (javDBMirror, error) {
	jdb.mirrorMu.Lock()
	defer jdb.mirrorMu.Unlock()
	if !jdb.mirrorEnabled {
		return nil, nil
	}
	if jdb.mirror != nil {
		return jdb.mirror, nil
	}
	base := strings.TrimSpace(jdb.mirrorBaseURL)
	if base == "" {
		base = defaultMirrorBaseURL
	}
	timeout := jdb.mirrorTimeout
	if timeout <= 0 {
		timeout = defaultMirrorTimeout
	}
	client, err := newMirrorClient(base, jdb.mirrorProxy, timeout)
	if err != nil {
		return nil, err
	}
	jdb.mirror = client
	return client, nil
}

func (jdb *JavDB) getWesternLimiter() *WesternSearchLimiter {
	jdb.westernLimiterMu.Lock()
	defer jdb.westernLimiterMu.Unlock()
	if jdb.westernSearchLimiter == nil {
		jdb.westernSearchLimiter = NewRandomWesternSearchLimiter(
			DefaultWesternSearchMinInterval,
			DefaultWesternSearchMaxInterval,
		)
	}
	return jdb.westernSearchLimiter
}

// SetWesternSearchLimiter replaces the Western scene search limiter.
func (jdb *JavDB) SetWesternSearchLimiter(limiter *WesternSearchLimiter) {
	jdb.westernLimiterMu.Lock()
	defer jdb.westernLimiterMu.Unlock()
	jdb.westernSearchLimiter = limiter
}

// SetWesternSearchInterval configures the interval between Western scene searches.
func (jdb *JavDB) SetWesternSearchInterval(interval time.Duration) {
	jdb.getWesternLimiter().SetInterval(interval)
}

// SetProxy applies the provider proxy to JavDB page/image requests and
// remembers it for the CookieCloud client. CookieCloud must use the same
// egress as JavDB so browser session cookies remain valid. When the Chrome
// TLS transport is installed, proxy updates go there instead of colly.SetProxy,
// which would replace the impersonating RoundTripper.
func (jdb *JavDB) SetProxy(proxyURL string) error {
	if jdb.browserTransport != nil {
		if err := jdb.browserTransport.SetProxy(proxyURL); err != nil {
			return err
		}
	} else if err := jdb.Scraper.SetProxy(proxyURL); err != nil {
		return err
	}
	jdb.cookieCloudMu.Lock()
	jdb.cookieCloudProxy = proxyURL
	jdb.cookieCloudHTTPClient = nil
	jdb.cookieCloudMu.Unlock()
	jdb.mirrorMu.Lock()
	jdb.mirrorProxy = proxyURL
	jdb.mirror = nil
	jdb.mirrorMu.Unlock()
	return nil
}

// SetRequestTimeout keeps CookieCloud refreshes from outliving the provider's
// request timeout.
func (jdb *JavDB) SetRequestTimeout(timeout time.Duration) {
	jdb.Scraper.SetRequestTimeout(timeout)
	if jdb.browserTransport != nil {
		_ = jdb.browserTransport.SetTimeout(timeout)
	}
	jdb.cookieCloudMu.Lock()
	jdb.cookieCloudTimeout = timeout
	jdb.cookieCloudHTTPClient = nil
	jdb.cookieCloudMu.Unlock()
}

// SetCookies applies a synchronized session to both page scraping and resource
// fetching. The explicit method also resolves the two embedded helper methods
// with the same name for callers that need to add cookies manually.
func (jdb *JavDB) SetCookies(rawURL string, cookies []*http.Cookie) error {
	if err := jdb.Scraper.SetCookies(rawURL, cookies); err != nil {
		return err
	}
	return jdb.Fetcher.SetCookies(rawURL, cookies)
}

// SetConfig enables optional CookieCloud browser-session synchronization.
// Environment variables are exposed through the standard provider config:
//
//	MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_HOST
//	MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_UUID
//	MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_PASSWORD
//	MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_REFRESH_INTERVAL (optional)
//	MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_USER_AGENT (optional; TLS fingerprint stays Chrome 150)
//	MT_MOVIE_PROVIDER_JAVDB__BASE_URL (optional, defaults to https://javdb.com/)
//	MT_MOVIE_PROVIDER_JAVDB__MIRROR_ENABLED (optional, defaults to true)
//	MT_MOVIE_PROVIDER_JAVDB__MIRROR_BASE_URL (optional, defaults to https://jdforrepam.com/api)
//	MT_MOVIE_PROVIDER_JAVDB__MIRROR_TIMEOUT (optional, defaults to 8s)
//	MT_MOVIE_PROVIDER_JAVDB__WESTERN_SEARCH_INTERVAL (optional, a duration or
//		a range such as 10s-20s; default 10s-20s)
//
// The cookie refresh is lazy and cached for ten minutes by default, so service
// startup does not depend on CookieCloud availability.
func (jdb *JavDB) SetConfig(config provider.Config) error {
	if config == nil {
		return nil
	}
	var mirrorEnabled *bool
	if value, ok := configValue(config, "mirror_enabled", "javdb_mirror_enabled"); ok && value != "" {
		enabled, err := parseMirrorBool(value)
		if err != nil {
			return fmt.Errorf("invalid JavDB mirror enabled setting")
		}
		mirrorEnabled = &enabled
	}
	var mirrorBase string
	var mirrorBaseConfigured bool
	if value, ok := configValue(config, "mirror_base_url", "javdb_mirror_base_url"); ok && value != "" {
		var err error
		mirrorBase, err = normalizeMirrorBaseURL(value)
		if err != nil {
			return err
		}
		mirrorBaseConfigured = true
	}
	var mirrorTimeout time.Duration
	if value, ok := configValue(config, "mirror_timeout", "javdb_mirror_timeout"); ok && value != "" {
		var err error
		mirrorTimeout, err = time.ParseDuration(value)
		if err != nil || mirrorTimeout <= 0 {
			return fmt.Errorf("invalid JavDB mirror timeout")
		}
	}
	if mirrorEnabled != nil || mirrorBaseConfigured || mirrorTimeout > 0 {
		jdb.mirrorMu.Lock()
		if mirrorEnabled != nil {
			jdb.mirrorEnabled = *mirrorEnabled
		}
		if mirrorBaseConfigured {
			jdb.mirrorBaseURL = mirrorBase
			jdb.mirrorConfigured = true
		}
		if mirrorTimeout > 0 {
			jdb.mirrorTimeout = mirrorTimeout
		}
		jdb.mirror = nil
		jdb.mirrorMu.Unlock()
	}
	if value, ok := configValue(config, "javdb_western_search_interval", "western_search_interval"); ok && value != "" {
		minInterval, maxInterval, err := parseWesternSearchInterval(value)
		if err != nil {
			return fmt.Errorf("invalid JavDB Western search interval")
		}
		jdb.getWesternLimiter().SetIntervalRange(minInterval, maxInterval)
	}
	if value, ok := configValue(config, "base_url", "javdb_base_url"); ok && value != "" {
		base, err := normalizeJavDBBaseURL(value)
		if err != nil {
			return err
		}
		jdb.movieURL = base + "v/%s"
		jdb.searchURL = base + "search?f=all&q=%s"
		jdb.Scraper.SetHeaders(javDBDocumentHeaders(base))
		jdb.Scraper.SetReferer(base)
		jdb.Fetcher.SetHeaders(javDBImageHeaders(base))
		jdb.Fetcher.SetReferer(base)
	}
	host, hostSet := configValue(config, "cookiecloud_host", "cookie_cloud_host")
	uuid, uuidSet := configValue(config, "cookiecloud_uuid", "cookie_cloud_uuid")
	password, passwordSet := configValue(config, "cookiecloud_password", "cookie_cloud_password")
	if !hostSet && !uuidSet && !passwordSet {
		return nil
	}
	if host == "" || uuid == "" || password == "" {
		return fmt.Errorf("JavDB CookieCloud requires host, uuid and password")
	}
	parsedHost, err := url.Parse(host)
	if err != nil || parsedHost.Host == "" ||
		(parsedHost.Scheme != "http" && parsedHost.Scheme != "https") {
		return fmt.Errorf("invalid JavDB CookieCloud host")
	}
	refreshInterval := defaultCookieCloudRefreshInterval
	if value, ok := configValue(config, "cookiecloud_refresh_interval", "cookie_cloud_refresh_interval"); ok && value != "" {
		refreshInterval, err = time.ParseDuration(value)
		if err != nil || refreshInterval < time.Second {
			return fmt.Errorf("invalid JavDB CookieCloud refresh interval")
		}
	}
	if value, ok := configValue(config, "cookiecloud_user_agent", "cookie_cloud_user_agent"); ok && value != "" {
		jdb.Scraper.SetUserAgent(value)
		jdb.Fetcher.SetUserAgent(value)
	}

	jdb.cookieCloudMu.Lock()
	jdb.cookieCloud = &cookieCloudClient{
		host:            strings.TrimRight(host, "/"),
		uuid:            uuid,
		password:        password,
		refreshInterval: refreshInterval,
	}
	jdb.cookieCloudLastAttempt = time.Time{}
	jdb.cookieCloudLastSuccess = time.Time{}
	jdb.cookieCloudLastFailure = time.Time{}
	jdb.cookieCloudLoaded = false
	jdb.cookieCloudLastError = nil
	jdb.cookieCloudHTTPClient = nil
	jdb.cookieCloudMu.Unlock()
	return nil
}

func configValue(config provider.Config, keys ...string) (string, bool) {
	for _, key := range keys {
		if !config.Has(key) {
			continue
		}
		value, err := config.GetString(key)
		if err != nil {
			return "", true
		}
		return strings.TrimSpace(value), true
	}
	return "", false
}

func (jdb *JavDB) cookieCloudHTTPClientLocked() *http.Client {
	if jdb.cookieCloudHTTPClient != nil {
		return jdb.cookieCloudHTTPClient
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = &http.Transport{}
	}
	if jdb.cookieCloudProxy != "" {
		if proxyURL, err := url.Parse(jdb.cookieCloudProxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}
	timeout := jdb.cookieCloudTimeout
	if timeout < time.Second {
		timeout = time.Minute
	}
	jdb.cookieCloudHTTPClient = &http.Client{Transport: transport, Timeout: timeout}
	return jdb.cookieCloudHTTPClient
}

func (jdb *JavDB) refreshCookieCloudLocked(now time.Time, force bool) error {
	jdb.cookieCloudLastAttempt = now
	jdb.cookieCloud.httpClient = jdb.cookieCloudHTTPClientLocked()
	cookies, err := jdb.cookieCloud.fetchCookies()
	if err != nil {
		err = jdb.recordCookieCloudRefreshFailure(err)
		if !force && jdb.cookieCloudLoaded {
			return nil
		}
		return err
	}
	if len(cookies) == 0 {
		err = jdb.recordCookieCloudRefreshFailure(fmt.Errorf("CookieCloud contains no JavDB cookies"))
		if !force && jdb.cookieCloudLoaded {
			return nil
		}
		return err
	}
	for _, cookie := range cookies {
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if domain == "" {
			domain = "javdb.com"
		}
		cookieURL := "https://" + domain + "/"
		if err = jdb.SetCookies(cookieURL, []*http.Cookie{cookie}); err != nil {
			err = jdb.recordCookieCloudRefreshFailure(fmt.Errorf("apply CookieCloud cookies: %w", err))
			if !force && jdb.cookieCloudLoaded {
				return nil
			}
			return err
		}
	}
	jdb.cookieCloudLoaded = true
	jdb.cookieCloudLastSuccess = now
	jdb.cookieCloudLastFailure = time.Time{}
	jdb.cookieCloudLastError = nil
	return nil
}

func (jdb *JavDB) recordCookieCloudRefreshFailure(err error) error {
	jdb.cookieCloudLastFailure = time.Now()
	jdb.cookieCloudLastError = err
	return err
}

// ensureCookieCloudCookies refreshes the CookieCloud session lazily. A
// previously loaded session is retained when a later refresh temporarily
// fails, avoiding an unnecessary fallback to anonymous JavDB requests.
func (jdb *JavDB) ensureCookieCloudCookies() error {
	jdb.cookieCloudMu.Lock()
	defer jdb.cookieCloudMu.Unlock()
	if jdb.cookieCloud == nil {
		return nil
	}
	now := time.Now()
	if jdb.cookieCloudLoaded && now.Sub(jdb.cookieCloudLastSuccess) < jdb.cookieCloud.refreshInterval {
		return nil
	}
	if !jdb.cookieCloudLastAttempt.IsZero() && now.Sub(jdb.cookieCloudLastAttempt) < time.Minute {
		if jdb.cookieCloudLoaded {
			return nil
		}
		return jdb.cookieCloudLastError
	}
	return jdb.refreshCookieCloudLocked(now, false)
}

func (jdb *JavDB) isCookieCloudConfigured() bool {
	jdb.cookieCloudMu.Lock()
	defer jdb.cookieCloudMu.Unlock()
	return jdb.cookieCloud != nil
}

func (jdb *JavDB) forceCookieCloudRefresh(attemptStart time.Time) error {
	jdb.cookieCloudMu.Lock()
	defer jdb.cookieCloudMu.Unlock()
	if jdb.cookieCloud == nil {
		return errors.New("CookieCloud is not configured")
	}
	if !attemptStart.IsZero() && jdb.cookieCloudLastSuccess.After(attemptStart) {
		return nil
	}
	if !jdb.cookieCloudLastFailure.IsZero() &&
		((!attemptStart.IsZero() && jdb.cookieCloudLastFailure.After(attemptStart)) || time.Since(jdb.cookieCloudLastFailure) < 5*time.Second) {
		if jdb.cookieCloudLastError != nil {
			return jdb.cookieCloudLastError
		}
		return errors.New("CookieCloud refresh recently failed")
	}
	return jdb.refreshCookieCloudLocked(time.Now(), true)
}

func isForbidden(statusCode int, err error) bool {
	if statusCode == http.StatusForbidden {
		return true
	}
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "403") || strings.Contains(msg, "forbidden") {
			return true
		}
	}
	return false
}

func isJavDBLoginURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch path.Clean(strings.ToLower(parsed.Path)) {
	case "/login", "/signin", "/sign_in", "/users/login", "/users/sign_in", "/accounts/login":
		return true
	default:
		return false
	}
}

func htmlAttribute(node *html.Node, name string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val
		}
	}
	return ""
}

func loginFormContainsCredentials(form *html.Node) bool {
	action := strings.ToLower(htmlAttribute(form, "action"))
	formID := strings.ToLower(htmlAttribute(form, "id"))
	formClass := strings.ToLower(htmlAttribute(form, "class"))
	loginMarker := strings.Contains(action, "login") || strings.Contains(formID, "login") || strings.Contains(formClass, "login")
	hasPassword, hasIdentity := false, false
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "input") {
			inputType := strings.ToLower(htmlAttribute(node, "type"))
			name := strings.ToLower(htmlAttribute(node, "name"))
			autocomplete := strings.ToLower(htmlAttribute(node, "autocomplete"))
			if inputType == "password" {
				hasPassword = true
			}
			if inputType == "email" || strings.Contains(autocomplete, "username") ||
				name == "username" || name == "user" || name == "account" ||
				name == "email" || name == "login" {
				hasIdentity = true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(form)
	return hasPassword && (hasIdentity || loginMarker)
}

func containsJavDBLoginForm(node *html.Node) bool {
	if node == nil {
		return false
	}
	if node.Type == html.ElementNode && strings.EqualFold(node.Data, "form") && loginFormContainsCredentials(node) {
		return true
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if containsJavDBLoginForm(child) {
			return true
		}
	}
	return false
}

func isJavDBLoginResponse(response *colly.Response) bool {
	if response == nil {
		return false
	}
	if response.Headers != nil && response.StatusCode >= 300 && response.StatusCode < 400 {
		location := response.Headers.Get("Location")
		if location != "" {
			if response.Request != nil && response.Request.URL != nil {
				if parsedLocation, err := response.Request.URL.Parse(location); err == nil {
					location = parsedLocation.String()
				}
			}
			if isJavDBLoginURL(location) {
				return true
			}
		}
	}
	if response.Request != nil && response.Request.URL != nil && isJavDBLoginURL(response.Request.URL.String()) {
		return true
	}
	if len(response.Body) == 0 {
		return false
	}
	document, err := html.Parse(strings.NewReader(string(response.Body)))
	if err != nil || hasJavDBMovieContent(document) {
		return false
	}
	return containsJavDBLoginForm(document)
}

func hasJavDBMovieContent(document *html.Node) bool {
	if document == nil {
		return false
	}
	if len(htmlquery.Find(document, `//div[contains(@class,"video-detail")]//h2[contains(@class,"title")]//strong[1]`)) > 0 &&
		len(htmlquery.Find(document, `//div[contains(@class,"column-video-cover")]//img[1]`)) > 0 {
		return true
	}
	return len(htmlquery.Find(document, `//div[contains(@class,"movie-list")]//div[contains(@class,"item")]/a[contains(@class,"box")]`)) > 0
}

func javDBRequestError(statusCode int, err error) error {
	if err != nil {
		return err
	}
	if statusCode == http.StatusForbidden {
		return errors.New(http.StatusText(http.StatusForbidden))
	}
	return nil
}

func isSessionRecoveryError(statusCode int, err error) bool {
	return isForbidden(statusCode, err) || errors.Is(err, ErrLoginRequired)
}

func (jdb *JavDB) refreshSessionAfterFailure(attemptStart time.Time, attemptErr error) (bool, error) {
	if !jdb.isCookieCloudConfigured() {
		return false, attemptErr
	}
	reason := "403 response"
	if errors.Is(attemptErr, ErrLoginRequired) {
		reason = "login page"
	}
	log.Printf("JavDB origin returned a %s; refreshing CookieCloud session", reason)
	if err := jdb.forceCookieCloudRefresh(attemptStart); err != nil {
		log.Printf("JavDB CookieCloud session refresh failed; returning the origin error")
		if attemptErr != nil {
			return false, fmt.Errorf("%w (CookieCloud refresh failed: %v)", attemptErr, err)
		}
		return false, err
	}
	log.Printf("JavDB CookieCloud session refreshed; retrying the origin once")
	return true, nil
}

// JavDB uses a case-sensitive opaque ID in /v/<id> URLs.
func (jdb *JavDB) NormalizeMovieID(id string) string {
	return strings.TrimSpace(id)
}

func (jdb *JavDB) ParseMovieIDFromURL(rawURL string) (string, error) {
	homepage, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(homepage.Path, "/v/") {
		return "", provider.ErrInvalidURL
	}
	return jdb.NormalizeMovieID(path.Base(homepage.Path)), nil
}

func (jdb *JavDB) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	endpoint := jdb.movieURL
	if endpoint == "" {
		endpoint = movieURL
	}
	return jdb.GetMovieInfoByURL(fmt.Sprintf(endpoint, id))
}

func (jdb *JavDB) getMovieInfoByURL(rawURL string) (*model.MovieInfo, int, error) {
	return jdb.getMovieInfoByURLWithFinalURL(rawURL, nil)
}

func (jdb *JavDB) getMovieInfoByURLWithFinalURL(rawURL string, finalURL *string) (*model.MovieInfo, int, error) {
	if finalURL != nil {
		*finalURL = ""
	}
	id, err := jdb.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, 0, err
	}

	info := &model.MovieInfo{
		ID:            id,
		Provider:      jdb.Name(),
		Homepage:      rawURL,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	c := jdb.ClonedCollector()

	var statusCode int
	var loginRequired bool
	c.OnResponse(func(r *colly.Response) {
		if finalURL != nil && r != nil && r.Request != nil && r.Request.URL != nil {
			*finalURL = r.Request.URL.String()
		}
		if isJavDBLoginResponse(r) {
			loginRequired = true
		}
	})
	c.OnError(func(r *colly.Response, err error) {
		if r != nil {
			statusCode = r.StatusCode
		}
	})

	c.OnXML(`//div[contains(@class,"video-detail")]//h2[contains(@class,"title")]`, func(e *colly.XMLElement) {
		info.Number = strings.TrimSpace(e.ChildText(`.//strong[1]`))
		info.Title = strings.TrimSpace(e.ChildText(`.//strong[contains(@class,"current-title")]`))
	})

	c.OnXML(`//div[contains(@class,"column-video-cover")]`, func(e *colly.XMLElement) {
		if cover := extractImageSrc(e, `.//img[1]`); cover != "" {
			info.CoverURL = cover
			info.ThumbURL = cover
		}
	})

	c.OnXML(`//nav[contains(@class,"movie-panel-info")]/div[contains(@class,"panel-block")]`, func(e *colly.XMLElement) {
		label := strings.TrimSpace(e.ChildText(`.//strong[1]`))
		value := strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]`))
		switch strings.TrimSuffix(label, ":") {
		case "番號", "番号", "ID":
			if value != "" {
				info.Number = strings.ReplaceAll(value, " ", "")
			}
		case "日期", "発売日", "Date":
			info.ReleaseDate = parser.ParseDate(value)
		case "時長", "时长", "収録時間", "Duration":
			info.Runtime = parser.ParseRuntime(value)
		case "導演", "导演", "監督", "Director":
			info.Director = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "片商", "メーカー", "Maker":
			info.Maker = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "發行", "发行", "発行", "Publisher":
			info.Label = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "系列", "シリーズ", "Series":
			info.Series = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "評分", "评分", "評価", "Rating":
			if score := scorePattern.FindString(value); score != "" {
				info.Score = parser.ParseScore(score)
			}
		case "類別", "类别", "カテゴリ", "Tags":
			for _, genre := range e.ChildTexts(`.//span[contains(@class,"value")]//a`) {
				if genre = strings.TrimSpace(genre); genre != "" {
					info.Genres = append(info.Genres, simplifyGenre(genre))
				}
			}
		case "演員", "演员", "出演者", "Actors":
			for _, aNode := range htmlquery.Find(e.DOM.(*html.Node), `.//span[contains(@class,"value")]//a`) {
				// JavDB movie actor lists are female-only by policy. Do not
				// guess from an unmarked name: keep only explicit female markers.
				if !isFemaleActorNode(aNode) {
					continue
				}
				if actor := extractActorName(aNode); actor != "" {
					info.Actors = append(info.Actors, actor)
				}
			}
		}
	})

	c.OnXML(`//div[contains(@class,"preview-images")]//a[@data-fancybox]`, func(e *colly.XMLElement) {
		if imageURL := e.Request.AbsoluteURL(e.Attr("href")); imageURL != "" {
			info.PreviewImages = append(info.PreviewImages, imageURL)
		}
	})

	err = c.Visit(info.Homepage)
	if loginRequired {
		return nil, statusCode, ErrLoginRequired
	}
	if err != nil {
		return nil, statusCode, err
	}
	return info, statusCode, nil
}

func (jdb *JavDB) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := jdb.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	if jdb.shouldUseMirror(rawURL) {
		if mirror, mirrorErr := jdb.getMirrorClient(); mirrorErr == nil && mirror != nil {
			info, mirrorErr := mirror.GetMovieInfoByID(id)
			if mirrorErr == nil && info != nil && info.ID == id && info.IsValid() {
				log.Printf("JavDB mirror detail lookup hit")
				return info, nil
			}
		}
		log.Printf("JavDB mirror detail lookup missed; using origin")
	}
	return jdb.getMovieInfoByURLWithRecovery(rawURL)
}

func (jdb *JavDB) getMovieInfoByURLWithRecovery(rawURL string) (*model.MovieInfo, error) {
	return jdb.getMovieInfoByURLWithRecoveryAndFinalURL(rawURL, nil)
}

func (jdb *JavDB) getMovieInfoByURLWithRecoveryAndFinalURL(rawURL string, finalURL *string) (*model.MovieInfo, error) {
	if finalURL != nil {
		*finalURL = ""
	}
	if err := jdb.ensureCookieCloudCookies(); err != nil {
		return nil, err
	}
	attemptStart := time.Now()
	info, statusCode, err := jdb.getMovieInfoByURLWithFinalURL(rawURL, finalURL)
	firstErr := javDBRequestError(statusCode, err)
	if isSessionRecoveryError(statusCode, firstErr) {
		refreshed, refreshErr := jdb.refreshSessionAfterFailure(attemptStart, firstErr)
		if refreshErr != nil {
			return nil, refreshErr
		}
		if refreshed {
			infoRetry, statusCodeRetry, retryErr := jdb.getMovieInfoByURLWithFinalURL(rawURL, finalURL)
			retryErr = javDBRequestError(statusCodeRetry, retryErr)
			if retryErr != nil {
				return nil, retryErr
			}
			return infoRetry, nil
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return info, nil
}

func (jdb *JavDB) NormalizeMovieKeyword(keyword string) string {
	return strings.ToUpper(strings.TrimSpace(keyword))
}

func (jdb *JavDB) searchMovie(keyword string) ([]*model.MovieSearchResult, int, error) {
	var results []*model.MovieSearchResult
	c := jdb.ClonedCollector()

	var statusCode int
	var loginRequired bool
	c.OnResponse(func(r *colly.Response) {
		if isJavDBLoginResponse(r) {
			loginRequired = true
		}
	})
	c.OnError(func(r *colly.Response, err error) {
		if r != nil {
			statusCode = r.StatusCode
		}
	})

	c.OnXML(`//div[contains(@class,"movie-list")]//div[contains(@class,"item")]/a[contains(@class,"box")]`, func(e *colly.XMLElement) {
		homepage := e.Request.AbsoluteURL(e.Attr("href"))
		id, parseErr := jdb.ParseMovieIDFromURL(homepage)
		if parseErr != nil {
			return
		}
		cover := extractImageSrc(e, `.//div[contains(@class,"cover")]//img[1]`)
		results = append(results, &model.MovieSearchResult{
			ID:          id,
			Number:      strings.TrimSpace(e.ChildText(`.//div[contains(@class,"video-title")]/strong[1]`)),
			Title:       strings.TrimSpace(e.Attr("title")),
			Provider:    jdb.Name(),
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			ReleaseDate: parser.ParseDate(strings.TrimSpace(e.ChildText(`.//div[contains(@class,"meta")]`))),
		})
	})

	endpoint := jdb.searchURL
	if endpoint == "" {
		endpoint = searchURL
	}
	err := c.Visit(fmt.Sprintf(endpoint, url.QueryEscape(keyword)))
	if loginRequired {
		return nil, statusCode, ErrLoginRequired
	}
	if err != nil {
		return nil, statusCode, err
	}
	return results, statusCode, nil
}

func (jdb *JavDB) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	if jdb.shouldUseMirror(jdb.searchURL) {
		if mirror, mirrorErr := jdb.getMirrorClient(); mirrorErr == nil && mirror != nil {
			results, mirrorErr := mirror.SearchMovie(keyword)
			if mirrorErr == nil && len(results) > 0 {
				log.Printf("JavDB mirror search hit")
				return results, nil
			}
		}
		log.Printf("JavDB mirror search missed; using origin")
	}
	if err := jdb.ensureCookieCloudCookies(); err != nil {
		return nil, err
	}
	if number.IsWestern(keyword) {
		jdb.getWesternLimiter().Wait()
	}
	attemptStart := time.Now()
	results, statusCode, err := jdb.searchMovie(keyword)
	firstErr := javDBRequestError(statusCode, err)
	if isSessionRecoveryError(statusCode, firstErr) {
		refreshed, refreshErr := jdb.refreshSessionAfterFailure(attemptStart, firstErr)
		if refreshErr != nil {
			return nil, refreshErr
		}
		if refreshed {
			resultsRetry, statusCodeRetry, retryErr := jdb.searchMovie(keyword)
			retryErr = javDBRequestError(statusCodeRetry, retryErr)
			if retryErr != nil {
				return nil, retryErr
			}
			return resultsRetry, nil
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func extractImageSrc(e *colly.XMLElement, xpath string) string {
	for _, attr := range []string{"data-src", "data-original", "data-lazy-src", "data-url", "src"} {
		if src := strings.TrimSpace(e.ChildAttr(xpath, attr)); src != "" {
			return e.Request.AbsoluteURL(src)
		}
	}
	return ""
}

func isMaleActorNode(n *html.Node) bool {
	if isMaleElement(n) {
		return true
	}
	for sib := n.NextSibling; sib != nil; sib = sib.NextSibling {
		if sib.Type == html.ElementNode {
			if sib.Data == "a" {
				break
			}
			if isMaleElement(sib) {
				return true
			}
			break
		}
	}
	if containsMaleElement(n) {
		return true
	}
	return false
}

func isFemaleActorNode(n *html.Node) bool {
	if isFemaleElement(n) {
		return true
	}
	for sib := n.NextSibling; sib != nil; sib = sib.NextSibling {
		if sib.Type == html.ElementNode {
			if sib.Data == "a" {
				break
			}
			if isFemaleElement(sib) {
				return true
			}
			break
		}
	}
	return containsFemaleElement(n)
}

func isMaleElement(n *html.Node) bool {
	var class string
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "class") {
			class = attr.Val
			break
		}
	}
	classLower := strings.ToLower(class)
	if strings.Contains(classLower, "male") && !strings.Contains(classLower, "female") {
		return true
	}
	text := getNodeText(n)
	if strings.Contains(text, "♂") && !strings.Contains(text, "♀") {
		return true
	}
	return false
}

func isFemaleElement(n *html.Node) bool {
	var class string
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "class") {
			class = attr.Val
			break
		}
	}
	classLower := strings.ToLower(class)
	if strings.Contains(classLower, "female") {
		return true
	}
	text := getNodeText(n)
	return strings.Contains(text, "♀")
}

func containsMaleElement(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if isMaleElement(c) || containsMaleElement(c) {
				return true
			}
		}
	}
	return false
}

func containsFemaleElement(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if isFemaleElement(c) || containsFemaleElement(c) {
				return true
			}
		}
	}
	return false
}

func extractActorName(n *html.Node) string {
	var sb strings.Builder
	var collectText func(*html.Node)
	collectText = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (isMaleElement(c) || isFemaleElement(c)) {
				continue
			}
			collectText(c)
		}
	}
	collectText(n)
	name := strings.TrimSpace(sb.String())
	name = strings.Trim(name, " \t\n\r,♀♂")
	return strings.TrimSpace(name)
}

func getNodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func init() {
	provider.Register(Name, New)
}
