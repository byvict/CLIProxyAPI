// Package performance reads activity without changing the source applications.
package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

type Day struct {
	Date              string `json:"date"`
	Agents            int    `json:"agents"`
	Tokens            int64  `json:"tokens"`
	ReportedAgents    int    `json:"reportedAgents"`
	PartialAgents     int    `json:"partialAgents"`
	LongestDurationMs int64  `json:"longestDurationMs"`
}

type Provider struct {
	ID   string `json:"id"`
	Days []Day  `json:"days"`
}

type Report struct {
	Year          int        `json:"year"`
	Timezone      string     `json:"timezone"`
	Today         string     `json:"today"`
	GeneratedAt   time.Time  `json:"generatedAt"`
	AvailableFrom string     `json:"availableFrom"`
	Providers     []Provider `json:"providers"`
}

type turnUsage struct {
	Scope  string `json:"usageScope"`
	Status string `json:"usageStatus"`
	Input  *int64 `json:"inputTokens"`
	Output *int64 `json:"outputTokens"`
}

// ReadT3 uses only the v2 projections, never event bodies, prompts, or credentials.
// A turn is an agent execution. Input tokens already include cache reads/writes;
// output tokens already include reasoning, so neither is added a second time.
func ReadT3(ctx context.Context, path string, now time.Time) (Report, error) {
	zone, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return Report{}, err
	}
	now = now.In(zone)
	report := Report{Year: now.Year(), Timezone: zone.String(), Today: now.Format(time.DateOnly), GeneratedAt: now, Providers: []Provider{}}
	for _, id := range []string{"claude", "codex", "grok", "meta"} {
		report.Providers = append(report.Providers, Provider{ID: id, Days: []Day{}})
	}
	if _, errStat := os.Stat(path); errStat != nil {
		return report, errStat
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return report, err
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Debug("close T3code activity database")
		}
	}()
	db.SetMaxOpenConns(1)
	start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, zone)
	rows, err := db.QueryContext(ctx, `SELECT p.provider, t.started_at, t.completed_at,
		json_extract(t.payload_json, '$.turnTokenUsage')
		FROM orchestration_v2_projection_provider_turns t
		JOIN orchestration_v2_projection_provider_threads p
		ON p.provider_thread_id = t.provider_thread_id AND p.thread_id = t.thread_id
		WHERE t.started_at >= ? AND t.started_at < ?
		AND p.provider IN ('claudeAgent', 'claude', 'codex', 'grok', 'muse', 'meta')
		ORDER BY t.started_at`, start.UTC().Format(time.DateOnly), now.UTC().AddDate(0, 0, 1).Format(time.DateOnly))
	if err != nil {
		return report, fmt.Errorf("read T3code activity: %w", err)
	}
	defer func() {
		if errClose := rows.Close(); errClose != nil {
			log.WithError(errClose).Debug("close T3code activity rows")
		}
	}()
	days := map[string]map[string]*Day{}
	for rows.Next() {
		var provider, started string
		var completed, rawUsage sql.NullString
		if errScan := rows.Scan(&provider, &started, &completed, &rawUsage); errScan != nil {
			return report, errScan
		}
		if provider == "claudeAgent" {
			provider = "claude"
		} else if provider == "muse" {
			provider = "meta"
		}
		at, errTime := time.Parse(time.RFC3339Nano, started)
		if errTime != nil {
			return report, fmt.Errorf("invalid T3code activity timestamp: %w", errTime)
		}
		if at.Before(start) || at.After(now) {
			continue
		}
		date := at.In(zone).Format(time.DateOnly)
		if report.AvailableFrom == "" {
			report.AvailableFrom = date
		}
		if days[provider] == nil {
			days[provider] = map[string]*Day{}
		}
		day := days[provider][date]
		if day == nil {
			day = &Day{Date: date}
			days[provider][date] = day
		}
		day.Agents++
		if end, errEnd := time.Parse(time.RFC3339Nano, completed.String); errEnd == nil && !end.After(now) && end.After(at) {
			day.LongestDurationMs = max(day.LongestDurationMs, end.Sub(at).Milliseconds())
		}
		var usage turnUsage
		if rawUsage.Valid && json.Unmarshal([]byte(rawUsage.String), &usage) != nil {
			return report, fmt.Errorf("invalid T3code token usage")
		}
		if usage.Scope == "main_agent" && (usage.Status == "complete" || usage.Status == "partial") && usage.Input != nil && usage.Output != nil && *usage.Input >= 0 && *usage.Output >= 0 {
			day.Tokens += *usage.Input + *usage.Output
			day.ReportedAgents++
			if usage.Status == "partial" {
				day.PartialAgents++
			}
		}
	}
	if errRows := rows.Err(); errRows != nil {
		return report, errRows
	}
	for i := range report.Providers {
		for date := start; !date.After(now); date = date.AddDate(0, 0, 1) {
			if day := days[report.Providers[i].ID][date.Format(time.DateOnly)]; day != nil {
				report.Providers[i].Days = append(report.Providers[i].Days, *day)
			}
		}
	}
	return report, nil
}
