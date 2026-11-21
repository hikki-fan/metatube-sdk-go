package imageutil

import (
	"bufio"
	"errors"
	"image"
	"io"
)

const (
	javDBJPEGSignature     = "\xff\xd8\xff"
	javDBPNGSignature      = "\x89PNG\r\n\x1a\n"
	javDBEncodedHeaderSize = 1 + len(javDBPNGSignature)
)

// DecodeJavDB decodes JavDB images whose first byte is an XOR key and whose
// remaining bytes are a JPEG or PNG payload XORed with that key. Unencoded images use
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

	if isJavDBImageHeader(header, 0) {
		return Decode(buffered)
	}

	key := header[0]
	if !isJavDBImageHeader(header[1:], key) {
		return Decode(buffered)
	}
	if _, err := buffered.Discard(1); err != nil {
		return nil, "", err
	}
	return Decode(&javDBXORReader{reader: buffered, key: key})
}

func isJavDBImageHeader(header []byte, key byte) bool {
	return matchesXORHeader(header, key, javDBJPEGSignature) ||
		matchesXORHeader(header, key, javDBPNGSignature)
}

func matchesXORHeader(header []byte, key byte, signature string) bool {
	if len(header) < len(signature) {
		return false
	}
	for index := 0; index < len(signature); index++ {
		if header[index]^key != signature[index] {
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
