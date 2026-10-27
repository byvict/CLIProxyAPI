package performance

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadT3AggregatesNativeTurnsWithoutDoubleCountingTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statev2.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE orchestration_v2_projection_provider_threads (provider_thread_id TEXT, thread_id TEXT, provider TEXT, payload_json TEXT)`,
		`CREATE TABLE orchestration_v2_projection_provider_turns (provider_thread_id TEXT, thread_id TEXT, started_at TEXT, completed_at TEXT, payload_json TEXT, provider_turn_id TEXT)`,
		`INSERT INTO orchestration_v2_projection_provider_threads VALUES ('c','c','claudeAgent','{}'), ('x','x','codex','{}'), ('g','g','grok','{}'), ('m','m','muse','{}'), ('z','z','cursor','{}')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(provider, start, end, usage string) {
		t.Helper()
		_, errInsert := db.Exec(`INSERT INTO orchestration_v2_projection_provider_turns VALUES (?, ?, ?, ?, ?, ?)`, provider, provider, start, end, usage, provider+start)
		if errInsert != nil {
			t.Fatal(errInsert)
		}
	}
	complete := `{"turnTokenUsage":{"usageScope":"main_agent","usageStatus":"complete","inputTokens":1000,"outputTokens":100,"cachedInputTokens":900,"cacheCreationTokens":50,"reasoningTokens":80}}`
	insert("c", "2026-10-26T23:00:00Z", "2026-10-27T01:00:00Z", complete)
	insert("x", "2026-10-27T01:00:00Z", "2026-10-27T01:30:00Z", complete)
	insert("g", "2026-10-27T13:00:00Z", "2026-10-27T13:20:00Z", `{"tokenUsage":{"usedTokens":99999}}`)
	insert("m", "2026-10-27T13:30:00Z", "", `{"turnTokenUsage":{"usageScope":"main_agent","usageStatus":"partial","inputTokens":500,"outputTokens":10}}`)
	insert("c", "2026-01-01T02:00:00Z", "2026-01-01T02:10:00Z", complete)
	insert("c", "2026-10-28T01:00:00Z", "2026-10-28T02:00:00Z", complete)
	insert("z", "2026-10-27T01:00:00Z", "2026-10-27T01:30:00Z", complete)
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	before, _ := os.ReadFile(path)
	report, err := ReadT3(context.Background(), path, now)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("reader changed the source database")
	}
	if report.Today != "2026-10-27" || report.AvailableFrom != "2026-10-26" || len(report.Providers) != 4 {
		t.Fatalf("unexpected report: %+v", report)
	}
	for _, provider := range report.Providers {
		if len(provider.Days) != 1 {
			t.Fatalf("unexpected days for %s: %+v", provider.ID, provider.Days)
		}
		day := provider.Days[0]
		if day.Agents != 1 {
			t.Fatalf("agents: %+v", day)
		}
		switch provider.ID {
		case "claude":
			if day.Tokens != 1100 || day.LongestDurationMs != 7200000 || day.Date != "2026-10-26" {
				t.Fatalf("claude: %+v", day)
			}
		case "codex":
			if day.Tokens != 1100 || day.Date != "2026-10-26" {
				t.Fatalf("codex timezone/cache: %+v", day)
			}
		case "grok":
			if day.Tokens != 0 || day.ReportedAgents != 0 {
				t.Fatalf("context usage is not turn usage: %+v", day)
			}
		case "meta":
			if day.Tokens != 510 || day.PartialAgents != 1 || day.LongestDurationMs != 0 {
				t.Fatalf("meta: %+v", day)
			}
		}
	}
}

func TestReadT3MissingDatabaseIsNotCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	_, err := ReadT3(context.Background(), path, time.Now())
	if !os.IsNotExist(err) {
		t.Fatalf("error: %v", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created missing database")
	}
}
