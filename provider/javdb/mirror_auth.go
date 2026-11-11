package javdb

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

type mirrorSession struct {
	token      string
	generation uint64
}

type mirrorLoginCall struct {
	done    chan struct{}
	session mirrorSession
	err     error
}

func parseMirrorCredentials(config provider.Config) (string, string, bool, error) {
	var values [2]string
	var present [2]bool
	for i, keys := range [][2]string{{"mirror_username", "javdb_mirror_username"}, {"mirror_password", "javdb_mirror_password"}} {
		for _, key := range keys {
			if config.Has(key) {
				value, err := config.GetString(key)
				if err != nil {
					return "", "", false, &mirrorClientError{kind: ErrMirrorAuth, detail: "invalid credential setting"}
				}
				values[i], present[i] = value, true
				break
			}
		}
	}
	if !present[0] && !present[1] {
		return "", "", false, nil
	}
	username, password := strings.TrimSpace(values[0]), values[1]
	if !present[0] || !present[1] || (username == "") != (password == "") {
		return "", "", false, &mirrorClientError{kind: ErrMirrorAuth, detail: "configure both mirror username and password"}
	}
	return username, password, true, nil
}

func newMirrorClientWithCredentials(baseURL, proxyURL string, timeout time.Duration, username, password string) (*mirrorClient, error) {
	if (strings.TrimSpace(username) == "") != (password == "") {
		return nil, &mirrorClientError{kind: ErrMirrorAuth, detail: "configure both mirror username and password"}
	}
	c, err := newMirrorClient(baseURL, proxyURL, timeout)
	if err != nil {
		return nil, err
	}
	c.username, c.password = strings.TrimSpace(username), password
	return c, nil
}

func (c *mirrorClient) ensureSession() (mirrorSession, error) {
	c.mu.Lock()
	if time.Now().Before(c.cooldownUntil) {
		c.mu.Unlock()
		return mirrorSession{}, &mirrorClientError{kind: ErrMirrorRateLimited, detail: "cooldown active"}
	}
	if c.session.token != "" {
		session := c.session
		c.mu.Unlock()
		return session, nil
	}
	if call := c.loginCall; call != nil {
		c.mu.Unlock()
		<-call.done
		return call.session, call.err
	}
	call := &mirrorLoginCall{done: make(chan struct{})}
	c.loginCall = call
	c.mu.Unlock()

	token, err := c.login()
	c.mu.Lock()
	if err == nil {
		c.session.generation++
		c.session.token = token
	}
	call.session, call.err = c.session, err
	c.loginCall = nil
	close(call.done)
	c.mu.Unlock()
	return call.session, call.err
}

func (c *mirrorClient) invalidateSession(session mirrorSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A late 401 from an older request must not invalidate a newer login.
	if c.session.generation == session.generation {
		c.session.token = ""
		clear(c.cache)
	}
}

func (c *mirrorClient) login() (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range [][2]string{
		{"username", c.username}, {"password", c.password},
		{"device_uuid", "web"}, {"device_name", "Web"}, {"device_model", "Browser"},
		{"platform", "android"}, {"system_version", "Mozilla/5.0"},
		{"app_channel", "official"}, {"app_version", "web"}, {"app_version_number", "1.0.0"},
	} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return "", &mirrorClientError{kind: ErrMirrorAuth, detail: "could not create login request"}
		}
	}
	if err := writer.Close(); err != nil {
		return "", &mirrorClientError{kind: ErrMirrorAuth, detail: "could not create login request"}
	}
	var data struct {
		Token string `json:"token"`
	}
	if err := c.requestWire(http.MethodPost, "/v1/sessions", nil, &body, writer.FormDataContentType(), "", &data); err != nil {
		return "", err
	}
	if strings.TrimSpace(data.Token) == "" || strings.ContainsAny(data.Token, "\r\n") {
		return "", &mirrorClientError{kind: ErrMirrorAuth, detail: "login returned no valid session"}
	}
	return data.Token, nil
}
