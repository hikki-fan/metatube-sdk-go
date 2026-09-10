package javdb

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
	"golang.org/x/net/html"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.ActorProvider        = (*JavDBActor)(nil)
	_ provider.ActorSearcher        = (*JavDBActor)(nil)
	_ provider.Fetcher              = (*JavDBActor)(nil)
	_ provider.ProxySetter          = (*JavDBActor)(nil)
	_ provider.RequestTimeoutSetter = (*JavDBActor)(nil)
	_ provider.ConfigSetter         = (*JavDBActor)(nil)
)

const (
	ActorProviderName = "JavDBActor"
	ActorPriority     = 1000
	actorBaseURL      = "https://javdb.com/"
	actorPageURL      = "https://javdb.com/actors/%s"
	actorSearchURL    = "https://javdb.com/search?f=actor&q=%s"
)

type JavDBActor struct {
	*fetch.Fetcher
	*scraper.Scraper
	// searchEndpoint is kept overridable for fixture tests. Production uses
	// actorSearchURL and still builds the keyword through QueryEscape.
	searchEndpoint string
	pageEndpoint   string

	cookieCloudMu          sync.Mutex
	cookieCloud            *cookieCloudClient
	cookieCloudProxy       string
	cookieCloudHTTPClient  *http.Client
	cookieCloudTimeout     time.Duration
	cookieCloudLastAttempt time.Time
	cookieCloudLastSuccess time.Time
	cookieCloudLoaded      bool
	cookieCloudLastError   error
}

func NewActor() *JavDBActor {
	return &JavDBActor{
		Fetcher: fetch.Default(&fetch.Config{Referer: actorBaseURL}),
		Scraper: scraper.NewDefaultScraper(
			ActorProviderName, actorBaseURL, ActorPriority,
			language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Accept-Language": "zh-CN,zh;q=0.9,ja;q=0.8,en;q=0.7",
				"Referer":         actorBaseURL,
			}),
			scraper.WithCookies(actorBaseURL, []*http.Cookie{
				{Name: "locale", Value: "zh-CN"},
			})),
		searchEndpoint: actorSearchURL,
		pageEndpoint:   actorPageURL,
	}
}

// SetProxy applies the provider proxy to the scraper and CookieCloud.
func (jdb *JavDBActor) SetProxy(proxyURL string) error {
	if err := jdb.Scraper.SetProxy(proxyURL); err != nil {
		return err
	}
	jdb.cookieCloudMu.Lock()
	jdb.cookieCloudProxy = proxyURL
	jdb.cookieCloudHTTPClient = nil
	jdb.cookieCloudMu.Unlock()
	return nil
}

// SetRequestTimeout sets timeout for scraper and CookieCloud.
func (jdb *JavDBActor) SetRequestTimeout(timeout time.Duration) {
	jdb.Scraper.SetRequestTimeout(timeout)
	jdb.cookieCloudMu.Lock()
	jdb.cookieCloudTimeout = timeout
	jdb.cookieCloudHTTPClient = nil
	jdb.cookieCloudMu.Unlock()
}

// SetCookies applies cookies to both scraper and fetcher.
func (jdb *JavDBActor) SetCookies(rawURL string, cookies []*http.Cookie) error {
	if err := jdb.Scraper.SetCookies(rawURL, cookies); err != nil {
		return err
	}
	return jdb.Fetcher.SetCookies(rawURL, cookies)
}

// SetConfig enables optional CookieCloud session synchronization for JavDBActor.
// Environment variables:
//
//	MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_HOST
//	MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_UUID
//	MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_PASSWORD
//	MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_REFRESH_INTERVAL (optional)
//	MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_USER_AGENT (optional)
//	MT_ACTOR_PROVIDER_JAVDBACTOR__BASE_URL (optional, defaults to https://javdb.com/)
func (jdb *JavDBActor) SetConfig(config provider.Config) error {
	if config == nil {
		return nil
	}
	if value, ok := configValue(config, "base_url", "javdb_base_url"); ok && value != "" {
		base, err := normalizeJavDBBaseURL(value)
		if err != nil {
			return err
		}
		jdb.pageEndpoint = base + "actors/%s"
		jdb.searchEndpoint = base + "search?f=actor&q=%s"
		jdb.Scraper.SetReferer(base)
	}
	host, hostSet := configValue(config, "cookiecloud_host", "cookie_cloud_host")
	uuid, uuidSet := configValue(config, "cookiecloud_uuid", "cookie_cloud_uuid")
	password, passwordSet := configValue(config, "cookiecloud_password", "cookie_cloud_password")
	if !hostSet && !uuidSet && !passwordSet {
		return nil
	}
	if host == "" || uuid == "" || password == "" {
		return fmt.Errorf("JavDBActor CookieCloud requires host, uuid and password")
	}
	parsedHost, err := url.Parse(host)
	if err != nil || parsedHost.Host == "" ||
		(parsedHost.Scheme != "http" && parsedHost.Scheme != "https") {
		return fmt.Errorf("invalid JavDBActor CookieCloud host")
	}
	refreshInterval := defaultCookieCloudRefreshInterval
	if value, ok := configValue(config, "cookiecloud_refresh_interval", "cookie_cloud_refresh_interval"); ok && value != "" {
		refreshInterval, err = time.ParseDuration(value)
		if err != nil || refreshInterval < time.Second {
			return fmt.Errorf("invalid JavDBActor CookieCloud refresh interval")
		}
	}
	if value, ok := configValue(config, "cookiecloud_user_agent", "cookie_cloud_user_agent"); ok && value != "" {
		jdb.Scraper.SetUserAgent(value)
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
	jdb.cookieCloudLoaded = false
	jdb.cookieCloudLastError = nil
	jdb.cookieCloudHTTPClient = nil
	jdb.cookieCloudMu.Unlock()
	return nil
}

func (jdb *JavDBActor) cookieCloudHTTPClientLocked() *http.Client {
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

func (jdb *JavDBActor) ensureCookieCloudCookies() error {
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
	jdb.cookieCloudLastAttempt = now
	jdb.cookieCloud.httpClient = jdb.cookieCloudHTTPClientLocked()
	cookies, err := jdb.cookieCloud.fetchCookies()
	if err != nil {
		jdb.cookieCloudLastError = err
		if jdb.cookieCloudLoaded {
			return nil
		}
		return err
	}
	if len(cookies) == 0 {
		err = fmt.Errorf("CookieCloud contains no JavDB cookies")
		jdb.cookieCloudLastError = err
		return err
	}
	for _, cookie := range cookies {
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if domain == "" {
			domain = "javdb.com"
		}
		cookieURL := "https://" + domain + "/"
		if err = jdb.SetCookies(cookieURL, []*http.Cookie{cookie}); err != nil {
			jdb.cookieCloudLastError = fmt.Errorf("apply CookieCloud cookies: %w", err)
			if jdb.cookieCloudLoaded {
				return nil
			}
			return jdb.cookieCloudLastError
		}
	}
	jdb.cookieCloudLoaded = true
	jdb.cookieCloudLastSuccess = now
	jdb.cookieCloudLastError = nil
	return nil
}

func (jdb *JavDBActor) NormalizeActorID(id string) string {
	return strings.TrimSpace(id)
}

func (jdb *JavDBActor) ParseActorIDFromURL(rawURL string) (string, error) {
	homepage, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	pathClean := strings.TrimRight(homepage.Path, "/")
	if !strings.HasPrefix(pathClean, "/actors/") {
		return "", provider.ErrInvalidURL
	}
	id := path.Base(pathClean)
	if id == "" || id == "/" || id == "actors" {
		return "", provider.ErrInvalidURL
	}
	return jdb.NormalizeActorID(id), nil
}

func (jdb *JavDBActor) GetActorInfoByID(id string) (*model.ActorInfo, error) {
	endpoint := jdb.pageEndpoint
	if endpoint == "" {
		endpoint = actorPageURL
	}
	return jdb.GetActorInfoByURL(fmt.Sprintf(endpoint, id))
}

func (jdb *JavDBActor) GetActorInfoByURL(rawURL string) (info *model.ActorInfo, err error) {
	if err = jdb.ensureCookieCloudCookies(); err != nil {
		return nil, err
	}
	id, err := jdb.ParseActorIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}

	info = &model.ActorInfo{
		ID:       id,
		Provider: jdb.Name(),
		Homepage: rawURL,
		Aliases:  []string{},
		Images:   []string{},
	}

	c := jdb.ClonedCollector()

	// Extract Actor Name
	c.OnXML(`//div[contains(@class,"actor-header")]//h2 | //div[contains(@class,"actor-section")]//h2 | //div[contains(@class,"article-header")]//h2 | //h2[contains(@class,"actor-name")] | //h2[contains(@class,"title")]`, func(e *colly.XMLElement) {
		if info.Name != "" {
			return
		}
		if domNode, ok := e.DOM.(*html.Node); ok {
			if name := cleanActorSearchName(extractActorName(domNode)); name != "" {
				info.Name = name
			}
		}
	})
	c.OnXML(`//span[contains(@class,"actor-section-name")] | //span[contains(@class,"section-name")]`, func(e *colly.XMLElement) {
		if info.Name == "" {
			if domNode, ok := e.DOM.(*html.Node); ok {
				info.Name = cleanActorSearchName(extractActorName(domNode))
			}
		}
	})

	// Extract Profile Image
	c.OnXML(`//div[contains(@class,"avatar")] | //span[contains(@class,"avatar")] | //div[contains(@class,"actor-avatar")] | //div[contains(@class,"column-avatar")] | //div[contains(@class,"actor-section")]`, func(e *colly.XMLElement) {
		if len(info.Images) > 0 {
			return
		}
		if avatar := extractActorImageSrc(e); avatar != "" {
			info.Images = append(info.Images, avatar)
		}
	})

	// JavDB currently exposes aliases as section metadata immediately after
	// the actor name. Ignore the movie-count metadata and keep this parser
	// tolerant of locale-specific separators.
	c.OnXML(`//span[contains(@class,"actor-section-name")]/following-sibling::span[contains(@class,"section-meta")]`, func(e *colly.XMLElement) {
		meta := strings.TrimSpace(e.Text)
		if meta == "" || strings.Contains(meta, "部影片") || strings.Contains(meta, "videos") {
			return
		}
		for _, alias := range splitActorAliases(meta) {
			if alias != "" && normalizeActorText(alias) != normalizeActorText(info.Name) {
				info.Aliases = appendActorAlias(info.Aliases, alias)
			}
		}
	})

	// Fallback Profile Image
	c.OnXML(`//meta[@property="og:image"]`, func(e *colly.XMLElement) {
		if len(info.Images) == 0 {
			if content := strings.TrimSpace(e.Attr("content")); content != "" {
				info.Images = append(info.Images, e.Request.AbsoluteURL(content))
			}
		}
	})

	// Extract Bio / Summary from dedicated container if present
	c.OnXML(`//div[contains(@class,"actor-intro")] | //div[contains(@class,"bio")] | //div[contains(@class,"actor-summary")] | //div[contains(@class,"description")]`, func(e *colly.XMLElement) {
		if info.Summary == "" {
			if text := strings.TrimSpace(e.Text); text != "" {
				info.Summary = text
			}
		}
	})

	// Extract Profile Details from panel blocks
	c.OnXML(`//nav[contains(@class,"panel-info") or contains(@class,"panel")]/div[contains(@class,"panel-block")] | //div[contains(@class,"actor-section")]//div[contains(@class,"panel-block")] | //div[contains(@class,"panel-block")]`, func(e *colly.XMLElement) {
		label := strings.TrimSpace(e.ChildText(`.//strong[1]`))
		value := strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]`))
		if value == "" {
			fullText := strings.TrimSpace(e.Text)
			if label != "" {
				value = strings.TrimSpace(strings.TrimPrefix(fullText, label))
			}
		}
		labelClean := strings.TrimSuffix(label, ":")
		labelClean = strings.TrimSuffix(labelClean, "：")
		labelClean = strings.TrimSpace(labelClean)

		switch labelClean {
		case "別名", "别名", "曾用名", "藝名", "艺名", "Aliases", "Other Names":
			for _, alias := range strings.FieldsFunc(value, func(r rune) bool {
				return r == ',' || r == '、' || r == '/' || r == '|' || r == ';'
			}) {
				if alias = strings.TrimSpace(alias); alias != "" {
					info.Aliases = append(info.Aliases, alias)
				}
			}
		case "生日", "生年月日", "Birthday", "DOB", "Date of Birth":
			info.Birthday = parser.ParseDate(value)
		case "身高", "身長", "Height":
			info.Height = parser.ParseInt(strings.TrimRight(value, "cm"))
		case "罩杯", "Cup", "Cup Size":
			if match := regexp.MustCompile(`[A-Za-z]`).FindString(value); match != "" {
				info.CupSize = strings.ToUpper(match)
			}
		case "三圍", "三围", "3サイズ", "Measurements":
			info.Measurements, info.CupSize = parseActorMeasurements(value, info.CupSize)
		case "簡介", "简介", "介绍", "紹介", "Bio", "Summary", "Profile":
			if info.Summary == "" {
				info.Summary = strings.TrimSpace(value)
			}
		case "出道", "出道日期", "デビュー", "Debut":
			info.DebutDate = parser.ParseDate(value)
		case "血型", "血液型", "Blood Type", "Blood":
			info.BloodType = strings.TrimSpace(strings.TrimRight(value, "型"))
		case "籍貫", "籍贯", "出身", "地区", "地區", "Nationality", "Origin":
			info.Nationality = strings.TrimSpace(value)
		case "愛好", "爱好", "趣味", "特長", "特长", "Hobby", "Hobbies":
			info.Hobby = strings.TrimSpace(value)
		}
	})

	err = c.Visit(info.Homepage)
	return
}

func (jdb *JavDBActor) SearchActor(keyword string) (results []*model.ActorSearchResult, err error) {
	if err = jdb.ensureCookieCloudCookies(); err != nil {
		return nil, err
	}

	c := jdb.ClonedCollector()
	results = make([]*model.ActorSearchResult, 0)

	c.OnXML(`//div[contains(@class,"actors")]//div[contains(@class,"box")] | //div[contains(@class,"actor-item")] | //div[contains(@class,"movie-list")]//div[contains(@class,"item")]/a[contains(@class,"box")] | //div[contains(@class,"grid")]//a[contains(@href,"/actors/")] | //a[contains(@href,"/actors/")]`, func(e *colly.XMLElement) {
		if domNode, ok := e.DOM.(*html.Node); ok && isMaleActorNode(domNode) {
			return
		}
		href := e.Attr("href")
		if href == "" {
			href = e.ChildAttr(`.//a[contains(@href,"/actors/")][1]`, "href")
		}
		if href == "" {
			return
		}
		homepage := e.Request.AbsoluteURL(href)
		id, parseErr := jdb.ParseActorIDFromURL(homepage)
		if parseErr != nil || id == "" {
			return
		}

		for _, r := range results {
			if r.ID == id {
				return
			}
		}

		avatar := extractImageSrc(e, `.//img[1]`)

		var name string
		if domNode, ok := e.DOM.(*html.Node); ok {
			name = cleanActorSearchName(extractActorName(domNode))
		}
		if name == "" {
			name = strings.TrimSpace(e.ChildText(`.//strong[contains(@class,"name")] | .//span[contains(@class,"name")] | .//strong[1]`))
		}
		name = cleanActorSearchName(name)
		if name == "" {
			return
		}

		res := &model.ActorSearchResult{
			ID:       id,
			Name:     name,
			Provider: jdb.Name(),
			Homepage: homepage,
			Images:   []string{},
			Aliases:  []string{},
		}
		if avatar != "" {
			res.Images = append(res.Images, avatar)
		}
		// Some JavDB layouts put aliases in the actor card title, e.g.
		// "Primary Name, Former Name".
		title := strings.TrimSpace(e.Attr("title"))
		if title == "" {
			title = strings.TrimSpace(e.ChildAttr(`.//a[1]`, "title"))
		}
		if title != "" {
			for _, alias := range splitActorAliases(title) {
				if alias != "" && normalizeActorText(alias) != normalizeActorText(res.Name) {
					res.Aliases = appendActorAlias(res.Aliases, alias)
				}
			}
		}

		if meta := strings.TrimSpace(e.ChildText(`.//span[contains(@class,"meta")] | .//div[contains(@class,"meta")] | .//small`)); meta != "" {
			meta = strings.TrimPrefix(meta, "别名:")
			meta = strings.TrimPrefix(meta, "別名:")
			meta = strings.TrimPrefix(meta, "Aliases:")
			for _, alias := range strings.FieldsFunc(meta, func(r rune) bool {
				return r == ',' || r == '、' || r == '/' || r == '|'
			}) {
				if alias = strings.TrimSpace(alias); alias != "" {
					res.Aliases = append(res.Aliases, alias)
				}
			}
		}

		results = append(results, res)
	})

	endpoint := jdb.searchEndpoint
	if endpoint == "" {
		endpoint = actorSearchURL
	}
	err = c.Visit(fmt.Sprintf(endpoint, url.QueryEscape(keyword)))
	return
}

var (
	measurementsBWHRe   = regexp.MustCompile(`(?i)B:?\s*(\d+)(?:[（(]([A-Za-z])[）)])?\s*[/,\s]\s*W:?\s*(\d+)\s*[/,\s]\s*H:?\s*(\d+)`)
	measurementsPlainRe = regexp.MustCompile(`^(\d{2,3})[\s\-/,\.]+([0-9]{2,3})[\s\-/,\.]+([0-9]{2,3})$`)
)

func parseActorMeasurements(s string, existingCup string) (measurements string, cup string) {
	cup = existingCup
	s = strings.TrimSpace(s)
	if s == "" {
		return "", cup
	}

	if match := measurementsBWHRe.FindStringSubmatch(s); len(match) == 5 {
		b := parser.ParseInt(match[1])
		if match[2] != "" && cup == "" {
			cup = strings.ToUpper(match[2])
		}
		w := parser.ParseInt(match[3])
		h := parser.ParseInt(match[4])
		if b > 0 && w > 0 && h > 0 {
			measurements = fmt.Sprintf("B:%d / W:%d / H:%d", b, w, h)
			return measurements, cup
		}
	}

	if match := measurementsPlainRe.FindStringSubmatch(s); len(match) == 4 {
		b := parser.ParseInt(match[1])
		w := parser.ParseInt(match[2])
		h := parser.ParseInt(match[3])
		if b > 0 && w > 0 && h > 0 {
			measurements = fmt.Sprintf("B:%d / W:%d / H:%d", b, w, h)
			return measurements, cup
		}
	}

	var b, w, h int
	for _, item := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '/' || r == ',' || r == ' ' || r == '、'
	}) {
		item = strings.TrimSpace(item)
		if strings.HasPrefix(strings.ToUpper(item), "B:") || strings.HasPrefix(strings.ToUpper(item), "B") {
			raw := strings.TrimPrefix(strings.TrimPrefix(item, "B:"), "B")
			raw = strings.TrimPrefix(raw, ":")
			if sub := regexp.MustCompile(`(\d+)(?:[（(]([A-Za-z])[）)])?`).FindStringSubmatch(raw); len(sub) >= 2 {
				b = parser.ParseInt(sub[1])
				if len(sub) == 3 && sub[2] != "" && cup == "" {
					cup = strings.ToUpper(sub[2])
				}
			}
		} else if strings.HasPrefix(strings.ToUpper(item), "W:") || strings.HasPrefix(strings.ToUpper(item), "W") {
			raw := strings.TrimPrefix(strings.TrimPrefix(item, "W:"), "W")
			raw = strings.TrimPrefix(raw, ":")
			w = parser.ParseInt(raw)
		} else if strings.HasPrefix(strings.ToUpper(item), "H:") || strings.HasPrefix(strings.ToUpper(item), "H") {
			raw := strings.TrimPrefix(strings.TrimPrefix(item, "H:"), "H")
			raw = strings.TrimPrefix(raw, ":")
			h = parser.ParseInt(raw)
		} else if strings.HasPrefix(strings.ToUpper(item), "CUP:") || strings.HasPrefix(strings.ToUpper(item), "CUP") {
			raw := strings.TrimPrefix(strings.TrimPrefix(item, "Cup:"), "Cup")
			raw = strings.TrimPrefix(raw, ":")
			if cupMatch := regexp.MustCompile(`[A-Za-z]`).FindString(raw); cupMatch != "" && cup == "" {
				cup = strings.ToUpper(cupMatch)
			}
		}
	}
	if b > 0 && w > 0 && h > 0 {
		measurements = fmt.Sprintf("B:%d / W:%d / H:%d", b, w, h)
	}
	return measurements, cup
}

func cleanActorSearchName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, " \t\n\r,♀♂")
	for {
		cleaned := actorMovieCountSuffixRE.ReplaceAllString(s, "")
		if cleaned == s {
			break
		}
		s = strings.TrimSpace(cleaned)
	}
	return strings.TrimSpace(s)
}

var actorImageURLRE = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]+)["']?\s*\)`)
var actorMovieCountSuffixRE = regexp.MustCompile(`(?i)\s*\d+\s*(?:部影片|部片|部作品|videos?)\s*$`)

func extractActorImageSrc(e *colly.XMLElement) string {
	if image := extractImageSrc(e, `.//img[1]`); image != "" {
		return image
	}
	styles := []string{e.Attr("style"), e.ChildAttr(`.//img[1]`, "style")}
	for _, style := range styles {
		if match := actorImageURLRE.FindStringSubmatch(style); len(match) == 2 {
			return e.Request.AbsoluteURL(strings.TrimSpace(match[1]))
		}
	}
	return ""
}

func splitActorAliases(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == '/' || r == '|' || r == ';' || r == '；'
	})
}

func normalizeActorText(value string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

func appendActorAlias(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if normalizeActorText(existing) == normalizeActorText(value) {
			return values
		}
	}
	return append(values, value)
}

func init() {
	provider.Register(ActorProviderName, NewActor)
}
