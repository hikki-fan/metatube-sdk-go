package javdb

import "github.com/metatube-community/metatube-sdk-go/common/chrometransport"

const (
	// Keep one stable browser identity for the whole provider lifetime. A
	// random UA is a poor match for a CookieCloud browser session and can make
	// anti-bot systems invalidate an otherwise valid session. The string must
	// stay on the same Chrome major version as chrometransport's ClientHello.
	defaultJavDBUserAgent = chrometransport.ChromeUserAgent
	javDBAcceptLanguage   = "zh-CN,zh;q=0.9"
	javDBDocumentAccept   = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	javDBImageAccept      = "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"
	javDBSecCHUA          = chrometransport.ChromeSecCHUA
)

func javDBDocumentHeaders(baseURL string) map[string]string {
	return map[string]string{
		"Accept":                    javDBDocumentAccept,
		"Accept-Language":           javDBAcceptLanguage,
		"Cache-Control":             "max-age=0",
		"Sec-CH-UA":                 javDBSecCHUA,
		"Sec-CH-UA-Mobile":          "?0",
		"Sec-CH-UA-Platform":        `"Windows"`,
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "same-origin",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
		"Referer":                   baseURL,
	}
}

func javDBImageHeaders(baseURL string) map[string]string {
	return map[string]string{
		"Accept":          javDBImageAccept,
		"Accept-Language": javDBAcceptLanguage,
		"Referer":         baseURL,
		"Sec-Fetch-Dest":  "image",
		"Sec-Fetch-Mode":  "no-cors",
		"Sec-Fetch-Site":  "same-site",
	}
}
