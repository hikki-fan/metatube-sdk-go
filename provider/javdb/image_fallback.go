package javdb

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

const (
	javDBImageFallbackCacheTTL         = 5 * time.Minute
	javDBImageFallbackNegativeCacheTTL = 20 * time.Second
	javDBImageFallbackCacheLimit       = 256
	javDBImageFallbackURLLimit         = 8
)

type javDBImageFallbackCacheEntry struct {
	urls      []string
	err       error
	expiresAt time.Time
}

type javDBImageFallbackCall struct {
	done chan struct{}
	urls []string
	err  error
}

// GetMovieImageFallbackURLsByID reads only the image projection from JavDB's
// origin detail page. It intentionally bypasses the mirror and metadata DB.
func (jdb *JavDB) GetMovieImageFallbackURLsByID(id string) ([]string, error) {
	normalizedID := jdb.NormalizeMovieID(id)
	if normalizedID == "" || normalizedID != id || strings.ContainsAny(id, "/\\?#%\x00\r\n\t ") || id == "." || id == ".." {
		return nil, provider.ErrInvalidID
	}

	jdb.imageFallbackMu.Lock()
	now := time.Now()
	jdb.pruneImageFallbackCacheLocked(now)
	if entry, ok := jdb.imageFallbackCache[id]; ok {
		jdb.imageFallbackMu.Unlock()
		return cloneImageFallbackURLs(entry.urls), entry.err
	}
	if call, ok := jdb.imageFallbackCalls[id]; ok {
		jdb.imageFallbackMu.Unlock()
		<-call.done
		return cloneImageFallbackURLs(call.urls), call.err
	}
	call := &javDBImageFallbackCall{done: make(chan struct{})}
	if jdb.imageFallbackCalls == nil {
		jdb.imageFallbackCalls = make(map[string]*javDBImageFallbackCall)
	}
	jdb.imageFallbackCalls[id] = call
	jdb.imageFallbackMu.Unlock()

	call.urls, call.err = jdb.fetchMovieImageFallbackURLs(id)

	jdb.imageFallbackMu.Lock()
	ttl := javDBImageFallbackCacheTTL
	if call.err != nil || len(call.urls) == 0 {
		ttl = javDBImageFallbackNegativeCacheTTL
	}
	if len(jdb.imageFallbackCache) >= javDBImageFallbackCacheLimit {
		jdb.evictOldestImageFallbackCacheEntryLocked()
	}
	if jdb.imageFallbackCache == nil {
		jdb.imageFallbackCache = make(map[string]javDBImageFallbackCacheEntry)
	}
	jdb.imageFallbackCache[id] = javDBImageFallbackCacheEntry{
		urls:      cloneImageFallbackURLs(call.urls),
		err:       call.err,
		expiresAt: time.Now().Add(ttl),
	}
	delete(jdb.imageFallbackCalls, id)
	close(call.done)
	jdb.imageFallbackMu.Unlock()

	return cloneImageFallbackURLs(call.urls), call.err
}

func (jdb *JavDB) fetchMovieImageFallbackURLs(id string) ([]string, error) {
	endpoint := jdb.movieURL
	if endpoint == "" {
		endpoint = movieURL
	}
	rawURL := fmt.Sprintf(endpoint, url.PathEscape(id))
	parsedID, err := jdb.ParseMovieIDFromURL(rawURL)
	if err != nil || parsedID != id {
		return nil, provider.ErrInvalidID
	}

	var finalURL string
	info, err := jdb.getMovieInfoByURLWithRecoveryAndFinalURL(rawURL, &finalURL)
	if err != nil {
		return nil, err
	}
	finalID, finalIDErr := jdb.ParseMovieIDFromURL(finalURL)
	if info == nil || info.ID != id || finalIDErr != nil || finalID != id || !strings.EqualFold(info.Provider, jdb.Name()) {
		return nil, fmt.Errorf("JavDB origin image lookup returned a mismatched movie")
	}

	urls := make([]string, 0, 5)
	seen := make(map[string]struct{})
	for _, candidate := range []string{
		info.CoverURL,
		info.ThumbURL,
		info.BigCoverURL,
		info.BigThumbURL,
	} {
		appendJavDBImageFallbackURL(&urls, seen, candidate)
	}
	for _, candidate := range info.PreviewImages {
		appendJavDBImageFallbackURL(&urls, seen, candidate)
	}
	if len(urls) > javDBImageFallbackURLLimit {
		urls = urls[:javDBImageFallbackURLLimit]
	}
	if len(urls) == 0 {
		return nil, provider.ErrImageNotFound
	}
	return urls, nil
}

func appendJavDBImageFallbackURL(urls *[]string, seen map[string]struct{}, candidate string) {
	candidate = strings.TrimSpace(candidate)
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return
	}
	if _, ok := seen[candidate]; ok {
		return
	}
	seen[candidate] = struct{}{}
	*urls = append(*urls, candidate)
}

func (jdb *JavDB) pruneImageFallbackCacheLocked(now time.Time) {
	for id, entry := range jdb.imageFallbackCache {
		if !now.Before(entry.expiresAt) {
			delete(jdb.imageFallbackCache, id)
		}
	}
}

func (jdb *JavDB) evictOldestImageFallbackCacheEntryLocked() {
	var oldestID string
	var oldest time.Time
	for id, entry := range jdb.imageFallbackCache {
		if oldest.IsZero() || entry.expiresAt.Before(oldest) {
			oldestID = id
			oldest = entry.expiresAt
		}
	}
	if oldestID != "" {
		delete(jdb.imageFallbackCache, oldestID)
	}
}

func cloneImageFallbackURLs(urls []string) []string {
	return append([]string(nil), urls...)
}
