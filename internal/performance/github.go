package performance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/httpfetch"
	log "github.com/sirupsen/logrus"
)

type ContributionDay struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type GitHubActivity struct {
	Login         string            `json:"login"`
	Year          int               `json:"year"`
	Contributions int               `json:"contributions"`
	PullRequests  int               `json:"pullRequests"`
	Reviews       int               `json:"reviews"`
	Commits       int               `json:"commits"`
	Days          []ContributionDay `json:"days"`
}

// ReadGitHub requests only the authenticated account's aggregate contributions.
// Upstream error bodies are deliberately excluded from errors and logs.
func ReadGitHub(ctx context.Context, client httpfetch.Doer, token string, now time.Time) (GitHubActivity, error) {
	query := `query Performance($from: DateTime!, $to: DateTime!) {
		viewer { login contributionsCollection(from: $from, to: $to) {
			totalCommitContributions totalPullRequestContributions totalPullRequestReviewContributions
			contributionCalendar { totalContributions weeks { contributionDays { date contributionCount } } }
		} }
	}`
	end := now.UTC()
	if nextYear := time.Date(now.Year()+1, 1, 1, 0, 0, 0, 0, time.UTC); !end.Before(nextYear) {
		end = nextYear.Add(-time.Second)
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": map[string]string{
		"from": time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"to":   end.Format(time.RFC3339),
	}})
	if err != nil {
		return GitHubActivity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/graphql", bytes.NewReader(body))
	if err != nil {
		return GitHubActivity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "CLIProxyAPI-Performance")
	response, err := client.Do(req)
	if err != nil {
		return GitHubActivity{}, fmt.Errorf("could not reach GitHub")
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			log.Debug("failed to close GitHub contribution response")
		}
	}()
	if response.StatusCode == http.StatusUnauthorized {
		return GitHubActivity{}, fmt.Errorf("GitHub rejected the token; reconnect with a valid token")
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		return GitHubActivity{}, fmt.Errorf("GitHub denied access; check token permissions or try again after the rate limit resets")
	}
	if response.StatusCode != http.StatusOK {
		return GitHubActivity{}, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	const maxResponse = 2 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return GitHubActivity{}, fmt.Errorf("could not read GitHub contributions")
	}
	var result struct {
		Data struct {
			Viewer *struct {
				Login         string `json:"login"`
				Contributions *struct {
					Commits      int `json:"totalCommitContributions"`
					PullRequests int `json:"totalPullRequestContributions"`
					Reviews      int `json:"totalPullRequestReviewContributions"`
					Calendar     *struct {
						Total int `json:"totalContributions"`
						Weeks []struct {
							Days []struct {
								Date  string `json:"date"`
								Count int    `json:"contributionCount"`
							} `json:"contributionDays"`
						} `json:"weeks"`
					} `json:"contributionCalendar"`
				} `json:"contributionsCollection"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Errors) > 0 || result.Data.Viewer == nil || result.Data.Viewer.Login == "" || result.Data.Viewer.Contributions == nil || result.Data.Viewer.Contributions.Calendar == nil {
		return GitHubActivity{}, fmt.Errorf("GitHub did not return contributions; check the token's access to the account")
	}
	viewer := result.Data.Viewer
	contributions := viewer.Contributions
	activity := GitHubActivity{Login: viewer.Login, Year: now.Year(), Contributions: contributions.Calendar.Total,
		PullRequests: contributions.PullRequests, Reviews: contributions.Reviews, Commits: contributions.Commits, Days: []ContributionDay{}}
	for _, week := range contributions.Calendar.Weeks {
		for _, day := range week.Days {
			date, errDate := time.Parse(time.DateOnly, day.Date)
			if errDate != nil || day.Count < 0 {
				return GitHubActivity{}, fmt.Errorf("GitHub returned an invalid contribution calendar")
			}
			if date.Year() == now.Year() {
				activity.Days = append(activity.Days, ContributionDay{Date: day.Date, Count: day.Count})
			}
		}
	}
	return activity, nil
}
