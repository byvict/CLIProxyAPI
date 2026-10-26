package submon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

type quotaReader struct {
	native nativeAccess
	client *http.Client
}

func quotaClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 0
	transport.TLSHandshakeTimeout = 0
	transport.IdleConnTimeout = 0
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (r quotaReader) sources() []source {
	out := []source{}
	for _, p := range providers {
		out = append(out, source{ID: string(p) + "-native", Provider: p, Cadence: cadence(p), Probe: func(c probeContext) probe { return r.probe(c, p) }})
	}
	return out
}
func retryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 && seconds < 31536000 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if t, err := http.ParseTime(value); err == nil && t.After(now) {
		return t
	}
	return time.Time{}
}
func (r quotaReader) request(ctx context.Context, now time.Time, url, access string, headers map[string]string, post bool) ([]byte, *missing) {
	method := http.MethodGet
	var body io.Reader
	if post {
		method = http.MethodPost
		body = bytes.NewBufferString("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, &missing{Status: transportFailure}
	}
	req.Header.Set("Authorization", "Bearer "+access)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := r.client.Do(req)
	if err != nil {
		return nil, &missing{Status: transportFailure}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		st := transportFailure
		switch res.StatusCode {
		case 401, 403:
			st = authFailure
		case 429:
			st = rateLimited
		}
		return nil, &missing{Status: st, Code: "http_" + strconv.Itoa(res.StatusCode), RetryAt: retryAfter(res.Header.Get("Retry-After"), now)}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil {
		return nil, &missing{Status: transportFailure}
	}
	if len(b) > 2*1024*1024 || !json.Valid(b) {
		return nil, &missing{Status: invalidResponse}
	}
	return b, nil
}
func (r quotaReader) probe(c probeContext, p provider) probe {
	result := probe{StartedAt: c.Now(), At: c.Now()}
	cred, err := r.native.acquire(c.Context, p)
	if err != nil {
		result.Outcome = acquisitionFailure(err)
		return result
	}
	if (p == codex || p == grok) && cred.Identity.Subject == "" {
		result.Outcome = missing{Status: identityUnverified}
		return result
	}
	var body []byte
	var absent *missing
	switch p {
	case codex:
		body, absent = r.request(c.Context, c.Now(), "https://chatgpt.com/backend-api/wham/usage", cred.Access, map[string]string{"ChatGPT-Account-Id": cred.Account, "User-Agent": "codex-cli"}, false)
	case grok:
		body, absent = r.request(c.Context, c.Now(), "https://cli-chat-proxy.grok.com/v1/billing?format=credits", cred.Access, nil, false)
	case claude:
		headers := map[string]string{"anthropic-beta": "oauth-2025-04-20", "User-Agent": "claude-code"}
		profile, profileMissing := r.request(c.Context, c.Now(), "https://api.anthropic.com/api/oauth/profile", cred.Access, headers, false)
		if profileMissing != nil {
			result.Outcome = missing{Status: identityUnverified, Code: profileMissing.Code, RetryAt: profileMissing.RetryAt}
			return result
		}
		cred.Identity = parseProfile(profile)
		if cred.Identity.Subject == "" {
			result.Outcome = missing{Status: identityUnverified}
			return result
		}
		body, absent = r.request(c.Context, c.Now(), "https://api.anthropic.com/api/oauth/usage", cred.Access, headers, false)
	case muse:
		result.SideEffects = []string{"credential_exchange_requested"}
		body, absent = r.request(c.Context, c.Now(), "https://api.meta.ai/muse-code/key", cred.Access, map[string]string{"x-api-version": "1.0.0", "Content-Type": "application/json", "User-Agent": "muse-code"}, true)
	}
	result.At = c.Now()
	if absent != nil {
		result.Outcome = *absent
		return result
	}
	if p == muse {
		result.Outcome = parseMuse(body)
		return result
	}
	windows, err := parseWindows(p, body)
	if err != nil {
		result.Outcome = missing{Status: invalidResponse}
		return result
	}
	result.Outcome = reading{Identity: cred.Identity, Windows: windows}
	return result
}
