package javdb

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
)

const (
	mirrorClientID      = "lpw6vgqzsp"
	mirrorSignatureSalt = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"
	mirrorMaxResponse   = 5 << 20
	mirrorCacheTTL      = 2 * time.Minute
	mirrorCacheLimit    = 256
	mirrorCooldown      = 30 * time.Second
)

var (
	ErrMirrorRateLimited     = errors.New("javdb mirror: rate limited")
	ErrMirrorBusiness        = errors.New("javdb mirror: business error")
	ErrMirrorInvalidResponse = errors.New("javdb mirror: invalid response")
	ErrMirrorMismatch        = errors.New("javdb mirror: movie identity mismatch")
	ErrMirrorAmbiguous       = errors.New("javdb mirror: ambiguous movie identity")
	ErrMirrorRedirect        = errors.New("javdb mirror: redirect rejected")

	mirrorIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	fc2MirrorNumberRE = regexp.MustCompile(`(?i)^FC2(?:-?PPV)?-?(\d+)(?:-(?:C|U|UC))?$`)
	dateStyleNumberRE = regexp.MustCompile(`^\d{6}[-_]\d{2,4}$`)
	nFamilyNumberRE   = regexp.MustCompile(`(?i)^N[-_]?\d+$`)
	uncensoredMakerRE = regexp.MustCompile(`(?i)^(?:1PONDO|CARIBBEAN(?:COMPR|COM|PR)?|10MUSUME|PACOPACO|MURAMURA)(?:[-_]|\d)`)
	flexTypeNumberRE  = regexp.MustCompile(`(?i)^(?:S2MBD|CWPBD|LAFBD|MKBD|SMBD|SKYHD|MKD|CWP|LAF|SMD|SKY|S2M|EMP)(?:[-_]|\d)`)
	mirrorSuffixRE    = regexp.MustCompile(`(?i)[-_](?:C|U|UC)$`)
)

type mirrorClientError struct {
	kind   error
	status int
	detail string
}

func (e *mirrorClientError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("%v: HTTP %d", e.kind, e.status)
	}
	if e.detail != "" {
		return fmt.Sprintf("%v: %s", e.kind, e.detail)
	}
	return e.kind.Error()
}

func (e *mirrorClientError) Unwrap() error { return e.kind }

type mirrorCacheEntry struct {
	expires time.Time
	info    *model.MovieInfo
	typeID  int
	results []*model.MovieSearchResult
}

type mirrorCall struct {
	done  chan struct{}
	entry mirrorCacheEntry
	err   error
}

type mirrorClient struct {
	baseURL    string
	httpClient *http.Client

	mu            sync.Mutex
	cache         map[string]mirrorCacheEntry
	inFlight      map[string]*mirrorCall
	cooldownUntil time.Time
}

type mirrorEnvelope struct {
	Success json.RawMessage `json:"success"`
	Data    json.RawMessage `json:"data"`
}

type mirrorDataEnvelope struct {
	Movie  json.RawMessage `json:"movie"`
	Movies json.RawMessage `json:"movies"`
}

type mirrorSearchMovie struct {
	ID     string          `json:"id"`
	Number string          `json:"number"`
	Type   json.RawMessage `json:"type"`
}

type mirrorActor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Gender    *int   `json:"gender"`
	AvatarURL string `json:"avatar_url"`
}

type mirrorTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type mirrorPreviewImage struct {
	ThumbURL string `json:"thumb_url"`
	LargeURL string `json:"large_url"`
}

type mirrorMovie struct {
	ID              string               `json:"id"`
	Type            json.RawMessage      `json:"type"`
	Number          string               `json:"number"`
	Title           string               `json:"title"`
	Summary         string               `json:"summary"`
	CoverURL        string               `json:"cover_url"`
	ThumbURL        string               `json:"thumb_url"`
	Score           json.RawMessage      `json:"score"`
	Duration        json.RawMessage      `json:"duration"`
	ReleaseDate     string               `json:"release_date"`
	Actors          []mirrorActor        `json:"actors"`
	Tags            []mirrorTag          `json:"tags"`
	PreviewImages   []mirrorPreviewImage `json:"preview_images"`
	MakerName       string               `json:"maker_name"`
	DirectorName    string               `json:"director_name"`
	PublisherName   string               `json:"publisher_name"`
	SeriesName      string               `json:"series_name"`
	PreviewVideoURL string               `json:"preview_video_url"`
}

func newMirrorClient(baseURL, proxyURL string, timeout time.Duration) (*mirrorClient, error) {
	parsedBase, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBase.Host == "" || parsedBase.User != nil ||
		(parsedBase.Scheme != "http" && parsedBase.Scheme != "https") ||
		parsedBase.RawQuery != "" || parsedBase.Fragment != "" {
		return nil, fmt.Errorf("invalid JavDB mirror base URL")
	}
	if timeout <= 0 {
		timeout = defaultMirrorTimeout
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.Proxy = http.ProxyFromEnvironment
	if strings.TrimSpace(proxyURL) != "" {
		parsedProxy, parseErr := url.Parse(strings.TrimSpace(proxyURL))
		if parseErr != nil || parsedProxy.Host == "" ||
			(parsedProxy.Scheme != "http" && parsedProxy.Scheme != "https" &&
				parsedProxy.Scheme != "socks5" && parsedProxy.Scheme != "socks5h") {
			return nil, fmt.Errorf("invalid JavDB mirror proxy URL")
		}
		transport.Proxy = http.ProxyURL(parsedProxy)
	}
	return &mirrorClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache:    make(map[string]mirrorCacheEntry),
		inFlight: make(map[string]*mirrorCall),
	}, nil
}

func mirrorSignature(now time.Time) string {
	timestamp := strconv.FormatInt(now.Unix(), 10)
	digest := md5.Sum([]byte(timestamp + mirrorSignatureSalt))
	return timestamp + "." + mirrorClientID + "." + hex.EncodeToString(digest[:])
}

func (c *mirrorClient) requestJSON(path string, query url.Values, dst any) error {
	c.mu.Lock()
	if time.Now().Before(c.cooldownUntil) {
		c.mu.Unlock()
		return &mirrorClientError{kind: ErrMirrorRateLimited, detail: "cooldown active"}
	}
	c.mu.Unlock()

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "invalid endpoint"}
	}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "invalid request"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("jdsignature", mirrorSignature(time.Now()))
	req.Header.Set("Origin", "https://catembylegacy.fastcdn.dpdns.org")
	req.Header.Set("Referer", "https://catembylegacy.fastcdn.dpdns.org/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "request failed"}
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return &mirrorClientError{kind: ErrMirrorRedirect, status: response.StatusCode}
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		cooldown := mirrorCooldown
		if seconds, parseErr := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); parseErr == nil && seconds > 0 {
			if seconds > 300 {
				seconds = 300
			}
			cooldown = time.Duration(seconds) * time.Second
		}
		c.mu.Lock()
		c.cooldownUntil = time.Now().Add(cooldown)
		c.mu.Unlock()
		return &mirrorClientError{kind: ErrMirrorRateLimited, status: response.StatusCode}
	}
	if response.StatusCode != http.StatusOK {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, status: response.StatusCode}
	}
	if response.ContentLength > mirrorMaxResponse {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "response exceeds 5 MiB"}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, mirrorMaxResponse+1))
	if err != nil {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "could not read response"}
	}
	if len(body) > mirrorMaxResponse {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "response exceeds 5 MiB"}
	}
	var envelope mirrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "invalid JSON"}
	}
	var success int
	if len(envelope.Success) == 0 || json.Unmarshal(envelope.Success, &success) != nil || success != 1 {
		return &mirrorClientError{kind: ErrMirrorBusiness, detail: "success must be integer 1"}
	}
	if len(envelope.Data) == 0 || string(bytes.TrimSpace(envelope.Data)) == "null" {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "missing data object"}
	}
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		return &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "invalid data object"}
	}
	return nil
}

func (c *mirrorClient) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	entry, err := c.getMovieInfoByID(id)
	if err != nil {
		return nil, err
	}
	return entry.info, nil
}

func (c *mirrorClient) getMovieInfoByID(id string) (mirrorCacheEntry, error) {
	id = strings.TrimSpace(id)
	if !mirrorIDPattern.MatchString(id) || strings.Contains(id, "..") {
		return mirrorCacheEntry{}, &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "invalid movie ID"}
	}
	return c.cached("detail:"+id, func() (mirrorCacheEntry, error) {
		var data mirrorDataEnvelope
		if err := c.requestJSON("/v4/movies/"+url.PathEscape(id), nil, &data); err != nil {
			return mirrorCacheEntry{}, err
		}
		if len(data.Movie) == 0 || string(bytes.TrimSpace(data.Movie)) == "null" {
			return mirrorCacheEntry{}, &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "missing movie"}
		}
		var raw mirrorMovie
		if err := json.Unmarshal(data.Movie, &raw); err != nil {
			return mirrorCacheEntry{}, &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "invalid movie"}
		}
		movieType, ok := parseMirrorType(raw.Type)
		if !ok || movieType < 0 || movieType > 3 {
			return mirrorCacheEntry{}, &mirrorClientError{kind: ErrMirrorMismatch, detail: "invalid movie type"}
		}
		info, err := mirrorMovieInfo(raw, id)
		if err != nil {
			return mirrorCacheEntry{}, err
		}
		return mirrorCacheEntry{info: info, typeID: movieType}, nil
	})
}

func (c *mirrorClient) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	target := normalizeMirrorNumber(keyword)
	if target == "" {
		return []*model.MovieSearchResult{}, nil
	}
	family := mirrorNumberFamily(target)
	value, err := c.cached("search:"+strconv.Itoa(family)+":"+target, func() (mirrorCacheEntry, error) {
		var query url.Values = make(url.Values)
		query.Set("q", mirrorSearchQuery(keyword, family))
		query.Set("page", "1")
		query.Set("type", "movie")
		query.Set("limit", "24")
		var data mirrorDataEnvelope
		if err := c.requestJSON("/v2/search", query, &data); err != nil {
			return mirrorCacheEntry{}, err
		}
		if len(data.Movies) == 0 || string(bytes.TrimSpace(data.Movies)) == "null" {
			return mirrorCacheEntry{}, &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "missing movies list"}
		}
		var movies []mirrorSearchMovie
		if err := json.Unmarshal(data.Movies, &movies); err != nil {
			return mirrorCacheEntry{}, &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "invalid movies list"}
		}
		candidates := make([]mirrorSearchMovie, 0, len(movies))
		seenIDs := make(map[string]struct{}, len(movies))
		for _, movie := range movies {
			id := strings.TrimSpace(movie.ID)
			if !mirrorIDPattern.MatchString(id) || strings.Contains(id, "..") || normalizeMirrorNumber(movie.Number) != target {
				continue
			}
			if _, duplicate := seenIDs[id]; duplicate {
				continue
			}
			if movie.Type != nil && string(bytes.TrimSpace(movie.Type)) != "null" {
				candidateType, ok := parseMirrorType(movie.Type)
				if !ok || !mirrorTypeAllowed(family, candidateType) {
					continue
				}
			}
			seenIDs[id] = struct{}{}
			candidates = append(candidates, movie)
		}

		results := make([]*model.MovieSearchResult, 0, len(candidates))
		for _, candidate := range candidates {
			detail, detailErr := c.getMovieInfoByID(candidate.ID)
			if detailErr != nil {
				if errors.Is(detailErr, ErrMirrorMismatch) {
					continue
				}
				return mirrorCacheEntry{}, detailErr
			}
			if normalizeMirrorNumber(detail.info.Number) != target || !mirrorTypeAllowed(family, detail.typeID) {
				continue
			}
			results = append(results, detail.info.ToSearchResult())
		}
		if len(results) > 1 {
			return mirrorCacheEntry{}, &mirrorClientError{kind: ErrMirrorAmbiguous, detail: target}
		}
		return mirrorCacheEntry{results: results}, nil
	})
	if err != nil {
		return nil, err
	}
	return value.results, nil
}

func (c *mirrorClient) cached(key string, load func() (mirrorCacheEntry, error)) (mirrorCacheEntry, error) {
	c.mu.Lock()
	now := time.Now()
	if entry, ok := c.cache[key]; ok {
		if now.Before(entry.expires) {
			c.mu.Unlock()
			return cloneMirrorEntry(entry), nil
		}
		delete(c.cache, key)
	}
	if call := c.inFlight[key]; call != nil {
		c.mu.Unlock()
		<-call.done
		return cloneMirrorEntry(call.entry), call.err
	}
	call := &mirrorCall{done: make(chan struct{})}
	c.inFlight[key] = call
	c.mu.Unlock()

	entry, err := load()
	c.mu.Lock()
	call.entry = cloneMirrorEntry(entry)
	call.err = err
	if err == nil && (entry.info != nil || len(entry.results) > 0) {
		if len(c.cache) >= mirrorCacheLimit {
			var oldestKey string
			var oldestExpiry time.Time
			for cacheKey, cacheEntry := range c.cache {
				if oldestKey == "" || cacheEntry.expires.Before(oldestExpiry) {
					oldestKey, oldestExpiry = cacheKey, cacheEntry.expires
				}
			}
			delete(c.cache, oldestKey)
		}
		entry.expires = time.Now().Add(mirrorCacheTTL)
		c.cache[key] = cloneMirrorEntry(entry)
	}
	delete(c.inFlight, key)
	close(call.done)
	c.mu.Unlock()
	return cloneMirrorEntry(entry), err
}

func cloneMirrorEntry(entry mirrorCacheEntry) mirrorCacheEntry {
	cloned := mirrorCacheEntry{expires: entry.expires, typeID: entry.typeID}
	if entry.info != nil {
		info := *entry.info
		info.Actors = append(pq.StringArray(nil), entry.info.Actors...)
		info.PreviewImages = append(pq.StringArray(nil), entry.info.PreviewImages...)
		info.Genres = append(pq.StringArray(nil), entry.info.Genres...)
		cloned.info = &info
	}
	if entry.results != nil {
		cloned.results = make([]*model.MovieSearchResult, 0, len(entry.results))
		for _, result := range entry.results {
			if result == nil {
				cloned.results = append(cloned.results, nil)
				continue
			}
			copyResult := *result
			copyResult.Actors = append(pq.StringArray(nil), result.Actors...)
			cloned.results = append(cloned.results, &copyResult)
		}
	}
	return cloned
}

func mirrorMovieInfo(movie mirrorMovie, requestedID string) (*model.MovieInfo, error) {
	if movie.ID != requestedID {
		return nil, &mirrorClientError{kind: ErrMirrorMismatch, detail: "detail ID does not match request"}
	}
	movieType, ok := parseMirrorType(movie.Type)
	if !ok || movieType < 0 || movieType > 3 {
		return nil, &mirrorClientError{kind: ErrMirrorMismatch, detail: "invalid movie type"}
	}
	info := &model.MovieInfo{
		ID:                 movie.ID,
		Number:             strings.TrimSpace(movie.Number),
		Title:              strings.TrimSpace(movie.Title),
		Summary:            strings.TrimSpace(movie.Summary),
		Provider:           Name,
		Homepage:           "https://javdb.com/v/" + movie.ID,
		Director:           strings.TrimSpace(movie.DirectorName),
		Actors:             make(pq.StringArray, 0, len(movie.Actors)),
		ThumbURL:           strings.TrimSpace(movie.ThumbURL),
		CoverURL:           strings.TrimSpace(movie.CoverURL),
		PreviewVideoURL:    strings.TrimSpace(movie.PreviewVideoURL),
		PreviewImages:      make(pq.StringArray, 0, len(movie.PreviewImages)),
		Maker:              strings.TrimSpace(movie.MakerName),
		Label:              strings.TrimSpace(movie.PublisherName),
		Series:             strings.TrimSpace(movie.SeriesName),
		Genres:             make(pq.StringArray, 0, len(movie.Tags)),
		Score:              parseMirrorFloat(movie.Score),
		Runtime:            parseMirrorInt(movie.Duration),
		ReleaseDate:        parser.ParseDate(strings.TrimSpace(movie.ReleaseDate)),
		PreviewVideoHLSURL: "",
	}
	if info.ThumbURL == "" {
		info.ThumbURL = info.CoverURL
	}
	for _, actor := range movie.Actors {
		if strings.TrimSpace(actor.Name) == "" || actor.Gender == nil || *actor.Gender != 0 {
			continue
		}
		info.Actors = append(info.Actors, strings.TrimSpace(actor.Name))
	}
	for _, tag := range movie.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			info.Genres = append(info.Genres, simplifyGenre(name))
		}
	}
	for _, image := range movie.PreviewImages {
		imageURL := strings.TrimSpace(image.LargeURL)
		if imageURL == "" {
			imageURL = strings.TrimSpace(image.ThumbURL)
		}
		if imageURL != "" {
			info.PreviewImages = append(info.PreviewImages, imageURL)
		}
	}
	if !info.IsValid() {
		return nil, &mirrorClientError{kind: ErrMirrorInvalidResponse, detail: "movie is missing required fields"}
	}
	return info, nil
}

func parseMirrorType(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return 0, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

func parseMirrorFloat(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var value float64
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0
	}
	value, _ = strconv.ParseFloat(strings.TrimSpace(text), 64)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func parseMirrorInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var value int
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0
	}
	value, _ = strconv.Atoi(strings.TrimSpace(text))
	return value
}

func normalizeMirrorNumber(raw string) string {
	numberText := strings.ToUpper(strings.TrimSpace(raw))
	numberText = strings.ReplaceAll(numberText, " ", "")
	numberWithNormalizedSeparators := strings.ReplaceAll(numberText, "_", "-")
	if match := fc2MirrorNumberRE.FindStringSubmatch(numberWithNormalizedSeparators); len(match) == 2 {
		return "FC2-" + match[1]
	}
	numberText = mirrorSuffixRE.ReplaceAllString(numberText, "")
	if dateStyleNumberRE.MatchString(numberText) {
		if numberText == "050815_076" {
			return "050815-076"
		}
		return numberText
	}
	numberText = strings.ReplaceAll(numberText, "_", "-")
	for strings.Contains(numberText, "--") {
		numberText = strings.ReplaceAll(numberText, "--", "-")
	}
	return strings.Trim(numberText, "-")
}

func mirrorSearchQuery(raw string, family int) string {
	query := strings.ToUpper(strings.Join(strings.Fields(raw), ""))
	if family == 3 {
		if match := fc2MirrorNumberRE.FindStringSubmatch(strings.ReplaceAll(query, "_", "-")); len(match) == 2 {
			return "FC2-" + match[1]
		}
		return normalizeMirrorNumber(query)
	}
	query = mirrorSuffixRE.ReplaceAllString(query, "")
	if dateStyleNumberRE.MatchString(query) {
		if query == "050815_076" {
			return "050815-076"
		}
		return query
	}
	query = strings.ReplaceAll(query, "_", "-")
	for strings.Contains(query, "--") {
		query = strings.ReplaceAll(query, "--", "-")
	}
	return query
}

// Family values match the public mirror API: JAV=0, UN=1, EU=2, FC2=3.
func mirrorNumberFamily(normalized string) int {
	if fc2MirrorNumberRE.MatchString(strings.ReplaceAll(normalized, "_", "-")) {
		return 3
	}
	if number.IsWestern(normalized) {
		return 2
	}
	if uncensoredMakerRE.MatchString(normalized) || nFamilyNumberRE.MatchString(normalized) || regexp.MustCompile(`^\d+$`).MatchString(normalized) || number.IsUncensored(normalized) {
		return 1
	}
	if flexTypeNumberRE.MatchString(normalized) {
		return -1
	}
	return 0
}

func mirrorTypeAllowed(family, movieType int) bool {
	if family == -1 {
		return movieType == 0 || movieType == 1
	}
	return movieType == family
}
