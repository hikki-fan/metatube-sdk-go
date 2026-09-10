package javdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
	"github.com/metatube-community/metatube-sdk-go/model"
)

func TestJavDB_SearchMovieExactNumber(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}
	results, err := New().SearchMovie("BYD-115")
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.Equal(t, "BYD-115", results[0].Number)
	assert.Equal(t, "GnyQ", results[0].ID)
	assert.Equal(t, Name, results[0].Provider)
	assert.NotEmpty(t, results[0].Title)
	assert.NotEmpty(t, results[0].CoverURL)
}

func TestJavDB_GetMovieInfoByID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}
	info, err := New().GetMovieInfoByID("GnyQ")
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.True(t, info.IsValid())
	assert.Equal(t, "BYD-115", info.Number)
	assert.Equal(t, 110, info.Runtime)
	assert.NotEmpty(t, info.Actors)
	assert.NotEmpty(t, info.Genres)
}

func TestJavDB_ParseMovieIDFromURLPreservesCase(t *testing.T) {
	id, err := New().ParseMovieIDFromURL("https://javdb.com/v/GnyQ")
	require.NoError(t, err)
	assert.Equal(t, "GnyQ", id)
}

func TestJavDB_SimplifiesTraditionalChineseGenres(t *testing.T) {
	tests := map[string]string{
		"連褲襪":   "连裤袜",
		"其他戀物癖": "其他恋物癖",
		"戀腿癖":   "恋腿癖",
		"企畫":    "企划",
		"姐姐":    "姐姐",
	}

	for traditional, simplified := range tests {
		assert.Equal(t, simplified, simplifyGenre(traditional))
	}
}

func TestJavDB_GetMovieInfoByURL_CoverImage_NormalSrc(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>BYD-115 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>BYD-115</strong>
      <strong class="current-title">Test Title Normal Src</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <a href="/v/GnyQ/play">
      <img class="video-cover" src="https://c0.jdbstatic.com/covers/gn/GnyQ.jpg" alt="BYD-115">
    </a>
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">BYD-115</span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "https://c0.jdbstatic.com/covers/gn/GnyQ.jpg", info.CoverURL)
	assert.Equal(t, "https://c0.jdbstatic.com/covers/gn/GnyQ.jpg", info.ThumbURL)
	assert.NotEqual(t, fmt.Sprintf("%s/v/GnyQ/play", server.URL), info.CoverURL)
	assert.NotContains(t, info.CoverURL, "/play")
}

func TestJavDB_GetMovieInfoByURL_CoverImage_LazyDataSrc(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>BYD-115 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>BYD-115</strong>
      <strong class="current-title">Test Title Lazy Data-Src</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <a href="/v/GnyQ/play">
      <img class="video-cover" src="https://c0.jdbstatic.com/placeholder.jpg" data-src="https://c0.jdbstatic.com/covers/gn/GnyQ_real.jpg" alt="BYD-115">
    </a>
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">BYD-115</span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "https://c0.jdbstatic.com/covers/gn/GnyQ_real.jpg", info.CoverURL)
	assert.Equal(t, "https://c0.jdbstatic.com/covers/gn/GnyQ_real.jpg", info.ThumbURL)
	assert.NotEqual(t, "https://c0.jdbstatic.com/placeholder.jpg", info.CoverURL)
	assert.NotEqual(t, fmt.Sprintf("%s/v/GnyQ/play", server.URL), info.CoverURL)
	assert.NotContains(t, info.CoverURL, "/play")
}

func TestJavDB_GetMovieInfoByURL_CoverImage_LazyDataOriginal(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>BYD-115 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>BYD-115</strong>
      <strong class="current-title">Test Title Lazy Data-Original</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <a href="/v/GnyQ/play">
      <img class="video-cover" src="/placeholder.png" data-original="https://c0.jdbstatic.com/covers/gn/GnyQ_orig.jpg" alt="BYD-115">
    </a>
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">BYD-115</span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "https://c0.jdbstatic.com/covers/gn/GnyQ_orig.jpg", info.CoverURL)
	assert.Equal(t, "https://c0.jdbstatic.com/covers/gn/GnyQ_orig.jpg", info.ThumbURL)
	assert.NotEqual(t, fmt.Sprintf("%s/placeholder.png", server.URL), info.CoverURL)
	assert.NotEqual(t, fmt.Sprintf("%s/v/GnyQ/play", server.URL), info.CoverURL)
	assert.NotContains(t, info.CoverURL, "/play")
}

func TestJavDB_GetMovieInfoByURL_CoverImage_PlayAnchorHrefIgnored(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>BYD-115 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>BYD-115</strong>
      <strong class="current-title">Test Title Play Anchor Only</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <a href="/v/GnyQ/play">
      <span>Play Preview</span>
    </a>
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">BYD-115</span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Empty(t, info.CoverURL)
	assert.Empty(t, info.ThumbURL)
	assert.NotEqual(t, fmt.Sprintf("%s/v/GnyQ/play", server.URL), info.CoverURL)
	assert.NotContains(t, info.CoverURL, "/play")
}

func TestJavDB_GetMovieInfoByURL_CoverImage_RelativeSrc(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>BYD-115 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>BYD-115</strong>
      <strong class="current-title">Test Title Relative Src</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <a href="/v/GnyQ/play">
      <img class="video-cover" src="/covers/gn/GnyQ.jpg" alt="BYD-115">
    </a>
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">BYD-115</span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, fmt.Sprintf("%s/covers/gn/GnyQ.jpg", server.URL), info.CoverURL)
	assert.Equal(t, fmt.Sprintf("%s/covers/gn/GnyQ.jpg", server.URL), info.ThumbURL)
	assert.NotEqual(t, fmt.Sprintf("%s/v/GnyQ/play", server.URL), info.CoverURL)
	assert.NotContains(t, info.CoverURL, "/play")
}

func TestJavDB_GetMovieInfoByURL_ActorGender_FemaleRetained(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>TEST-101</strong>
      <strong class="current-title">Female Only</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <img class="video-cover" src="https://c0.jdbstatic.com/covers/gn/TEST101.jpg">
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>演員:</strong>
      <span class="value">
        <a href="/actors/v0w">三上悠亜</a><strong class="symbol female">♀</strong>
      </span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/TEST101", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, []string{"三上悠亜"}, []string(info.Actors))
}

func TestJavDB_GetMovieInfoByURL_ActorGender_MaleFiltered(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>TEST-102</strong>
      <strong class="current-title">Male Only</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <img class="video-cover" src="https://c0.jdbstatic.com/covers/gn/TEST102.jpg">
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>演員:</strong>
      <span class="value">
        <a href="/actors/xyz">森林原人</a><strong class="symbol male">♂</strong>
      </span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/TEST102", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Empty(t, info.Actors)
}

func TestJavDB_GetMovieInfoByURL_ActorGender_MixedPerformers(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>TEST-103</strong>
      <strong class="current-title">Mixed Cast</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <img class="video-cover" src="https://c0.jdbstatic.com/covers/gn/TEST103.jpg">
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>演員:</strong>
      <span class="value">
        <a href="/actors/v0w">三上悠亜</a><strong class="symbol female">♀</strong>,
        <a href="/actors/m01">森林原人</a><strong class="symbol male">♂</strong>,
        <a href="/actors/v02">吉沢明歩</a><strong class="symbol female">♀</strong>,
        <a href="/actors/m02">しみけん</a><strong class="symbol male">♂</strong>
      </span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/TEST103", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, []string{"三上悠亜", "吉沢明歩"}, []string(info.Actors))
}

func TestJavDB_GetMovieInfoByURL_ActorGender_UnmarkedFallback(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>TEST-104</strong>
      <strong class="current-title">Unmarked Cast Fallback</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <img class="video-cover" src="https://c0.jdbstatic.com/covers/gn/TEST104.jpg">
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>演員:</strong>
      <span class="value">
        <a href="/actors/u01">河北彩花</a>,
        <a href="/actors/m01">男優X</a><strong class="symbol male">♂</strong>,
        <a href="/actors/u02">小宵こなん</a>
      </span>
    </div>
  </nav>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/TEST104", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, []string{"河北彩花", "小宵こなん"}, []string(info.Actors))
}

func TestJavDB_GetMovieInfoByURL_PreservesMetadataAndFiltersActors(t *testing.T) {
	const html = `
<!DOCTYPE html>
<html>
<head><title>SSNI-100 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>SSNI-100</strong>
      <strong class="current-title">完整测试标题</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <a href="/v/SSNI100/play">
      <img class="video-cover" src="/covers/ss/SSNI100.jpg" data-src="https://c0.jdbstatic.com/covers/ss/SSNI100.jpg" alt="SSNI-100">
    </a>
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">SSNI-100</span>
    </div>
    <div class="panel-block">
      <strong>日期:</strong>
      <span class="value">2023-05-20</span>
    </div>
    <div class="panel-block">
      <strong>時長:</strong>
      <span class="value">120 分鐘</span>
    </div>
    <div class="panel-block">
      <strong>導演:</strong>
      <span class="value"><a href="/directors/d01">監督名</a></span>
    </div>
    <div class="panel-block">
      <strong>片商:</strong>
      <span class="value"><a href="/makers/m01">S1 NO.1 STYLE</a></span>
    </div>
    <div class="panel-block">
      <strong>發行:</strong>
      <span class="value"><a href="/publishers/p01">S1</a></span>
    </div>
    <div class="panel-block">
      <strong>系列:</strong>
      <span class="value"><a href="/series/s01">人気シリーズ</a></span>
    </div>
    <div class="panel-block">
      <strong>評分:</strong>
      <span class="value">4.5分, 由100人評價</span>
    </div>
    <div class="panel-block">
      <strong>類別:</strong>
      <span class="value">
        <a href="/tags/1">連褲襪</a>
        <a href="/tags/2">企畫</a>
        <a href="/tags/3">美少女</a>
      </span>
    </div>
    <div class="panel-block">
      <strong>演員:</strong>
      <span class="value">
        <a href="/actors/a01">三上悠亜</a><strong class="symbol female">♀</strong>,
        <a href="/actors/a02">森林原人</a><strong class="symbol male">♂</strong>
      </span>
    </div>
  </nav>
  <div class="preview-images">
    <a data-fancybox="gallery" href="https://c0.jdbstatic.com/previews/ss/SSNI100_1.jpg">
      <img src="https://c0.jdbstatic.com/previews/ss/SSNI100_1_thumb.jpg">
    </a>
    <a data-fancybox="gallery" href="https://c0.jdbstatic.com/previews/ss/SSNI100_2.jpg">
      <img src="https://c0.jdbstatic.com/previews/ss/SSNI100_2_thumb.jpg">
    </a>
  </div>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(fmt.Sprintf("%s/v/SSNI100", server.URL))
	require.NoError(t, err)
	require.NotNil(t, info)

	// Verify identifiers and title
	assert.Equal(t, "SSNI100", info.ID)
	assert.Equal(t, "SSNI-100", info.Number)
	assert.Equal(t, "完整测试标题", info.Title)
	assert.Equal(t, Name, info.Provider)

	// Verify date and runtime
	assert.Equal(t, time.Date(2023, 5, 20, 0, 0, 0, 0, time.UTC), time.Time(info.ReleaseDate))
	assert.Equal(t, 120, info.Runtime)

	// Verify maker, label, director, series, score
	assert.Equal(t, "監督名", info.Director)
	assert.Equal(t, "S1 NO.1 STYLE", info.Maker)
	assert.Equal(t, "S1", info.Label)
	assert.Equal(t, "人気シリーズ", info.Series)
	assert.Equal(t, 4.5, info.Score)

	// Verify genre conversion to Simplified Chinese
	assert.Equal(t, []string{"连裤袜", "企划", "美少女"}, []string(info.Genres))

	// Verify cover/thumb extraction (real image over play anchor and lazy data-src over src)
	assert.Equal(t, "https://c0.jdbstatic.com/covers/ss/SSNI100.jpg", info.CoverURL)
	assert.Equal(t, "https://c0.jdbstatic.com/covers/ss/SSNI100.jpg", info.ThumbURL)
	assert.NotContains(t, info.CoverURL, "/play")

	// Verify preview images
	assert.Equal(t, []string{
		"https://c0.jdbstatic.com/previews/ss/SSNI100_1.jpg",
		"https://c0.jdbstatic.com/previews/ss/SSNI100_2.jpg",
	}, []string(info.PreviewImages))

	// Verify actor gender filtering (only female kept)
	assert.Equal(t, []string{"三上悠亜"}, []string(info.Actors))
}

func TestJavDB_GetMovieInfoByURL_403RecoverySuccess(t *testing.T) {
	var cookieCloudCalls atomic.Int32
	cookieCloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCloudCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer cookieCloudServer.Close()

	const movieHTML = `
<!DOCTYPE html>
<html>
<head><title>BYD-115 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>BYD-115</strong>
      <strong class="current-title">Recovered Title</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <img class="video-cover" src="https://c0.jdbstatic.com/covers/gn/GnyQ.jpg">
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">BYD-115</span>
    </div>
  </nav>
</body>
</html>`

	var javdbCalls atomic.Int32
	javdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := javdbCalls.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Forbidden"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(movieHTML))
	}))
	defer javdbServer.Close()

	config := envconfig.NewConfig()
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")

	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()
	require.NoError(t, jdb.ensureCookieCloudCookies())
	cookieCloudCalls.Store(0)

	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", javdbServer.URL))
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "BYD-115", info.Number)
	assert.Equal(t, "Recovered Title", info.Title)

	assert.Equal(t, int32(2), javdbCalls.Load(), "expected initial 403 request + 1 retry")
	assert.Equal(t, int32(1), cookieCloudCalls.Load(), "expected 1 CookieCloud refresh")
}

func TestJavDB_GetMovieInfoByURL_403Persistent(t *testing.T) {
	var cookieCloudCalls atomic.Int32
	cookieCloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCloudCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer cookieCloudServer.Close()

	var javdbCalls atomic.Int32
	javdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		javdbCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Forbidden"))
	}))
	defer javdbServer.Close()

	config := envconfig.NewConfig()
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")

	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()
	require.NoError(t, jdb.ensureCookieCloudCookies())
	cookieCloudCalls.Store(0)

	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", javdbServer.URL))
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "Forbidden")

	assert.Equal(t, int32(2), javdbCalls.Load(), "expected exactly 1 retry (2 total requests, no loop)")
	assert.Equal(t, int32(1), cookieCloudCalls.Load(), "expected 1 CookieCloud refresh")
}

func TestJavDB_SearchMovie_403RecoverySuccess(t *testing.T) {
	var cookieCloudCalls atomic.Int32
	cookieCloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCloudCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer cookieCloudServer.Close()

	const searchHTML = `
<!DOCTYPE html>
<html>
<body>
  <div class="movie-list">
    <div class="item">
      <a class="box" href="/v/TEST123" title="Test Search Recovered">
        <div class="cover">
          <img src="https://c0.jdbstatic.com/covers/te/TEST123.jpg">
        </div>
        <div class="video-title">
          <strong>TEST-123</strong>
        </div>
        <div class="meta">2023-08-01</div>
      </a>
    </div>
  </div>
</body>
</html>`

	var javdbCalls atomic.Int32
	javdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := javdbCalls.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Forbidden"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(searchHTML))
	}))
	defer javdbServer.Close()

	config := envconfig.NewConfig()
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")

	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()
	jdb.searchURL = javdbServer.URL + "/search?f=all&q=%s"
	require.NoError(t, jdb.ensureCookieCloudCookies())
	cookieCloudCalls.Store(0)

	results, err := jdb.SearchMovie("TEST-123")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "TEST-123", results[0].Number)
	assert.Equal(t, "TEST123", results[0].ID)
	assert.Equal(t, "Test Search Recovered", results[0].Title)

	assert.Equal(t, int32(2), javdbCalls.Load(), "expected 1 initial 403 + 1 retry")
	assert.Equal(t, int32(1), cookieCloudCalls.Load(), "expected 1 CookieCloud refresh")
}

func TestJavDB_SearchMovie_403Persistent(t *testing.T) {
	var cookieCloudCalls atomic.Int32
	cookieCloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCloudCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer cookieCloudServer.Close()

	var javdbCalls atomic.Int32
	javdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		javdbCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Forbidden"))
	}))
	defer javdbServer.Close()

	config := envconfig.NewConfig()
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")

	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()
	jdb.searchURL = javdbServer.URL + "/search?f=all&q=%s"
	require.NoError(t, jdb.ensureCookieCloudCookies())
	cookieCloudCalls.Store(0)

	results, err := jdb.SearchMovie("TEST-123")
	require.Error(t, err)
	assert.Empty(t, results)
	assert.Contains(t, err.Error(), "Forbidden")

	assert.Equal(t, int32(2), javdbCalls.Load(), "expected exactly 1 retry (2 total requests, no loop)")
	assert.Equal(t, int32(1), cookieCloudCalls.Load(), "expected 1 CookieCloud refresh")
}

func TestJavDB_403_CookieCloudUnconfigured(t *testing.T) {
	var javdbCalls atomic.Int32
	javdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		javdbCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Forbidden"))
	}))
	defer javdbServer.Close()

	jdb := New()
	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", javdbServer.URL))
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "Forbidden")

	assert.Equal(t, int32(1), javdbCalls.Load(), "expected no retry when CookieCloud is unconfigured")
}

func TestJavDB_403_CookieCloudRefreshFails(t *testing.T) {
	var cookieCloudCalls atomic.Int32
	var cookieCloudFails atomic.Bool
	cookieCloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCloudCalls.Add(1)
		if cookieCloudFails.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer cookieCloudServer.Close()

	var javdbCalls atomic.Int32
	javdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		javdbCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Forbidden"))
	}))
	defer javdbServer.Close()

	config := envconfig.NewConfig()
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")

	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()
	require.NoError(t, jdb.ensureCookieCloudCookies())
	cookieCloudCalls.Store(0)
	cookieCloudFails.Store(true)

	info, err := jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ", javdbServer.URL))
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "Forbidden", "preserves original 403 error semantics")

	assert.Equal(t, int32(1), javdbCalls.Load(), "expected no retry when CookieCloud refresh fails")
	assert.Equal(t, int32(1), cookieCloudCalls.Load(), "expected 1 CookieCloud refresh attempt")
}

func TestJavDB_CookieCacheSynchronization_ConcurrencySafe(t *testing.T) {
	var cookieCloudCalls atomic.Int32
	cookieCloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCloudCalls.Add(1)
		time.Sleep(10 * time.Millisecond) // simulate slight latency
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cookieCloudResponse{Encrypted: cookieCloudLegacyFixture})
	}))
	defer cookieCloudServer.Close()

	const movieHTML = `
<!DOCTYPE html>
<html>
<head><title>BYD-115 - JavDB</title></head>
<body>
  <div class="video-detail">
    <h2 class="title">
      <strong>BYD-115</strong>
      <strong class="current-title">Concurrent Title</strong>
    </h2>
  </div>
  <div class="column-video-cover">
    <img class="video-cover" src="https://c0.jdbstatic.com/covers/gn/GnyQ.jpg">
  </div>
  <nav class="movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      <span class="value">BYD-115</span>
    </div>
  </nav>
</body>
</html>`

	var pathCounts sync.Map
	javdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val, _ := pathCounts.LoadOrStore(r.URL.Path, new(atomic.Int32))
		counter := val.(*atomic.Int32)
		if counter.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Forbidden"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(movieHTML))
	}))
	defer javdbServer.Close()

	config := envconfig.NewConfig()
	config.Set("cookiecloud_host", cookieCloudServer.URL)
	config.Set("cookiecloud_uuid", "test-uuid")
	config.Set("cookiecloud_password", "test-password")

	jdb := New()
	require.NoError(t, jdb.SetConfig(config))
	jdb.cookieCloudHTTPClient = cookieCloudServer.Client()

	const concurrency = 5
	var wg sync.WaitGroup
	errs := make([]error, concurrency)
	infos := make([]*model.MovieInfo, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			infos[idx], errs[idx] = jdb.GetMovieInfoByURL(fmt.Sprintf("%s/v/GnyQ%d", javdbServer.URL, idx))
		}(i)
	}

	wg.Wait()

	for i := 0; i < concurrency; i++ {
		require.NoError(t, errs[i], "concurrent request %d failed", i)
		require.NotNil(t, infos[i], "concurrent request %d returned nil info", i)
		assert.Equal(t, "BYD-115", infos[i].Number)
	}
}
