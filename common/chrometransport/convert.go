package chrometransport

import (
	"net/http"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

// Chrome document/navigation header order. Values must be lowercase; tls-client
// ignores Header-Order keys that use mixed case.
var chromeHeaderOrder = []string{
	"cache-control",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"upgrade-insecure-requests",
	"user-agent",
	"accept",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-user",
	"sec-fetch-dest",
	"referer",
	"accept-language",
	"cookie",
}

var chromePseudoHeaderOrder = []string{
	":method",
	":authority",
	":scheme",
	":path",
}

func toFHTTPRequest(req *http.Request) (*fhttp.Request, error) {
	fReq, err := fhttp.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), req.Body)
	if err != nil {
		return nil, err
	}
	fReq.Host = req.Host
	fReq.ContentLength = req.ContentLength
	fReq.Close = req.Close
	fReq.Header = fhttp.Header{}
	for key, values := range req.Header {
		if isReservedHeader(key) {
			continue
		}
		fReq.Header[key] = append([]string(nil), values...)
	}
	applyChromeHeaderOrder(fReq)
	return fReq, nil
}

func fromFHTTPResponse(resp *fhttp.Response, orig *http.Request) *http.Response {
	if resp == nil {
		return nil
	}
	header := make(http.Header, len(resp.Header))
	for key, values := range resp.Header {
		if isReservedHeader(key) {
			continue
		}
		header[key] = append([]string(nil), values...)
	}
	return &http.Response{
		Status:           resp.Status,
		StatusCode:       resp.StatusCode,
		Proto:            resp.Proto,
		ProtoMajor:       resp.ProtoMajor,
		ProtoMinor:       resp.ProtoMinor,
		Header:           header,
		Body:             resp.Body,
		ContentLength:    resp.ContentLength,
		TransferEncoding: append([]string(nil), resp.TransferEncoding...),
		Uncompressed:     resp.Uncompressed,
		Trailer:          cloneHTTPHeader(resp.Trailer),
		Request:          orig,
	}
}

func applyChromeHeaderOrder(req *fhttp.Request) {
	if req.Header == nil {
		req.Header = fhttp.Header{}
	}
	present := make(map[string]struct{}, len(req.Header))
	for key := range req.Header {
		present[strings.ToLower(key)] = struct{}{}
	}
	order := make([]string, 0, len(chromeHeaderOrder))
	for _, key := range chromeHeaderOrder {
		if _, ok := present[key]; ok {
			order = append(order, key)
		}
	}
	for key := range present {
		if key == strings.ToLower(fhttp.HeaderOrderKey) || key == strings.ToLower(fhttp.PHeaderOrderKey) {
			continue
		}
		if containsString(chromeHeaderOrder, key) {
			continue
		}
		order = append(order, key)
	}
	req.Header[fhttp.HeaderOrderKey] = order
	req.Header[fhttp.PHeaderOrderKey] = append([]string(nil), chromePseudoHeaderOrder...)
}

func isReservedHeader(key string) bool {
	switch strings.ToLower(key) {
	case strings.ToLower(fhttp.HeaderOrderKey), strings.ToLower(fhttp.PHeaderOrderKey):
		return true
	default:
		return false
	}
}

func cloneHTTPHeader(src fhttp.Header) http.Header {
	if src == nil {
		return nil
	}
	dst := make(http.Header, len(src))
	for key, values := range src {
		if isReservedHeader(key) {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
