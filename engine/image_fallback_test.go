package engine

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"
	"gorm.io/gorm/logger"

	"github.com/metatube-community/metatube-sdk-go/collection/maps"
	"github.com/metatube-community/metatube-sdk-go/database"
	"github.com/metatube-community/metatube-sdk-go/engine/providerid"
	"github.com/metatube-community/metatube-sdk-go/model"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

const imageFallbackProviderName = "ImageFallbackTest"

type imageFallbackMovieProvider struct {
	fallbackURLs []string
	fallbackErr  error
	responses    map[string][]byte
	fetches      []string
	fetchMu      sync.Mutex
	fallbacks    atomic.Int32
}

func (p *imageFallbackMovieProvider) Name() string           { return imageFallbackProviderName }
func (p *imageFallbackMovieProvider) Priority() float64      { return 1 }
func (p *imageFallbackMovieProvider) SetPriority(float64)    {}
func (p *imageFallbackMovieProvider) Language() language.Tag { return language.English }
func (p *imageFallbackMovieProvider) URL() *url.URL {
	u, _ := url.Parse("https://movies.example/")
	return u
}
func (p *imageFallbackMovieProvider) NormalizeMovieID(id string) string { return strings.TrimSpace(id) }
func (p *imageFallbackMovieProvider) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.HasPrefix(u.Path, "/v/") {
		return "", mt.ErrInvalidURL
	}
	return path.Base(u.Path), nil
}
func (*imageFallbackMovieProvider) GetMovieInfoByID(string) (*model.MovieInfo, error) {
	return nil, fmt.Errorf("unexpected metadata lookup")
}
func (*imageFallbackMovieProvider) GetMovieInfoByURL(string) (*model.MovieInfo, error) {
	return nil, fmt.Errorf("unexpected metadata lookup")
}
func (p *imageFallbackMovieProvider) GetMovieImageFallbackURLsByID(string) ([]string, error) {
	p.fallbacks.Add(1)
	return append([]string(nil), p.fallbackURLs...), p.fallbackErr
}
func (p *imageFallbackMovieProvider) Fetch(rawURL string) (*http.Response, error) {
	p.fetchMu.Lock()
	p.fetches = append(p.fetches, rawURL)
	p.fetchMu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(p.responses[rawURL])),
		Header:     make(http.Header),
	}, nil
}

type plainImageMovieProvider struct{ base *imageFallbackMovieProvider }

func (p *plainImageMovieProvider) Name() string           { return p.base.Name() }
func (p *plainImageMovieProvider) Priority() float64      { return p.base.Priority() }
func (p *plainImageMovieProvider) SetPriority(v float64)  { p.base.SetPriority(v) }
func (p *plainImageMovieProvider) Language() language.Tag { return p.base.Language() }
func (p *plainImageMovieProvider) URL() *url.URL          { return p.base.URL() }
func (p *plainImageMovieProvider) NormalizeMovieID(id string) string {
	return p.base.NormalizeMovieID(id)
}
func (p *plainImageMovieProvider) ParseMovieIDFromURL(rawURL string) (string, error) {
	return p.base.ParseMovieIDFromURL(rawURL)
}
func (p *plainImageMovieProvider) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	return p.base.GetMovieInfoByID(id)
}
func (p *plainImageMovieProvider) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	return p.base.GetMovieInfoByURL(rawURL)
}
func (p *plainImageMovieProvider) Fetch(rawURL string) (*http.Response, error) {
	return p.base.Fetch(rawURL)
}

func newImageFallbackEngine(t *testing.T, p mt.MovieProvider, movie *model.MovieInfo) *Engine {
	t.Helper()
	db, err := database.Open(&database.Config{
		DSN:                  ":memory:",
		DisableAutomaticPing: true,
		LogLevel:             logger.Silent,
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.MovieInfo{}))
	if movie != nil {
		require.NoError(t, db.Create(movie).Error)
	}

	providers := maps.NewCaseInsensitiveMap[mt.MovieProvider]()
	providers.Set(p.Name(), p)
	return &Engine{db: db, movieProviders: providers}
}

func makeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func TestMovieImageFallbackRunsForPrimaryThumbBackdropAndExplicitURL(t *testing.T) {
	const (
		coverURL    = "https://mirror.example/cover.jpg"
		thumbURL    = "https://mirror.example/thumb.jpg"
		backdropURL = "https://mirror.example/backdrop.jpg"
		originURL   = "https://javdb.example/real.jpg"
	)
	provider := &imageFallbackMovieProvider{
		fallbackURLs: []string{originURL},
		responses: map[string][]byte{
			coverURL:                              []byte("HTTP 200 but not an image"),
			thumbURL:                              []byte("HTTP 200 but not an image"),
			backdropURL:                           []byte("HTTP 200 but not an image"),
			"https://mirror.example/explicit.jpg": []byte("HTTP 200 but not an image"),
			originURL:                             makeJPEG(t, 60, 60),
		},
	}
	info := &model.MovieInfo{
		ID: "OpaqueID", Provider: imageFallbackProviderName, Number: "TEST-123", Title: "Title",
		Homepage: "https://movies.example/v/OpaqueID", CoverURL: coverURL,
		BigThumbURL: thumbURL, BigCoverURL: backdropURL,
	}
	app := newImageFallbackEngine(t, provider, info)
	pid := providerid.ProviderID{Provider: imageFallbackProviderName, ID: "OpaqueID"}

	primary, err := app.GetMoviePrimaryImage(pid, -1, -1)
	require.NoError(t, err)
	assert.Equal(t, image.Rect(20, 0, 60, 60), primary.Bounds())
	thumb, err := app.GetMovieThumbImage(pid)
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 14, 60, 47), thumb.Bounds())
	backdrop, err := app.GetMovieBackdropImage(pid)
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 60, 60), backdrop.Bounds())
	explicit, err := app.GetMovieImageByURL(pid, "https://mirror.example/explicit.jpg", 2.0/3.0, 1, false)
	require.NoError(t, err)
	assert.Equal(t, image.Rect(20, 0, 60, 60), explicit.Bounds())
	assert.EqualValues(t, 4, provider.fallbacks.Load())
}

func TestMovieImageFallbackDoesNotRunWhenMirrorImageDecodes(t *testing.T) {
	const mirrorURL = "https://mirror.example/valid.jpg"
	provider := &imageFallbackMovieProvider{
		fallbackURLs: []string{"https://javdb.example/unused.jpg"},
		responses:    map[string][]byte{mirrorURL: makeJPEG(t, 48, 72)},
	}
	app := newImageFallbackEngine(t, provider, nil)
	img, err := app.GetMovieImageByURL(
		providerid.ProviderID{Provider: imageFallbackProviderName, ID: "OpaqueID"},
		mirrorURL, 0, 0.5, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, int(provider.fallbacks.Load()))
	assert.Equal(t, image.Rect(0, 0, 48, 72), img.Bounds())
}

func TestMovieImageFallbackPreservesNativeErrorAndDoesNotRetryUnsupportedProvider(t *testing.T) {
	const mirrorURL = "https://mirror.example/invalid.jpg"
	base := &imageFallbackMovieProvider{
		fallbackURLs: []string{"https://javdb.example/unused.jpg"},
		fallbackErr:  fmt.Errorf("origin unavailable"),
		responses:    map[string][]byte{mirrorURL: []byte("not an image")},
	}
	app := newImageFallbackEngine(t, base, nil)
	_, err := app.GetMovieImageByURL(
		providerid.ProviderID{Provider: imageFallbackProviderName, ID: "OpaqueID"},
		mirrorURL, 0, 0.5, false,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image: unknown format")
	assert.Contains(t, err.Error(), "origin unavailable")

	unsupported := &plainImageMovieProvider{base: &imageFallbackMovieProvider{
		responses: map[string][]byte{mirrorURL: []byte("not an image")},
	}}
	plainApp := newImageFallbackEngine(t, unsupported, nil)
	_, err = plainApp.GetMovieImageByURL(
		providerid.ProviderID{Provider: imageFallbackProviderName, ID: "OpaqueID"},
		mirrorURL, 0, 0.5, false,
	)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "fallback failed")
	assert.Equal(t, 0, int(unsupported.base.fallbacks.Load()))
}

func TestMovieImageFallbackRequiresMatchingProviderIdentity(t *testing.T) {
	const mirrorURL = "https://mirror.example/invalid.jpg"
	provider := &imageFallbackMovieProvider{
		fallbackURLs: []string{"https://javdb.example/unused.jpg"},
		responses:    map[string][]byte{mirrorURL: []byte("not an image")},
	}
	app := newImageFallbackEngine(t, provider, nil)
	_, err := app.getMovieImageWithFallback(
		provider,
		providerid.ProviderID{Provider: "DifferentProvider", ID: "OpaqueID"},
		mirrorURL, 0, 0.5, false,
	)
	require.Error(t, err)
	assert.Equal(t, 0, int(provider.fallbacks.Load()))
}
