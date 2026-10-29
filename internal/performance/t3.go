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
	TokenRecords      int    `json:"tokenRecords"`
	ReportedAgents    int    `json:"reportedAgents"`
	PartialAgents     int    `json:"partialAgents"`
	LongestDurationMs int64  `json:"longestDurationMs"`
}

type Provider struct {
	ID   string `json:"id"`
	Days []Day  `json:"days"`
}

type Report struct {
	Year            int        `json:"year"`
	Timezone        string     `json:"timezone"`
	Today           string     `json:"today"`
	GeneratedAt     time.Time  `json:"generatedAt"`
	AvailableFrom   string     `json:"availableFrom"`
	Providers       []Provider `json:"providers"`
	HistoryImported bool       `json:"historyImported"`
}

type turnUsage struct {
	Scope  string `json:"usageScope"`
	Status string `json:"usageStatus"`
	Input  *int64 `json:"inputTokens"`
	Output *int64 `json:"outputTokens"`
}

// ReadT3 reads the current provider projections without changing the source.
func ReadT3(ctx context.Context, path string, now time.Time) (Report, error) {
	turns, err := readT3Turns(ctx, path)
	if err != nil {
		return Report{}, err
	}
	return aggregateActivity(turns, nil, now, false)
}

// readOnlyDSN waits out the locks a writer holds while it opens or closes a WAL
// database instead of failing the read.
func readOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	return u.String()
}

func readT3Turns(ctx context.Context, path string) ([]execution, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Debug("close T3code activity database")
		}
	}()
	db.SetMaxOpenConns(1)
	rows, err := db.QueryContext(ctx, `SELECT t.provider_turn_id, p.provider,
        COALESCE(json_extract(p.payload_json, '$.nativeThreadRef.nativeId'), t.provider_turn_id),
        t.started_at, t.completed_at, json_extract(t.payload_json, '$.turnTokenUsage')
        FROM orchestration_v2_projection_provider_turns t
        JOIN orchestration_v2_projection_provider_threads p
        ON p.provider_thread_id = t.provider_thread_id AND p.thread_id = t.thread_id
        WHERE t.started_at IS NOT NULL
        AND p.provider IN ('claudeAgent', 'claude', 'codex', 'grok', 'muse', 'meta')`)
	if err != nil {
		return nil, fmt.Errorf("read T3code activity: %w", err)
	}
	defer func() {
		if errClose := rows.Close(); errClose != nil {
			log.WithError(errClose).Debug("close T3code activity rows")
		}
	}()
	turns := []execution{}
	for rows.Next() {
		var turn execution
		var started string
		var completed, rawUsage sql.NullString
		if errScan := rows.Scan(&turn.ID, &turn.Provider, &turn.Session, &started, &completed, &rawUsage); errScan != nil {
			return nil, errScan
		}
		at, errTime := time.Parse(time.RFC3339Nano, started)
		if errTime != nil {
			return nil, fmt.Errorf("invalid T3code activity timestamp: %w", errTime)
		}
		turn.Start = at.UnixMilli()
		if end, errEnd := time.Parse(time.RFC3339Nano, completed.String); errEnd == nil {
			turn.End = end.UnixMilli()
		}
		var usage turnUsage
		if rawUsage.Valid && json.Unmarshal([]byte(rawUsage.String), &usage) != nil {
			return nil, fmt.Errorf("invalid T3code token usage")
		}
		if usage.Scope == "main_agent" && (usage.Status == "complete" || usage.Status == "partial") && usage.Input != nil && usage.Output != nil && *usage.Input >= 0 && *usage.Output >= 0 {
			turn.Tokens = sql.NullInt64{Int64: *usage.Input + *usage.Output, Valid: true}
			turn.Partial = usage.Status == "partial"
		}
		turn.Provider = normalizeProvider(turn.Provider)
		turn.Source = "t3:v2"
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}
