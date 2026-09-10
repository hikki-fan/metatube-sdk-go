package chrometransport

import (
	"net/http"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/stretchr/testify/require"
)

func TestToFHTTPRequestAppliesChromeHeaderOrder(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://javdb.com/search?q=probe", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", ChromeUserAgent)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Sec-CH-UA", ChromeSecCHUA)
	req.Header.Set("Cookie", "locale=zh-CN")
	req.Header.Set("X-Custom", "1")

	fReq, err := toFHTTPRequest(req)
	require.NoError(t, err)
	require.Equal(t, ChromeUserAgent, fReq.Header.Get("User-Agent"))
	require.Equal(t, chromePseudoHeaderOrder, []string(fReq.Header[fhttp.PHeaderOrderKey]))

	order := []string(fReq.Header[fhttp.HeaderOrderKey])
	require.Equal(t, "sec-ch-ua", order[0])
	require.Contains(t, order, "user-agent")
	require.Contains(t, order, "cookie")
	require.Contains(t, order, "x-custom")
	for _, key := range order {
		require.Equal(t, strings.ToLower(key), key)
	}
}
