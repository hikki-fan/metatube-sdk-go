package javdb

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	defaultCookieCloudRefreshInterval = 10 * time.Minute
	maxCookieCloudResponseSize        = 32 << 20
	cookieCloudSaltHeader             = "Salted__"
)

type cookieCloudClient struct {
	host            string
	uuid            string
	password        string
	refreshInterval time.Duration
	httpClient      *http.Client
}

type cookieCloudResponse struct {
	Encrypted  string `json:"encrypted"`
	CryptoType string `json:"crypto_type,omitempty"`
}

type cookieCloudPayload struct {
	CookieData map[string][]cookieCloudCookie `json:"cookie_data"`
}

// cookieCloudCookie mirrors the fields emitted by the CookieCloud browser
// extension. Unknown fields are intentionally ignored so extension updates do
// not break metadata requests.
type cookieCloudCookie struct {
	Name           string  `json:"name"`
	Value          string  `json:"value"`
	Domain         string  `json:"domain"`
	Path           string  `json:"path"`
	Secure         bool    `json:"secure"`
	HTTPOnly       bool    `json:"httpOnly"`
	SameSite       string  `json:"sameSite"`
	ExpirationDate float64 `json:"expirationDate"`
}

func (c *cookieCloudClient) fetchCookies() ([]*http.Cookie, error) {
	if c == nil {
		return nil, errors.New("CookieCloud is not configured")
	}
	host := strings.TrimRight(c.host, "/")
	uuid := url.PathEscape(strings.TrimSpace(c.uuid))
	request, err := http.NewRequest(http.MethodGet, host+"/get/"+uuid, nil)
	if err != nil {
		return nil, fmt.Errorf("create CookieCloud request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	client := c.httpClient
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request CookieCloud: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CookieCloud returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCookieCloudResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read CookieCloud response: %w", err)
	}
	var envelope cookieCloudResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode CookieCloud response: %w", err)
	}
	if strings.TrimSpace(envelope.Encrypted) == "" {
		return nil, errors.New("CookieCloud response does not contain encrypted data")
	}
	plain, err := decryptCookieCloud(envelope.Encrypted, c.uuid, c.password, envelope.CryptoType)
	if err != nil {
		return nil, fmt.Errorf("decrypt CookieCloud data: %w", err)
	}

	var payload cookieCloudPayload
	if err := json.Unmarshal(plain, &payload); err != nil || payload.CookieData == nil {
		// CookieCloud versions before local-storage support stored the domain map
		// directly. Keep accepting that format for existing installations.
		var legacy map[string][]cookieCloudCookie
		if legacyErr := json.Unmarshal(plain, &legacy); legacyErr != nil {
			if err != nil {
				return nil, fmt.Errorf("decode CookieCloud payload: %w", err)
			}
			return nil, errors.New("CookieCloud payload does not contain cookie data")
		}
		payload.CookieData = legacy
	}
	return cookieCloudCookies(payload.CookieData), nil
}

func cookieCloudCookies(byDomain map[string][]cookieCloudCookie) []*http.Cookie {
	var result []*http.Cookie
	now := time.Now()
	domains := make([]string, 0, len(byDomain))
	for domain := range byDomain {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, domain := range domains {
		entries := byDomain[domain]
		if !cookieCloudDomainAllowed(domain) {
			continue
		}
		for _, entry := range entries {
			if strings.TrimSpace(entry.Name) == "" {
				continue
			}
			cookieDomain := strings.TrimSpace(entry.Domain)
			if cookieDomain == "" {
				cookieDomain = domain
			}
			if !cookieCloudDomainAllowed(cookieDomain) {
				continue
			}
			cookie := &http.Cookie{
				Name:     entry.Name,
				Value:    entry.Value,
				Domain:   cookieDomain,
				Path:     entry.Path,
				Secure:   entry.Secure,
				HttpOnly: entry.HTTPOnly,
			}
			if cookie.Path == "" {
				cookie.Path = "/"
			}
			if entry.ExpirationDate > 0 {
				seconds := entry.ExpirationDate
				if seconds > 1e12 {
					seconds /= 1000
				}
				cookie.Expires = time.Unix(int64(seconds), 0)
				if cookie.Expires.Before(now) {
					continue
				}
			}
			switch strings.ToLower(strings.TrimSpace(entry.SameSite)) {
			case "strict":
				cookie.SameSite = http.SameSiteStrictMode
			case "lax":
				cookie.SameSite = http.SameSiteLaxMode
			case "none", "no_restriction":
				cookie.SameSite = http.SameSiteNoneMode
			}
			result = append(result, cookie)
		}
	}
	return result
}

func cookieCloudDomainAllowed(domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	return javDBMirrorHostPattern.MatchString(domain) || strings.HasSuffix(domain, ".javdb.com") ||
		domain == "jdbstatic.com" || strings.HasSuffix(domain, ".jdbstatic.com")
}

// decryptCookieCloud implements CookieCloud's legacy CryptoJS AES format and
// its newer aes-128-cbc-fixed format. The legacy format is the default used by
// existing CookieCloud servers and wraps the ciphertext in OpenSSL's
// "Salted__" envelope.
func decryptCookieCloud(encrypted, uuid, password, cryptoType string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	keyPassword := md5Hex(uuid + "-" + password)[:16]
	typeName := strings.ToLower(strings.TrimSpace(cryptoType))
	if typeName == "aes-128-cbc-fixed" {
		plain, err := decryptCookieCloudFixed(raw, keyPassword)
		if err != nil {
			return nil, err
		}
		if !json.Valid(plain) {
			return nil, errors.New("fixed CookieCloud plaintext is not JSON")
		}
		return plain, nil
	}
	if typeName != "" && typeName != "aes-256-cbc" && typeName != "crypto-js" {
		return nil, fmt.Errorf("unsupported CookieCloud crypto type %q", cryptoType)
	}

	legacyPlain, legacyErr := decryptCookieCloudLegacy(raw, keyPassword)
	if legacyErr == nil && json.Valid(legacyPlain) {
		return legacyPlain, nil
	}
	if legacyErr == nil {
		legacyErr = errors.New("legacy CookieCloud plaintext is not JSON")
	}
	// Some newer/older CookieCloud servers omit crypto_type. Try the fixed
	// format as a compatibility fallback, but only accept plaintext that is
	// valid JSON so a coincidental PKCS#7 padding match cannot be misdetected.
	fixedPlain, fixedErr := decryptCookieCloudFixed(raw, keyPassword)
	if fixedErr == nil && json.Valid(fixedPlain) {
		return fixedPlain, nil
	}
	if typeName != "" {
		return nil, fmt.Errorf("decrypt legacy CookieCloud data: %w", legacyErr)
	}
	if fixedErr != nil {
		return nil, fmt.Errorf("decrypt CookieCloud data (legacy: %v; fixed: %w)", legacyErr, fixedErr)
	}
	return nil, fmt.Errorf("decrypt CookieCloud data (legacy: %v; fixed plaintext is not JSON)", legacyErr)
}

func decryptCookieCloudLegacy(raw []byte, keyPassword string) ([]byte, error) {
	if len(raw) < len(cookieCloudSaltHeader)+8+aes.BlockSize || string(raw[:8]) != cookieCloudSaltHeader {
		return nil, errors.New("invalid legacy CookieCloud ciphertext")
	}
	salt := raw[8:16]
	ciphertext := raw[16:]
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("legacy AES ciphertext is not block aligned")
	}
	key, iv := evpBytesToKey([]byte(keyPassword), salt, 32, aes.BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	return decryptCBC(block, ciphertext, iv)
}

func decryptCookieCloudFixed(raw []byte, keyPassword string) ([]byte, error) {
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, errors.New("fixed AES ciphertext is not block aligned")
	}
	block, err := aes.NewCipher([]byte(keyPassword))
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	return decryptCBC(block, raw, make([]byte, aes.BlockSize))
}

func decryptCBC(block cipher.Block, ciphertext, iv []byte) ([]byte, error) {
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	if len(plain) == 0 {
		return nil, errors.New("decrypted CookieCloud payload is empty")
	}
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > block.BlockSize() || padding > len(plain) {
		return nil, errors.New("invalid CookieCloud padding")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid CookieCloud padding")
		}
	}
	return plain[:len(plain)-padding], nil
}

func evpBytesToKey(password, salt []byte, keyLength, ivLength int) ([]byte, []byte) {
	var derived []byte
	var previous []byte
	for len(derived) < keyLength+ivLength {
		hash := md5.New()
		_, _ = hash.Write(previous)
		_, _ = hash.Write(password)
		_, _ = hash.Write(salt)
		previous = hash.Sum(nil)
		derived = append(derived, previous...)
	}
	return derived[:keyLength], derived[keyLength : keyLength+ivLength]
}

func md5Hex(value string) string {
	digest := md5.Sum([]byte(value))
	return hex.EncodeToString(digest[:])
}
