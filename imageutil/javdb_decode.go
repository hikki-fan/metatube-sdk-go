package imageutil

import (
	"bufio"
	"errors"
	"image"
	"io"
)

const (
	javDBJPEGSignatureSize = 3
	javDBEncodedHeaderSize = javDBJPEGSignatureSize + 1
)

var javDBJPEGSignature = []byte{0xff, 0xd8, 0xff}

// DecodeJavDB decodes JavDB images whose first byte is an XOR key and whose
// remaining bytes are a JPEG payload XORed with that key. Unencoded images use
// the normal decoder unchanged.
func DecodeJavDB(r io.Reader) (image.Image, string, error) {
	buffered := bufio.NewReader(r)
	header, err := buffered.Peek(javDBEncodedHeaderSize)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Decode(buffered)
		}
		return nil, "", err
	}

	if isJavDBJPEGHeader(header, 0) {
		return Decode(buffered)
	}

	key := header[0]
	if !isJavDBJPEGHeader(header[1:], key) {
		return Decode(buffered)
	}
	if _, err := buffered.Discard(1); err != nil {
		return nil, "", err
	}
	return Decode(&javDBXORReader{reader: buffered, key: key})
}

func isJavDBJPEGHeader(header []byte, key byte) bool {
	if len(header) < len(javDBJPEGSignature) {
		return false
	}
	for index, expected := range javDBJPEGSignature {
		if header[index]^key != expected {
			return false
		}
	}
	return true
}

type javDBXORReader struct {
	reader io.Reader
	key    byte
}

func (r *javDBXORReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	for index := 0; index < n; index++ {
		p[index] ^= r.key
	}
	return n, err
}
