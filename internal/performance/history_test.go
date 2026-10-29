package performance

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"modernc.org/sqlite"
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
	report, err := readActivity(context.Background(), filepath.Join(t.TempDir(), "missing"), path, time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || report.AvailableFrom != "2026-08-28" || report.Providers[1].Days[0].Tokens != 110 {
		t.Fatalf("archive read: %+v", report)
	}
}

func historyTokens(t *testing.T, path string) int64 {
	t.Helper()
	report, err := readActivity(context.Background(), filepath.Join(t.TempDir(), "missing"), path, time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var tokens int64
	for _, day := range report.Providers[1].Days {
		tokens += day.Tokens
	}
	return tokens
}

func TestReadActivityReadsWALHistoryDuringAndAfterImports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "performance-history.sqlite")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	writer.SetMaxOpenConns(1)
	_, err = writer.Exec(`PRAGMA journal_mode=WAL;
		CREATE TABLE executions (id,provider,session_id,started_ms,completed_ms,input_tokens,output_tokens,partial,source);
		CREATE TABLE usage_events (provider,session_id,at_ms,input_tokens,output_tokens);
		INSERT INTO usage_events VALUES ('codex','s',1787924109699,100,10)`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO usage_events VALUES ('codex','s',1787924109699,1000,0)`); err != nil {
		t.Fatal(err)
	}
	if tokens := historyTokens(t, path); tokens != 110 {
		t.Fatalf("read during an open import transaction: %d tokens", tokens)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, errStat := os.Stat(path + suffix); !os.IsNotExist(errStat) {
			t.Fatalf("writer left %s behind: %v", suffix, errStat)
		}
	}
	if tokens := historyTokens(t, path); tokens != 1110 {
		t.Fatalf("read after the import closed: %d tokens", tokens)
	}
}

// The last writer to close a WAL database holds its exclusive lock while it
// checkpoints, as the importer does at the end of every run.
func TestReadsWaitWhileAWriterLocksTheDatabase(t *testing.T) {
	for _, source := range []struct {
		name, schema string
		read         func(t *testing.T, path string)
	}{
		{"t3", `CREATE TABLE orchestration_v2_projection_provider_threads (provider_thread_id, thread_id, provider, payload_json);
			CREATE TABLE orchestration_v2_projection_provider_turns (provider_thread_id, thread_id, started_at, completed_at, payload_json, provider_turn_id)`,
			func(t *testing.T, path string) {
				if _, err := ReadT3(context.Background(), path, time.Now()); err != nil {
					t.Fatal(err)
				}
			}},
		{"history", `CREATE TABLE executions (id,provider,session_id,started_ms,completed_ms,input_tokens,output_tokens,partial,source);
			CREATE TABLE usage_events (provider,session_id,at_ms,input_tokens,output_tokens)`,
			func(t *testing.T, path string) { historyTokens(t, path) }},
	} {
		t.Run(source.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.sqlite")
			writer, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			writer.SetMaxOpenConns(1)
			if _, err = writer.Exec(`PRAGMA journal_mode=WAL;` + source.schema + `; PRAGMA locking_mode=EXCLUSIVE; BEGIN EXCLUSIVE`); err != nil {
				t.Fatal(err)
			}
			time.AfterFunc(100*time.Millisecond, func() { _ = writer.Close() })
			source.read(t, path)
		})
	}
}

// commit_import stands in for an import that commits while the proxy reads the
// history. The executions view below calls it from inside the first query.
func init() {
	sqlite.MustRegisterScalarFunction("commit_import", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		db, err := sql.Open("sqlite", args[0].(string))
		if err != nil {
			return nil, err
		}
		_, err = db.Exec(`INSERT OR IGNORE INTO usage_events VALUES ('late','codex','s',1787924109699,1000,0)`)
		return int64(1), errors.Join(err, db.Close())
	})
}

func TestReadActivityDoesNotMixImportGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "performance-history.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`PRAGMA journal_mode=WAL;
		CREATE TABLE imported_executions (id,provider,session_id,started_ms,completed_ms,input_tokens,output_tokens,partial,source);
		CREATE TABLE usage_events (id PRIMARY KEY,provider,session_id,at_ms,input_tokens,output_tokens);
		INSERT INTO imported_executions VALUES ('e','codex','s',1787924100000,1787924200000,NULL,NULL,0,'cli:codex');
		INSERT INTO usage_events VALUES ('early','codex','s',1787924109699,100,10);
		CREATE VIEW executions AS SELECT * FROM imported_executions WHERE commit_import(` + "'" + path + "'" + `)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if tokens := historyTokens(t, path); tokens != 110 {
		t.Fatalf("one read mixed two import generations: %d tokens", tokens)
	}
	if tokens := historyTokens(t, path); tokens != 1110 {
		t.Fatalf("the committed import is missing from the next read: %d tokens", tokens)
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
