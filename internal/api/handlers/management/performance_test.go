package management

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/githubauth"
)

type performanceTransport func(*http.Request) (*http.Response, error)

func (f performanceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubConnectionValidatesBeforePersistingAndDoesNotExposeToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("config-version: 8\nserver:\n  port: 8317\n  github-token: original-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(cfg, path, nil)
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; githubauth.SetToken("") })
	githubauth.SetToken("original-token")
	http.DefaultTransport = performanceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.github.com/graphql" {
			t.Fatalf("unexpected destination: %s", r.URL)
		}
		status := 200
		body := `{"data":{"viewer":{"login":"fixture-user","contributionsCollection":{"totalCommitContributions":2,"totalPullRequestContributions":1,"totalPullRequestReviewContributions":0,"contributionCalendar":{"totalContributions":3,"weeks":[]}}}}}`
		if r.Header.Get("Authorization") == "Bearer rejected-token" {
			status = 401
			body = "rejected-token"
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	request := func(method, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(method, "/v8/management/infra/github", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(ConfigV8ContextKey, true)
		if method == http.MethodPost {
			h.ConnectGitHubInfra(c)
		} else {
			h.GetGitHubInfra(c)
		}
		return recorder
	}
	rejected := request(http.MethodPost, `{"token":"rejected-token"}`)
	if rejected.Code != 422 {
		t.Fatalf("status: %d", rejected.Code)
	}
	if h.configSnapshot().GitHubToken != "original-token" || githubauth.ResolveToken() != "original-token" {
		t.Fatal("invalid token replaced current connection")
	}
	saved, _ := os.ReadFile(path)
	if strings.Contains(string(saved), "rejected-token") || strings.Contains(rejected.Body.String(), "rejected-token") {
		t.Fatal("rejected credential persisted or disclosed")
	}
	accepted := request(http.MethodPost, `{"token":"replacement-token"}`)
	if accepted.Code != 200 {
		t.Fatalf("save: %d %s", accepted.Code, accepted.Body.String())
	}
	reloaded, err := config.LoadConfig(path)
	if err != nil || reloaded.GitHubToken != "replacement-token" || githubauth.ResolveToken() != "replacement-token" {
		t.Fatal("connection was not persisted and published")
	}
	status := request(http.MethodGet, "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"login":"fixture-user"`) || strings.Contains(status.Body.String(), "replacement-token") {
		t.Fatalf("unsafe connection response: %s", status.Body.String())
	}
	h.configFilePath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	failed := request(http.MethodPost, `{"token":"unsaved-token"}`)
	if failed.Code != 500 || h.configSnapshot().GitHubToken != "replacement-token" || githubauth.ResolveToken() != "replacement-token" {
		t.Fatal("failed persistence changed active credentials")
	}
}
