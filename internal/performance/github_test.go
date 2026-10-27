package performance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type githubDoer struct {
	client *http.Client
	url    string
}

func (d githubDoer) Do(request *http.Request) (*http.Response, error) {
	next := request.Clone(request.Context())
	local, _ := http.NewRequestWithContext(request.Context(), request.Method, d.url, request.Body)
	next.URL = local.URL
	return d.client.Do(next)
}

const githubFixture = `{"data":{"viewer":{"login":"fixture-user","contributionsCollection":{"totalCommitContributions":7,"totalPullRequestContributions":2,"totalPullRequestReviewContributions":1,"contributionCalendar":{"totalContributions":10,"weeks":[{"contributionDays":[{"date":"2026-10-26","contributionCount":4},{"date":"2026-10-27","contributionCount":6}]}]}}}}}`

func TestReadGitHubContributionsAndSafeFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		body      string
		wantError bool
	}{
		{"calendar", 200, githubFixture, false},
		{"invalid token", 401, "secret-value", true},
		{"rate limited", 403, "secret-value", true},
		{"graphql partial errors", 200, `{"data":{"viewer":{"login":"user"}},"errors":[{"message":"secret-value"}]}`, true},
		{"malformed", 200, `{"data":{}}`, true},
		{"oversized", 200, strings.Repeat("x", 2*1024*1024+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret-value" {
					t.Error("missing authenticated GraphQL request")
				}
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "contributionCalendar") || !strings.Contains(string(body), "2026-01-01T00:00:00Z") {
					t.Error("wrong query")
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			activity, err := ReadGitHub(context.Background(), githubDoer{server.Client(), server.URL}, "secret-value", time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC))
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-value") {
				t.Fatal("credential leaked")
			}
			if !tc.wantError && (activity.Login != "fixture-user" || activity.Contributions != 10 || activity.PullRequests != 2 || activity.Reviews != 1 || activity.Commits != 7 || len(activity.Days) != 2) {
				t.Fatalf("activity: %+v", activity)
			}
		})
	}
}
