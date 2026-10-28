package performance

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type execution struct {
	ID, Provider, Session, Source string
	Start, End                    int64
	matchEnd                      int64
	Tokens                        sql.NullInt64
	Partial                       bool
}

type usageEvent struct {
	Provider, Session string
	At, Tokens        int64
}

func normalizeProvider(provider string) string {
	switch provider {
	case "claudeAgent":
		return "claude"
	case "muse":
		return "meta"
	default:
		return provider
	}
}

// ReadActivity merges imported observations with current T3 turns. Live projections
// replace their imported copies. Native tokens replace overlapping turn totals.
func ReadActivity(ctx context.Context, t3Path, historyPath string, now time.Time) (Report, error) {
	turns, err := readT3Turns(ctx, t3Path)
	if err != nil && !os.IsNotExist(err) {
		return Report{}, err
	}
	if _, errStat := os.Stat(historyPath); os.IsNotExist(errStat) {
		if err != nil {
			return Report{}, err
		}
		return aggregateActivity(turns, nil, now, false)
	} else if errStat != nil {
		return Report{}, errStat
	}
	u := url.URL{Scheme: "file", Path: historyPath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return Report{}, err
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Debug("close imported performance history")
		}
	}()
	db.SetMaxOpenConns(1)
	rows, err := db.QueryContext(ctx, `SELECT id,provider,session_id,started_ms,
		COALESCE(completed_ms,0),input_tokens+output_tokens,partial,source FROM executions ORDER BY started_ms`)
	if err != nil {
		return Report{}, fmt.Errorf("read imported executions: %w", err)
	}
	seen := map[string]bool{}
	for _, turn := range turns {
		seen[turn.ID] = true
	}
	for rows.Next() {
		var turn execution
		if errScan := rows.Scan(&turn.ID, &turn.Provider, &turn.Session, &turn.Start, &turn.End, &turn.Tokens, &turn.Partial, &turn.Source); errScan != nil {
			_ = rows.Close()
			return Report{}, errScan
		}
		if !seen[turn.ID] {
			turns = append(turns, turn)
			seen[turn.ID] = true
		}
	}
	errRows := rows.Err()
	_ = rows.Close()
	if errRows != nil {
		return Report{}, errRows
	}
	rows, err = db.QueryContext(ctx, `SELECT provider,session_id,at_ms,input_tokens+output_tokens FROM usage_events`)
	if err != nil {
		return Report{}, fmt.Errorf("read imported token events: %w", err)
	}
	defer func() {
		if errClose := rows.Close(); errClose != nil {
			log.WithError(errClose).Debug("close imported token rows")
		}
	}()
	events := []usageEvent{}
	for rows.Next() {
		var event usageEvent
		if errScan := rows.Scan(&event.Provider, &event.Session, &event.At, &event.Tokens); errScan != nil {
			return Report{}, errScan
		}
		events = append(events, event)
	}
	if errRows = rows.Err(); errRows != nil {
		return Report{}, errRows
	}
	return aggregateActivity(turns, events, now, true)
}

func aggregateActivity(turns []execution, events []usageEvent, now time.Time, imported bool) (Report, error) {
	zone, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return Report{}, err
	}
	now = now.In(zone)
	start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, zone).UnixMilli()
	until := now.UnixMilli()
	report := Report{Year: now.Year(), Timezone: zone.String(), Today: now.Format(time.DateOnly), GeneratedAt: now, Providers: []Provider{}, HistoryImported: imported}
	days := map[string]map[string]*Day{}
	for _, id := range []string{"claude", "codex", "grok", "meta"} {
		report.Providers = append(report.Providers, Provider{ID: id, Days: []Day{}})
		days[id] = map[string]*Day{}
	}
	dayAt := func(provider string, at int64) *Day {
		if at < start || at > until || days[provider] == nil {
			return nil
		}
		date := time.UnixMilli(at).In(zone).Format(time.DateOnly)
		if report.AvailableFrom == "" || date < report.AvailableFrom {
			report.AvailableFrom = date
		}
		if days[provider][date] == nil {
			days[provider][date] = &Day{Date: date}
		}
		return days[provider][date]
	}
	// Prefer T3's execution boundaries over the same turns in native transcripts.
	// Native turns with no T3 overlap remain independently observable executions.
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].Start < turns[j].Start })
	next := map[string]int64{}
	for i := len(turns) - 1; i >= 0; i-- {
		turn := &turns[i]
		// A missing completion is bounded by the next observed execution for
		// matching only. It never supplies a reported duration.
		key := turn.Provider + "\x00" + turn.Session + "\x00" + turn.Source
		turn.matchEnd = turn.End
		if turn.matchEnd == 0 {
			turn.matchEnd = until
			if at, ok := next[key]; ok {
				turn.matchEnd = at - 1
			}
		}
		next[key] = turn.Start
	}
	bySession := map[string][]execution{}
	for _, turn := range turns {
		if strings.HasPrefix(turn.Source, "t3:") {
			key := turn.Provider + "\x00" + turn.Session
			bySession[key] = append(bySession[key], turn)
		}
	}
	selected := map[string][]execution{}
	for _, turn := range turns {
		key := turn.Provider + "\x00" + turn.Session
		duplicate := false
		if !strings.HasPrefix(turn.Source, "t3:") {
			for _, known := range bySession[key] {
				if turn.Start <= known.matchEnd+1000 && known.Start <= turn.matchEnd+1000 {
					duplicate = true
					break
				}
			}
		}
		if !duplicate {
			selected[key] = append(selected[key], turn)
		}
	}
	for key := range selected {
		sort.SliceStable(selected[key], func(i, j int) bool {
			return strings.HasPrefix(selected[key][i].Source, "t3:") && !strings.HasPrefix(selected[key][j].Source, "t3:")
		})
	}
	matched := map[string]int64{}
	for _, event := range events {
		day := dayAt(event.Provider, event.At)
		if day == nil {
			continue
		}
		day.Tokens += event.Tokens
		day.TokenRecords++
		for _, turn := range selected[event.Provider+"\x00"+event.Session] {
			if event.At >= turn.Start && event.At <= turn.matchEnd+1000 {
				matched[turn.ID] += event.Tokens
				break
			}
		}
	}
	for _, sessionTurns := range selected {
		for _, turn := range sessionTurns {
			day := dayAt(turn.Provider, turn.Start)
			if day == nil {
				continue
			}
			day.Agents++
			if turn.End > turn.Start && turn.End <= until {
				day.LongestDurationMs = max(day.LongestDurationMs, turn.End-turn.Start)
			}
			nativeTokens := matched[turn.ID]
			if turn.Tokens.Valid || nativeTokens > 0 {
				day.ReportedAgents++
				if turn.Partial || !turn.Tokens.Valid || nativeTokens > 0 && nativeTokens != turn.Tokens.Int64 {
					day.PartialAgents++
				}
			}
			if turn.Tokens.Valid {
				// Keep only the reported remainder if the native transcript is incomplete.
				day.Tokens += max(int64(0), turn.Tokens.Int64-nativeTokens)
			}
		}
	}
	for i := range report.Providers {
		for _, day := range days[report.Providers[i].ID] {
			report.Providers[i].Days = append(report.Providers[i].Days, *day)
		}
		sort.Slice(report.Providers[i].Days, func(a, b int) bool { return report.Providers[i].Days[a].Date < report.Providers[i].Days[b].Date })
	}
	return report, nil
}
