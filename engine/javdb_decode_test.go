package engine

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"
)

const javDBImageTestHost = "https://tp.spfcas.com/covers/encoded.jpg"

type javDBImageTestProvider struct {
	name string
	body []byte
}

func (p *javDBImageTestProvider) Name() string         { return p.name }
func (*javDBImageTestProvider) Priority() float64      { return 1 }
func (*javDBImageTestProvider) SetPriority(float64)    {}
func (*javDBImageTestProvider) Language() language.Tag { return language.English }
func (*javDBImageTestProvider) URL() *url.URL          { return &url.URL{Scheme: "https", Host: "example.com"} }
func (p *javDBImageTestProvider) Fetch(string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(p.body)),
		Header:     make(http.Header),
	}, nil
}

func TestGetImageByURLDecodesScopedJavDBImages(t *testing.T) {
	plain := makeJavDBEngineTestJPEG(t)
	encoded := encodeJavDBEngineTestImage(plain, 0x38)

	for _, providerName := range []string{"JavDB", "JavDBActor"} {
		t.Run(providerName, func(t *testing.T) {
			app := &Engine{}
			provider := &javDBImageTestProvider{name: providerName, body: encoded}
			img, err := app.getImageByURL(provider, javDBImageTestHost)
			require.NoError(t, err)
			assert.Equal(t, image.Rect(0, 0, 8, 8), img.Bounds())
		})
	}
}

func TestGetImageByURLDecodesPlainJavDBImage(t *testing.T) {
	app := &Engine{}
	provider := &javDBImageTestProvider{name: "JavDB", body: makeJavDBEngineTestJPEG(t)}
	img, err := app.getImageByURL(provider, javDBImageTestHost)
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 8, 8), img.Bounds())
}

func TestGetImageByURLKeepsJavDBDecoderScoped(t *testing.T) {
	plain := makeJavDBEngineTestJPEG(t)
	encoded := encodeJavDBEngineTestImage(plain, 0x38)
	tests := map[string]struct {
		providerName string
		url          string
	}{
		"other provider": {providerName: "Other", url: javDBImageTestHost},
		"other host":     {providerName: "JavDB", url: "https://images.example/encoded.jpg"},
		"lookalike host": {providerName: "JavDB", url: "https://tp.spfcas.com.example/encoded.jpg"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			app := &Engine{}
			provider := &javDBImageTestProvider{name: test.providerName, body: encoded}
			_, err := app.getImageByURL(provider, test.url)
			require.Error(t, err)
		})
	}
}

func TestShouldDecodeJavDBImageUsesExactHostname(t *testing.T) {
	provider := &javDBImageTestProvider{name: "JavDB"}
	assert.True(t, shouldDecodeJavDBImage(provider, "https://TP.SPFCAS.COM:8443/image.jpg?token=x"))
	assert.False(t, shouldDecodeJavDBImage(provider, "https://tp.spfcas.com.evil/image.jpg"))
	assert.False(t, shouldDecodeJavDBImage(provider, "not a URL"))
}

func makeJavDBEngineTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(2, 3, color.White)
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, img, nil))
	return encoded.Bytes()
}

func encodeJavDBEngineTestImage(plain []byte, key byte) []byte {
	encoded := make([]byte, len(plain)+1)
	encoded[0] = key
	for index, value := range plain {
		encoded[index+1] = value ^ key
	}
	return encoded
}
