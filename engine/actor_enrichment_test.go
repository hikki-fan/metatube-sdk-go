package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/model"
)

func TestUniqueMatchingActor(t *testing.T) {
	primaryNames := []string{"河北彩花", "河北 彩花"}
	results := []*model.ActorSearchResult{
		{
			ID:       "EvkJ",
			Name:     "河北彩花",
			Provider: javDBActorProviderName,
			Aliases:  []string{"河北 彩花"},
		},
		{
			ID:       "male-1",
			Name:     "河北彩花",
			Provider: "JavBus",
		},
	}

	got := uniqueMatchingActor(results, primaryNames)
	require.NotNil(t, got)
	assert.Equal(t, "EvkJ", got.ID)
}

func TestUniqueMatchingActorRejectsAmbiguousResults(t *testing.T) {
	results := []*model.ActorSearchResult{
		{ID: "one", Name: "同名", Provider: javDBActorProviderName},
		{ID: "two", Name: "同名", Provider: javDBActorProviderName},
	}
	assert.Nil(t, uniqueMatchingActor(results, []string{"同名"}))
}

func TestMergeActorSearchResultPreservesPrimaryFields(t *testing.T) {
	info := &model.ActorInfo{
		ID:           "8301",
		Name:         "主名称",
		Provider:     avLeagueActorProviderName,
		Homepage:     "https://www.av-league.com/actress/8301.html",
		Aliases:      []string{"旧别名"},
		Images:       []string{"https://primary.example/avatar.jpg"},
		Measurements: "B:84 / W:58 / H:88",
	}
	result := &model.ActorSearchResult{
		ID:       "z0p",
		Name:     "补充名称",
		Provider: javDBActorProviderName,
		Aliases:  []string{"旧别名", "JavDB别名"},
		Images:   []string{"https://primary.example/avatar.jpg", "https://javdb.example/avatar.jpg"},
	}

	changed := mergeActorSearchResult(info, result)
	assert.True(t, changed)
	assert.Equal(t, "主名称", info.Name)
	assert.Equal(t, avLeagueActorProviderName, info.Provider)
	assert.Equal(t, "https://www.av-league.com/actress/8301.html", info.Homepage)
	assert.Equal(t, "B:84 / W:58 / H:88", info.Measurements)
	assert.Equal(t, []string{"旧别名", "补充名称", "JavDB别名"}, []string(info.Aliases))
	assert.Equal(t, []string{"https://primary.example/avatar.jpg", "https://javdb.example/avatar.jpg"}, []string(info.Images))
}
