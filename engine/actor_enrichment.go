package engine

import (
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/metatube-community/metatube-sdk-go/model"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

// AV-LEAGUE remains the source of truth for an actor record. JavDBActor is
// used only to fill the small set of fields which AV-LEAGUE does not always
// have. Keep these names as strings instead of importing either provider so
// the enrichment does not change build-tag based provider registration.
const (
	avLeagueActorProviderName = "AV-LEAGUE"
	javDBActorProviderName    = "JavDBActor"
	actorEnrichmentRetryAfter = 6 * time.Hour
)

// enrichActorInfo adds JavDB's actor name, aliases and images to an
// AV-LEAGUE record. It deliberately fails closed: a provider error, an
// ambiguous match, or an unavailable JavDB provider leaves the primary
// metadata untouched.
func (e *Engine) enrichActorInfo(info *model.ActorInfo, force bool) bool {
	if info == nil || !strings.EqualFold(info.Provider, avLeagueActorProviderName) {
		return false
	}

	provider, err := e.GetActorProviderByName(javDBActorProviderName)
	if err != nil {
		return false
	}
	searcher, ok := provider.(mt.ActorSearcher)
	if !ok {
		return false
	}

	if force {
		e.recordActorEnrichmentAttempt(info)
	} else if !e.shouldAttemptActorEnrichment(info) {
		return false
	}

	// Search the canonical AV-LEAGUE name first, then its known aliases. A
	// bounded list prevents a malformed page from causing many JavDB requests.
	keywords := make([]string, 0, 1+len(info.Aliases))
	keywords = append(keywords, info.Name)
	keywords = append(keywords, info.Aliases...)
	seenKeywords := make(map[string]struct{}, len(keywords))
	for i, keyword := range keywords {
		if i >= 5 {
			break
		}
		keyword = strings.TrimSpace(keyword)
		key := normalizeActorName(keyword)
		if key == "" {
			continue
		}
		if _, seen := seenKeywords[key]; seen {
			continue
		}
		seenKeywords[key] = struct{}{}

		results, searchErr := searcher.SearchActor(keyword)
		if searchErr != nil {
			continue
		}
		candidate := uniqueMatchingActor(results, keywords)
		if candidate == nil {
			continue
		}
		return mergeActorSearchResult(info, candidate)
	}
	return false
}

func (e *Engine) shouldAttemptActorEnrichment(info *model.ActorInfo) bool {
	key := strings.ToLower(strings.TrimSpace(info.Provider)) + ":" +
		strings.ToLower(strings.TrimSpace(info.ID))
	now := time.Now()
	e.actorEnrichmentMu.Lock()
	defer e.actorEnrichmentMu.Unlock()
	if e.actorEnrichmentAttempts == nil {
		e.actorEnrichmentAttempts = make(map[string]time.Time)
	}
	if last, ok := e.actorEnrichmentAttempts[key]; ok && now.Sub(last) < actorEnrichmentRetryAfter {
		return false
	}
	e.actorEnrichmentAttempts[key] = now
	return true
}

func (e *Engine) recordActorEnrichmentAttempt(info *model.ActorInfo) {
	key := strings.ToLower(strings.TrimSpace(info.Provider)) + ":" +
		strings.ToLower(strings.TrimSpace(info.ID))
	e.actorEnrichmentMu.Lock()
	defer e.actorEnrichmentMu.Unlock()
	if e.actorEnrichmentAttempts == nil {
		e.actorEnrichmentAttempts = make(map[string]time.Time)
	}
	e.actorEnrichmentAttempts[key] = time.Now()
}

// normalizeActorName is intentionally conservative. It handles full-width
// forms, case and harmless punctuation/whitespace differences, while still
// requiring the same actual name or alias for a merge.
func normalizeActorName(value string) string {
	value = norm.NFKC.String(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}

func actorSearchResultNames(result *model.ActorSearchResult) []string {
	if result == nil {
		return nil
	}
	names := make([]string, 0, 1+len(result.Aliases))
	if result.Name != "" {
		names = append(names, result.Name)
	}
	names = append(names, result.Aliases...)
	return names
}

// uniqueMatchingActor returns a result only when exactly one provider ID
// matches one of the primary actor's names or aliases. This prevents common
// names from silently attaching the wrong JavDB actor.
func uniqueMatchingActor(results []*model.ActorSearchResult, names []string) *model.ActorSearchResult {
	requested := make(map[string]struct{}, len(names))
	for _, name := range names {
		if key := normalizeActorName(name); key != "" {
			requested[key] = struct{}{}
		}
	}
	if len(requested) == 0 {
		return nil
	}

	matches := make([]*model.ActorSearchResult, 0, 1)
	seenIDs := make(map[string]struct{})
	for _, result := range results {
		if result == nil || result.Provider != javDBActorProviderName || result.ID == "" {
			continue
		}
		if _, seen := seenIDs[result.ID]; seen {
			continue
		}
		matched := false
		for _, name := range actorSearchResultNames(result) {
			if _, ok := requested[normalizeActorName(name)]; ok {
				matched = true
				break
			}
		}
		if matched {
			seenIDs[result.ID] = struct{}{}
			matches = append(matches, result)
		}
	}
	if len(matches) != 1 {
		return nil
	}
	return matches[0]
}

func mergeActorSearchResult(info *model.ActorInfo, result *model.ActorSearchResult) bool {
	if info == nil || result == nil {
		return false
	}
	changed := false
	if strings.TrimSpace(info.Name) == "" {
		name := strings.TrimSpace(result.Name)
		if name != "" {
			info.Name = name
			changed = true
		}
	} else if normalizeActorName(info.Name) != normalizeActorName(result.Name) {
		updated := appendActorValue(info.Aliases, result.Name)
		changed = len(updated) != len(info.Aliases)
		info.Aliases = updated
	}
	for _, alias := range result.Aliases {
		updated := appendActorValue(info.Aliases, alias)
		if len(updated) != len(info.Aliases) {
			changed = true
		}
		info.Aliases = updated
	}
	for _, image := range result.Images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		found := false
		for _, existing := range info.Images {
			if strings.EqualFold(strings.TrimSpace(existing), image) {
				found = true
				break
			}
		}
		if !found {
			info.Images = append(info.Images, image)
			changed = true
		}
	}
	return changed
}

func appendActorValue(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	key := normalizeActorName(value)
	for _, existing := range values {
		if normalizeActorName(existing) == key {
			return values
		}
	}
	return append(values, value)
}
