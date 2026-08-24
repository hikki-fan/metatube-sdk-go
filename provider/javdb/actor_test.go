package javdb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/provider/internal/testkit"
)

func TestJavDBActor_ParseActorIDFromURL(t *testing.T) {
	actor := NewActor()

	id, err := actor.ParseActorIDFromURL("https://javdb.com/actors/z0p")
	require.NoError(t, err)
	assert.Equal(t, "z0p", id)

	id, err = actor.ParseActorIDFromURL("https://javdb.com/actors/z0p/")
	require.NoError(t, err)
	assert.Equal(t, "z0p", id)

	id, err = actor.ParseActorIDFromURL("https://javdb.com/actors/z0p?locale=zh-CN")
	require.NoError(t, err)
	assert.Equal(t, "z0p", id)

	_, err = actor.ParseActorIDFromURL("https://javdb.com/v/SSNI100")
	require.Error(t, err)

	_, err = actor.ParseActorIDFromURL("https://javdb.com/actors/")
	require.Error(t, err)
}

func TestJavDBActor_GetActorInfoByURL_CompleteProfile(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>三上悠亜 - JavDB</title></head>
<body>
  <div class="actor-header">
    <h2 class="title">
      三上悠亜
      <strong class="symbol female">♀</strong>
    </h2>
  </div>
  <div class="actor-section">
    <div class="avatar">
      <img class="avatar" src="https://c0.jdbstatic.com/placeholder.jpg" data-src="https://c0.jdbstatic.com/avatars/z0/z0p_real.jpg" alt="三上悠亜">
    </div>
    <div class="actor-summary">
      前SKE48成员，后转型为知名AV女优。
    </div>
    <nav class="actor-panel-info">
      <div class="panel-block">
        <strong>別名:</strong>
        <span class="value">鬼頭桃菜、鬼头桃菜, Mikami Yua</span>
      </div>
      <div class="panel-block">
        <strong>生日:</strong>
        <span class="value">1993-08-16</span>
      </div>
      <div class="panel-block">
        <strong>身長:</strong>
        <span class="value">159cm</span>
      </div>
      <div class="panel-block">
        <strong>罩杯:</strong>
        <span class="value">F</span>
      </div>
      <div class="panel-block">
        <strong>三圍:</strong>
        <span class="value">B:84(F) / W:58 / H:88</span>
      </div>
      <div class="panel-block">
        <strong>出道日期:</strong>
        <span class="value">2015-06-01</span>
      </div>
      <div class="panel-block">
        <strong>血液型:</strong>
        <span class="value">A型</span>
      </div>
      <div class="panel-block">
        <strong>出身:</strong>
        <span class="value">日本</span>
      </div>
      <div class="panel-block">
        <strong>趣味:</strong>
        <span class="value">舞蹈、美妆</span>
      </div>
    </nav>
  </div>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	actor := NewActor()
	info, err := actor.GetActorInfoByURL(fmt.Sprintf("%s/actors/z0p", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.True(t, info.IsValid())
	assert.Equal(t, "z0p", info.ID)
	assert.Equal(t, "三上悠亜", info.Name)
	assert.Equal(t, ActorProviderName, info.Provider)
	assert.Equal(t, fmt.Sprintf("%s/actors/z0p", server.URL), info.Homepage)
	assert.Equal(t, []string{"https://c0.jdbstatic.com/avatars/z0/z0p_real.jpg"}, []string(info.Images))
	assert.Equal(t, []string{"鬼頭桃菜", "鬼头桃菜", "Mikami Yua"}, []string(info.Aliases))
	assert.Equal(t, time.Date(1993, 8, 16, 0, 0, 0, 0, time.UTC), time.Time(info.Birthday))
	assert.Equal(t, 159, info.Height)
	assert.Equal(t, "F", info.CupSize)
	assert.Equal(t, "B:84 / W:58 / H:88", info.Measurements)
	assert.Equal(t, "前SKE48成员，后转型为知名AV女优。", info.Summary)
	assert.Equal(t, time.Date(2015, 6, 1, 0, 0, 0, 0, time.UTC), time.Time(info.DebutDate))
	assert.Equal(t, "A", info.BloodType)
	assert.Equal(t, "日本", info.Nationality)
	assert.Equal(t, "舞蹈、美妆", info.Hobby)
}

func TestJavDBActor_SearchActor_Mock(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>搜索演员 - JavDB</title></head>
<body>
  <div class="actors">
    <div class="box">
      <a href="/actors/z0p">
        <div class="avatar">
          <img src="https://c0.jdbstatic.com/placeholder.jpg" data-src="https://c0.jdbstatic.com/avatars/z0/z0p.jpg" alt="三上悠亜">
        </div>
        <strong class="name">三上悠亜<strong class="symbol female">♀</strong></strong>
        <span class="meta">别名: 鬼頭桃菜</span>
      </a>
    </div>
    <div class="box">
      <a href="/actors/k01">
        <div class="avatar">
          <img src="https://c0.jdbstatic.com/avatars/k0/k01.jpg" alt="河北彩花">
        </div>
        <strong class="name">河北彩花</strong>
      </a>
    </div>
  </div>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	actor := NewActor()
	// Override search collector visit by testing direct scraping or URL
	c := actor.ClonedCollector()
	var results []*testSearchResult
	c.OnXML(`//div[contains(@class,"actors")]//div[contains(@class,"box")]`, func(e *colly.XMLElement) {
		href := e.ChildAttr(`.//a[contains(@href,"/actors/")][1]`, "href")
		id, err := actor.ParseActorIDFromURL(e.Request.AbsoluteURL(href))
		if err != nil {
			return
		}
		avatar := extractImageSrc(e, `.//img[1]`)
		name := cleanActorSearchName(e.ChildText(`.//strong[contains(@class,"name")]`))
		results = append(results, &testSearchResult{ID: id, Name: name, Avatar: avatar})
	})

	err := c.Visit(server.URL)
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "z0p", results[0].ID)
	assert.Equal(t, "三上悠亜", results[0].Name)
	assert.Equal(t, "https://c0.jdbstatic.com/avatars/z0/z0p.jpg", results[0].Avatar)

	assert.Equal(t, "k01", results[1].ID)
	assert.Equal(t, "河北彩花", results[1].Name)
	assert.Equal(t, "https://c0.jdbstatic.com/avatars/k0/k01.jpg", results[1].Avatar)
}

func TestJavDBActor_GetActorInfoByURL_ActualLayout(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html><body>
  <div class="actor-section">
    <span class="avatar" style="background-image: url('https://c0.jdbstatic.com/avatars/ev/EvkJ.jpg')"></span>
    <span class="actor-section-name">河北彩花 218 部影片</span>
    <span class="section-meta">河北樱花</span>
    <span class="section-meta">218 部影片</span>
  </div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	actor := NewActor()
	info, err := actor.GetActorInfoByURL(fmt.Sprintf("%s/actors/EvkJ", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "EvkJ", info.ID)
	assert.Equal(t, "河北彩花", info.Name)
	assert.Equal(t, []string{"河北樱花"}, []string(info.Aliases))
	assert.Equal(t, []string{"https://c0.jdbstatic.com/avatars/ev/EvkJ.jpg"}, []string(info.Images))
}

func TestJavDBActor_SearchActor_ActualLayout(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html><body>
  <div id="actors" class="actors">
    <div class="box actor-box">
      <a href="/actors/EvkJ" title="河北彩花, 河北樱花">
        <figure class="image"><img class="avatar" src="https://c0.jdbstatic.com/avatars/ev/EvkJ.jpg"></figure>
        <strong>河北彩花</strong>
      </a>
    </div>
    <div class="box actor-box">
      <a href="/actors/male1" title="男性演员">
        <figure class="image"><img class="avatar" src="https://c0.jdbstatic.com/avatars/ma/male1.jpg"></figure>
        <strong>男性演员<strong class="symbol male">♂</strong></strong>
      </a>
    </div>
  </div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	actor := NewActor()
	actor.searchEndpoint = server.URL + "/search?q=%s"
	results, err := actor.SearchActor("河北彩花")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "EvkJ", results[0].ID)
	assert.Equal(t, "河北彩花", results[0].Name)
	assert.Equal(t, []string{"河北樱花"}, []string(results[0].Aliases))
	assert.Equal(t, []string{"https://c0.jdbstatic.com/avatars/ev/EvkJ.jpg"}, []string(results[0].Images))
}

type testSearchResult struct {
	ID     string
	Name   string
	Avatar string
}

func TestJavDBActor_GetActorInfoByID(t *testing.T) {
	if os.Getenv("MT_RUN_LIVE_TESTS") != "1" {
		t.Skip("set MT_RUN_LIVE_TESTS=1 to run live JavDB tests")
	}
	testkit.Test(t, NewActor, []string{
		"EvkJ",
		"R2Vg",
	})
}

func TestJavDBActor_SearchActor(t *testing.T) {
	if os.Getenv("MT_RUN_LIVE_TESTS") != "1" {
		t.Skip("set MT_RUN_LIVE_TESTS=1 to run live JavDB tests")
	}
	testkit.Test(t, NewActor, []string{
		"三上悠亜",
		"河北彩花",
	})
}
