package performance

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestImportedActivityDeduplicatesTurnsAndTokensAcrossMidnight(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	start := time.Date(2026, 10, 27, 2, 59, 0, 0, time.UTC).UnixMilli()
	turns := []execution{
		{ID: "t3", Provider: "grok", Session: "session", Source: "t3:v2", Start: start, End: start + 120000, Tokens: sql.NullInt64{Int64: 1100, Valid: true}},
		{ID: "native-copy", Provider: "grok", Session: "session", Source: "cli:grok", Start: start + 500, End: start + 120000},
		{ID: "direct", Provider: "grok", Session: "session", Source: "cli:grok", Start: start + 300000, End: start + 360000},
	}
	events := []usageEvent{
		{Provider: "grok", Session: "session", At: start + 1000, Tokens: 400},
		{Provider: "grok", Session: "session", At: start + 100000, Tokens: 700},
		{Provider: "grok", Session: "session", At: start + 350000, Tokens: 300},
		{Provider: "claude", Session: "deleted-transcript", At: start, Tokens: 99},
	}
	report, err := aggregateActivity(turns, events, now, true)
	if err != nil {
		t.Fatal(err)
	}
	var agents int
	var tokens int64
	for _, provider := range report.Providers {
		for _, day := range provider.Days {
			agents += day.Agents
			tokens += day.Tokens
		}
	}
	if agents != 2 || tokens != 1499 || !report.HistoryImported {
		t.Fatalf("duplicate turns or token totals: %+v", report)
	}
	claude := report.Providers[0].Days[0]
	if claude.Agents != 0 || claude.TokenRecords != 1 || claude.Date != "2026-10-26" {
		t.Fatalf("cached calls became invented executions: %+v", claude)
	}
	if len(report.Providers[2].Days) != 2 || report.Providers[2].Days[1].Tokens != 1000 {
		t.Fatalf("native tokens assigned to wrong date: %+v", report.Providers[2])
	}
}

func TestImportedActivityPreservesReportedRemainderAndPartialCoverage(t *testing.T) {
	now := time.Now()
	start := now.Add(-time.Hour).UnixMilli()
	report, err := aggregateActivity([]execution{{ID: "x", Provider: "meta", Session: "s", Source: "t3:v2", Start: start,
		Tokens: sql.NullInt64{Int64: 1000, Valid: true}, Partial: true}},
		[]usageEvent{{Provider: "meta", Session: "s", At: start + 1000, Tokens: 300}}, now, true)
	if err != nil {
		t.Fatal(err)
	}
	day := report.Providers[3].Days[0]
	if day.Tokens != 1000 || day.ReportedAgents != 1 || day.PartialAgents != 1 {
		t.Fatalf("lost reported tokens: %+v", day)
	}
}

func TestNativeTurnWithoutCompletionDoesNotDuplicateItsT3Turn(t *testing.T) {
	now := time.Now()
	start := now.Add(-time.Hour).UnixMilli()
	turns := []execution{
		{ID: "native", Provider: "claude", Session: "s", Source: "cli:claude", Start: start - 5000},
		{ID: "t3", Provider: "claude", Session: "s", Source: "t3:v2", Start: start, End: start + 10000, Tokens: sql.NullInt64{Int64: 100, Valid: true}},
		{ID: "later", Provider: "claude", Session: "s", Source: "cli:claude", Start: start + 20000},
	}
	report, err := aggregateActivity(turns, []usageEvent{{Provider: "claude", Session: "s", At: start + 5000, Tokens: 100}}, now, true)
	if err != nil {
		t.Fatal(err)
	}
	day := report.Providers[0].Days[0]
	if day.Agents != 2 || day.Tokens != 100 || day.LongestDurationMs != 10000 {
		t.Fatalf("native boundary duplicated T3 usage or invented duration: %+v", day)
	}
}

func TestReadActivityWorksWithoutT3AndDoesNotModifyArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "performance-history.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE executions (id,provider,session_id,started_ms,completed_ms,input_tokens,output_tokens,partial,source);
		CREATE TABLE usage_events (provider,session_id,at_ms,input_tokens,output_tokens);
		INSERT INTO usage_events VALUES ('codex','s',1787924109699,100,10)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	report, err := ReadActivity(context.Background(), filepath.Join(t.TempDir(), "missing"), path, time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || report.AvailableFrom != "2026-08-28" || report.Providers[1].Days[0].Tokens != 110 {
		t.Fatalf("archive read: %+v", report)
	}
}

func TestActivityReportIndependentOfEventOrder(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	start := time.Date(2026, 10, 27, 2, 59, 0, 0, time.UTC).UnixMilli()
	turns := []execution{
		{ID: "t3", Provider: "grok", Session: "s", Source: "t3:v2", Start: start, End: start + 120000, Tokens: sql.NullInt64{Int64: 1100, Valid: true}},
		{ID: "native", Provider: "grok", Session: "s", Source: "cli:grok", Start: start + 500, End: start + 120000},
		{ID: "open", Provider: "grok", Session: "s", Source: "t3:v2", Start: start + 300000, Partial: true},
	}
	events := []usageEvent{
		{Provider: "grok", Session: "s", At: start + 1000, Tokens: 400},
		{Provider: "grok", Session: "s", At: start + 100000, Tokens: 700},
		{Provider: "grok", Session: "s", At: start + 100000, Tokens: 13},
		{Provider: "grok", Session: "s", At: start + 350000, Tokens: 300},
		{Provider: "claude", Session: "deleted", At: start, Tokens: 99},
		{Provider: "codex", Session: "future", At: now.Add(time.Hour).UnixMilli(), Tokens: 5},
	}
	want, err := aggregateActivity(slices.Clone(turns), events, now, true)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(events)
	got, err := aggregateActivity(slices.Clone(turns), events, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("event order changed report: %#v != %#v", want, got)
	}
}
