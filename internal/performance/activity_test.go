package performance

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func execSQL(t *testing.T, path, statements string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(statements); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}

// activityFixture writes both sources in WAL mode with their writers closed, as
// they are between T3 sessions and imports.
func activityFixture(t *testing.T) *Activity {
	t.Helper()
	dir := t.TempDir()
	t3 := filepath.Join(dir, "statev2.sqlite")
	history := filepath.Join(dir, "performance-history.sqlite")
	execSQL(t, t3, `PRAGMA journal_mode=WAL;
		CREATE TABLE orchestration_v2_projection_provider_threads (provider_thread_id TEXT, thread_id TEXT, provider TEXT, payload_json TEXT);
		CREATE TABLE orchestration_v2_projection_provider_turns (provider_thread_id TEXT, thread_id TEXT, started_at TEXT, completed_at TEXT, payload_json TEXT, provider_turn_id TEXT);
		INSERT INTO orchestration_v2_projection_provider_threads VALUES ('c','c','claudeAgent','{}');
		INSERT INTO orchestration_v2_projection_provider_turns VALUES ('c','c','2026-10-27T12:00:00Z','2026-10-27T12:10:00Z','{}','turn')`)
	execSQL(t, history, `PRAGMA journal_mode=WAL;
		CREATE TABLE executions (id,provider,session_id,started_ms,completed_ms,input_tokens,output_tokens,partial,source);
		CREATE TABLE usage_events (provider,session_id,at_ms,input_tokens,output_tokens);
		INSERT INTO usage_events VALUES ('codex','s',1793102400000,100,10)`)
	return NewActivity(t3, history)
}

func latestReport(t *testing.T, a *Activity) Report {
	t.Helper()
	report, err := a.latest()
	if err != nil {
		t.Fatal(err)
	}
	return report
}

var fixtureNow = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)

func TestRefreshWithUnchangedSourcesKeepsTheReport(t *testing.T) {
	a := activityFixture(t)
	a.refresh(fixtureNow)
	a.refresh(fixtureNow.Add(time.Minute))
	if report := latestReport(t, a); !report.GeneratedAt.Equal(fixtureNow) {
		t.Fatalf("recomputed without a source change: %v", report.GeneratedAt)
	}
}

func TestRefreshReflectsAnImportedRow(t *testing.T) {
	a := activityFixture(t)
	a.refresh(fixtureNow)
	execSQL(t, a.historyPath, `INSERT INTO usage_events VALUES ('codex','s',1793102400000,1000,0)`)
	a.refresh(fixtureNow.Add(time.Minute))
	if days := latestReport(t, a).Providers[1].Days; len(days) != 1 || days[0].Tokens != 1110 {
		t.Fatalf("imported row missing: %+v", days)
	}
}

func TestRefreshAdvancesTodayAtSaoPauloMidnight(t *testing.T) {
	a := activityFixture(t)
	zone, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	a.refresh(time.Date(2026, 10, 27, 23, 59, 30, 0, zone))
	a.refresh(time.Date(2026, 10, 28, 0, 0, 30, 0, zone))
	if today := latestReport(t, a).Today; today != "2026-10-28" {
		t.Fatalf("today after midnight: %s", today)
	}
}

func TestFailedRefreshKeepsThePreviousReport(t *testing.T) {
	a := activityFixture(t)
	previous := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(previous) })
	hook := logtest.NewGlobal()
	a.refresh(fixtureNow)
	execSQL(t, a.t3Path, `INSERT INTO orchestration_v2_projection_provider_turns VALUES ('c','c','not a time',NULL,'{}','broken')`)
	a.refresh(fixtureNow.Add(time.Minute))
	if report := latestReport(t, a); !report.GeneratedAt.Equal(fixtureNow) {
		t.Fatalf("failure replaced the report: %v", report.GeneratedAt)
	}
	if warnings := len(hook.AllEntries()); warnings != 1 {
		t.Fatalf("one failing input logged %d times", warnings)
	}
}

// A mode change keeps every file's size and modification time, so the sources
// stamp the same before and after the failure.
func TestFailedRefreshIsRetriedWithoutASourceChange(t *testing.T) {
	a := activityFixture(t)
	if err := os.Chmod(a.historyPath, 0); err != nil {
		t.Fatal(err)
	}
	a.refresh(fixtureNow)
	if _, err := a.latest(); err == nil {
		t.Fatal("read an unreadable history")
	}
	if err := os.Chmod(a.historyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	a.refresh(fixtureNow.Add(time.Minute))
	if report := latestReport(t, a); !report.HistoryImported {
		t.Fatalf("report after the history became readable: %+v", report)
	}
}

func TestReportSurvivesACanceledFirstRequest(t *testing.T) {
	a := activityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Report(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
	report, err := a.Report(context.Background())
	if err != nil || !report.HistoryImported {
		t.Fatalf("report after a canceled request: %+v, %v", report, err)
	}
	missing := NewActivity(filepath.Join(t.TempDir(), "statev2.sqlite"), filepath.Join(t.TempDir(), "performance-history.sqlite"))
	if _, err = missing.Report(context.Background()); !os.IsNotExist(err) {
		t.Fatalf("missing sources: %v", err)
	}
}
