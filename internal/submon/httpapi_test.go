package submon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
)

func testAPI(t *testing.T) (http.Handler, *store, string) {
	t.Helper()
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.close() })
	p := successfulProbe(testTime(), "fixture-account")
	if err = s.append(p, p.At.Add(5*time.Minute), 0); err != nil {
		t.Fatal(err)
	}
	c := newCollector(s, nil, testTime)
	a := newAPI(s, c, "fixture-monitor-key", testTime, "127.0.0.1:8318")
	return a, s, p.Outcome.(reading).Identity.accountID()
}
func requestAPI(h http.Handler, method, path, body, key, origin, host string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8318"+path, strings.NewReader(body))
	req.Host = host
	req.RemoteAddr = "127.0.0.1:1234"
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func TestHealthIdentifiesBuildWithoutCredentials(t *testing.T) {
	h, _, _ := testAPI(t)
	w := requestAPI(h, "GET", "/healthz", "", "", "", "127.0.0.1:8318", nil)
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || body["status"] != "ok" || body["version"] != buildinfo.Version || body["commit"] != buildinfo.Commit || body["build_date"] != buildinfo.BuildDate {
		t.Fatalf("unexpected build health: %d %s", w.Code, w.Body.String())
	}
	if w := requestAPI(h, "GET", "/healthz", "", "", "", "evil.test:8318", nil); w.Code != 403 {
		t.Fatal("health bypassed host validation")
	}
}

func TestHTTPAuthenticationHostOriginAndEmail(t *testing.T) {
	h, _, id := testAPI(t)
	host := "127.0.0.1:8318"
	origin := "http://" + host
	for _, path := range []string{"/api/v1/snapshot", "/api/v1/view?provider=all"} {
		if w := requestAPI(h, "GET", path, "", "", "", host, nil); w.Code != 401 {
			t.Fatal("unprotected", path, w.Code)
		}
		w := requestAPI(h, "GET", path, "", "fixture-monitor-key", "", host, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "fixture-private@example.test") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("email/cache disclosure")
		}
	}
	if w := requestAPI(h, "GET", "/api/v1/snapshot", "", "fixture-monitor-key", "", "evil.test:8318", nil); w.Code != 403 {
		t.Fatal("Host accepted")
	}
	if w := requestAPI(h, "POST", "/session", `{"key":"fixture-monitor-key"}`, "", "https://evil.test", host, nil); w.Code != 403 {
		t.Fatal("login CSRF")
	}
	w := requestAPI(h, "POST", "/session", `{"key":"fixture-monitor-key"}`, "", origin, host, nil)
	if w.Code != 200 {
		t.Fatal("session", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	cookie := cookies[0]
	if w := requestAPI(h, "GET", "/api/v1/snapshot", "", "", "", host, cookie); w.Code != 200 {
		t.Fatal("cookie rejected")
	}
	path := "/api/v1/accounts/" + id + "/email"
	if w := requestAPI(h, "GET", path, "", "fixture-monitor-key", "", host, nil); w.Code != 405 {
		t.Fatal("GET email allowed")
	}
	if w := requestAPI(h, "POST", path, `{}`, "", "", host, cookie); w.Code != 403 {
		t.Fatal("missing Origin")
	}
	if w := requestAPI(h, "POST", path, `{}`, "", "https://evil.test", host, cookie); w.Code != 403 {
		t.Fatal("Origin accepted")
	}
	w = requestAPI(h, "POST", path, `{}`, "", origin, host, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture-private@example.test") {
		t.Fatal("explicit email reveal failed")
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS enabled")
	}
}
func TestHTTPCookieExpiryAndMalformedRequests(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	now := testTime()
	clock := func() time.Time { return now }
	h := newAPI(s, newCollector(s, nil, clock), "fixture-key", clock, "127.0.0.1:8318")
	host := "127.0.0.1:8318"
	w := requestAPI(h, "POST", "/session", `{"key":"fixture-key"}`, "", "http://"+host, host, nil)
	cookie := w.Result().Cookies()[0]
	now = now.Add(13 * time.Hour)
	if w := requestAPI(h, "GET", "/api/v1/snapshot", "", "", "", host, cookie); w.Code != 401 {
		t.Fatal("expired session")
	}
	for _, path := range []string{"/api/v1/view?provider=invalid", "/api/v1/view?provider=claude&period=-1h", "/api/v1/snapshot?format=invalid"} {
		if w := requestAPI(h, "GET", path, "", "fixture-key", "", host, nil); w.Code != 400 {
			t.Fatal("invalid selection accepted", path, w.Code)
		}
	}
	w = requestAPI(h, "GET", "/api/v1/snapshot?format=t3-usage", "", "fixture-key", "", host, nil)
	var d usageReport
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || len(d.Providers) != 4 {
		t.Fatal("bad usage contract")
	}
}
