package imageutil

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeJavDBEncodedJPEG(t *testing.T) {
	plain := makeJavDBTestJPEG(t)
	tests := []struct {
		name  string
		key   byte
		plain []byte
	}{
		{name: "key_0x00", key: 0x00, plain: plain},
		{name: "key_0x38", key: 0x38, plain: plain},
		{name: "key_0xa5", key: 0xa5, plain: plain},
		{name: "key_0xff", key: 0xff, plain: plain},
		{name: "exif_header", key: 0x38, plain: withJavDBTestJPEGMarker(t, plain, 0xe1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded := encodeJavDBTestImage(test.plain, test.key)
			decoded, format, err := DecodeJavDB(bytes.NewReader(encoded))
			require.NoError(t, err)
			assert.Equal(t, "jpeg", format)
			assert.Equal(t, image.Rect(0, 0, 8, 8), decoded.Bounds())
		})
	}
}

func withJavDBTestJPEGMarker(t *testing.T, plain []byte, marker byte) []byte {
	t.Helper()
	require.GreaterOrEqual(t, len(plain), 4)
	require.Equal(t, []byte{0xff, 0xd8, 0xff}, plain[:3])
	withMarker := bytes.Clone(plain)
	withMarker[3] = marker
	return withMarker
}

func TestDecodeJavDBLeavesOtherImageFormatsUnchanged(t *testing.T) {
	plain := image.NewRGBA(image.Rect(0, 0, 3, 5))
	plain.Set(1, 2, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, plain))

	decoded, format, err := DecodeJavDB(bytes.NewReader(encoded.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, image.Rect(0, 0, 3, 5), decoded.Bounds())

	decoded, format, err = DecodeJavDB(bytes.NewReader(makeJavDBTestJPEG(t)))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
	assert.Equal(t, image.Rect(0, 0, 8, 8), decoded.Bounds())
}

func TestDecodeJavDBRejectsMalformedImages(t *testing.T) {
	const key = byte(0x38)
	tests := map[string][]byte{
		"wrong signature": {key, 0x00, 0x01, 0x02},
		"truncated":       {key, 0xff ^ key, 0xd8 ^ key, 0xff ^ key, 0xe0 ^ key},
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := DecodeJavDB(bytes.NewReader(data))
			require.Error(t, err)
		})
	}
}

func TestDecodeJavDBReturnsReaderErrors(t *testing.T) {
	expected := errors.New("reader failed")
	_, _, err := DecodeJavDB(errorReader{err: expected})
	require.ErrorIs(t, err, expected)
}

func TestDecodeJavDBUsesJPEGLIFallback(t *testing.T) {
	plain, err := base64.StdEncoding.DecodeString(base64EncodedImage)
	require.NoError(t, err)
	encoded := encodeJavDBTestImage(plain, 0x38)

	decoded, format, err := DecodeJavDB(bytes.NewReader(encoded))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
	assert.Equal(t, image.Rect(0, 0, 32, 18), decoded.Bounds())
}

func makeJavDBTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 24), G: uint8(y * 24), B: 60, A: 255})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, img, nil))
	return encoded.Bytes()
}

func encodeJavDBTestImage(plain []byte, key byte) []byte {
	encoded := make([]byte, len(plain)+1)
	encoded[0] = key
	for index, value := range plain {
		encoded[index+1] = value ^ key
	}
	return encoded
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

var _ io.Reader = errorReader{}
