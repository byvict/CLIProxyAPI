"""Behavior checks for recovered history using synthetic source files only."""
import argparse
import contextlib
import datetime as dt
import importlib.util
import io
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path
from zoneinfo import ZoneInfo

spec = importlib.util.spec_from_file_location("history", Path(__file__).with_name("import-performance-history.py"))
history = importlib.util.module_from_spec(spec)
spec.loader.exec_module(history)


class HistoryTests(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(":memory:")
        self.db.executescript(history.SCHEMA)
        self.importer = history.Importer(self.db)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.addCleanup(self.db.close)

    def file(self, name, data):
        path = Path(self.temp.name) / name
        path.write_text(data)
        return path

    def test_cache_versions_and_transcript_copies_count_usage_once(self):
        row = [1786037567045, 0, 0, 2, 30040, 14014, 320, 100, "msg:req", None, 0]
        for version in [4, 9]:
            path = self.file(f"v{version}.json", json.dumps({"version": version, "models": ["claude"], "sessions": ["session"],
                "files": {"deleted": {"p": "claude", "r": [row], "t": []}}}))
            self.importer.cache(path)
        record = {"type": "assistant", "timestamp": "2026-08-06T17:32:47.045Z", "sessionId": "session",
            "requestId": "req", "message": {"id": "msg", "model": "claude", "usage": {"input_tokens": 2,
            "cache_read_input_tokens": 30040, "cache_creation_input_tokens": 14014, "output_tokens": 320}}}
        path = self.file("claude.jsonl", json.dumps(record) + "\n" + json.dumps(record) + "\n")
        before = path.read_bytes()
        self.importer.transcript(path, "claude")
        self.importer.transcript(path, "claude")
        self.assertEqual(self.db.execute("SELECT count(*),sum(input_tokens+output_tokens) FROM usage_events").fetchone(), (1, 44376))
        self.assertEqual(self.db.execute("SELECT count(*) FROM executions").fetchone()[0], 0)
        self.assertEqual(path.read_bytes(), before)

    def test_codex_repeated_cumulative_usage_and_fork_copies_are_not_new_usage(self):
        state = {"session": "s", "model": "sol", "previous": None, "signature": None, "fork": None}
        runs = {}
        usage = {"input_tokens": 100, "output_tokens": 20, "cached_input_tokens": 50}
        record = {"timestamp": "2026-10-27T12:00:00Z", "type": "event_msg", "payload": {"type": "token_count", "info": {"last_token_usage": usage, "total_token_usage": usage}}}
        for _ in range(2):
            self.importer.codex(record, state, runs, Path("synthetic"))
        record["timestamp"] = "2026-10-27T12:00:01Z"
        self.importer.codex(record, state, runs, Path("synthetic"))
        self.assertEqual(self.db.execute("SELECT count(*),sum(input_tokens+output_tokens) FROM usage_events").fetchone(), (1, 120))
        state["fork"] = history.timestamp(record["timestamp"])
        self.importer.codex(record, state, runs, Path("synthetic"))
        self.assertEqual(self.db.execute("SELECT count(*) FROM usage_events").fetchone()[0], 1)

    def test_grok_model_breakdown_is_not_added_to_top_level_usage(self):
        record = {"timestamp": 1793102400, "params": {"sessionId": "s", "update": {"sessionUpdate": "turn_completed",
            "prompt_id": "p", "usage": {"inputTokens": 1000, "outputTokens": 50,
            "modelUsage": {"grok": {"inputTokens": 1000, "outputTokens": 50}}}}}}
        path = self.file("updates.jsonl", json.dumps(record) + "\n")
        self.importer.transcript(path, "grok")
        self.assertEqual(self.db.execute("SELECT sum(input_tokens+output_tokens) FROM usage_events").fetchone()[0], 1050)
        self.assertEqual(self.db.execute("SELECT count(*),completed_ms FROM executions").fetchone(), (1, None))

    def test_codex_mismatched_cumulative_counters_are_excluded(self):
        state = {"session": "s", "model": "sol", "previous": None, "signature": None, "fork": None}
        for at, cumulative in [(0, 100), (1, 150)]:
            self.importer.codex({"timestamp": f"2026-10-27T12:00:0{at}Z", "type": "event_msg", "payload": {"type": "token_count",
                "info": {"last_token_usage": {"input_tokens": 100, "output_tokens": 20},
                         "total_token_usage": {"input_tokens": cumulative, "output_tokens": 20}}}}, state, {}, Path("synthetic"))
        self.assertEqual(self.db.execute("SELECT count(*),sum(input_tokens+output_tokens) FROM usage_events").fetchone(), (1, 120))
        self.assertEqual(self.importer.stats["invalidCodexUsage"], 1)

    def test_muse_request_records_remain_distinct_from_completed_runs(self):
        def record(at, event):
            return {"id": "event", "recorded_at": at, "stream": {"kind": "session", "id": "s"},
                    "payload": {"kind": "run", "run_id": "r", "source_run_record_id": "original", "event": event}}
        records = [record(1793102400000000, {"kind": "model_request_configured"}),
                   record(1793102401000000, {"kind": "model_completed", "usage": {"input_tokens": 100, "output_tokens": 20, "cached_tokens": 50, "reasoning_tokens": 10}}),
                   record(1793102402000000, {"kind": "terminal"})]
        self.importer.transcript(self.file("session.jsonl", "\n".join(map(json.dumps, records))), "muse")
        self.assertEqual(self.db.execute("SELECT count(*),sum(input_tokens+output_tokens) FROM usage_events").fetchone(), (1, 120))
        self.assertEqual(self.db.execute("SELECT count(*),completed_ms-started_ms FROM executions").fetchone(), (1, 2000))


class RunTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name).resolve()
        self.home = self.root / "home"
        self.destination = self.root / "runtime" / "performance-history.sqlite"

    def transcript(self, name):
        record = {"type": "assistant", "timestamp": "2026-10-28T12:00:00Z", "sessionId": name, "requestId": name,
                  "message": {"id": name, "model": "claude", "usage": {"input_tokens": 10, "output_tokens": 5}}}
        path = self.home / ".claude/projects/p" / (name + ".jsonl")
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(record) + "\n")

    def run_import(self):
        receipt = self.root / "receipt.json"
        args = argparse.Namespace(home=self.home, destination=self.destination, receipt=receipt,
                                  dry_run=False, cache_only=False)
        with contextlib.redirect_stdout(io.StringIO()):
            history.run(args)
        return json.loads(receipt.read_text())

    def t3_database(self, *payloads):
        path = self.home / ".t3/userdata/statev2.sqlite"
        path.parent.mkdir(parents=True, exist_ok=True)
        with contextlib.closing(sqlite3.connect(path)) as db, db:
            db.executescript("""CREATE TABLE orchestration_v2_projection_provider_threads
                    (provider_thread_id, thread_id, provider, payload_json);
                CREATE TABLE orchestration_v2_projection_provider_turns
                    (provider_thread_id, thread_id, started_at, completed_at, payload_json, provider_turn_id);
                INSERT INTO orchestration_v2_projection_provider_threads VALUES ('p', 't', 'claudeAgent', '{}');""")
            db.executemany("""INSERT INTO orchestration_v2_projection_provider_turns
                VALUES ('p', 't', '2026-10-28T12:00:00Z', '2026-10-28T12:01:00Z', ?, ?)""",
                           [(payload, f"turn{index}") for index, payload in enumerate(payloads)])
        return path

    def rows(self, path=None):
        with contextlib.closing(sqlite3.connect(path or self.destination)) as db:
            return (db.execute("SELECT count(*) FROM usage_events").fetchone()[0],
                    db.execute("SELECT count(*) FROM executions").fetchone()[0],
                    db.execute("SELECT * FROM imports ORDER BY source").fetchall())

    def test_import_leaves_history_in_wal_mode(self):
        self.run_import()
        with contextlib.closing(sqlite3.connect(self.destination)) as db:
            self.assertEqual(db.execute("PRAGMA journal_mode").fetchone()[0], "wal")

    def test_readers_see_committed_rows_while_an_import_holds_the_write_lock(self):
        self.transcript("a")
        self.run_import()
        writer = sqlite3.connect(self.destination, isolation_level=None)
        self.addCleanup(writer.close)
        writer.execute("BEGIN EXCLUSIVE")
        writer.execute("INSERT INTO usage_events VALUES ('uncommitted','claude','s',0,1,1,'test')")
        reader = sqlite3.connect(self.destination.as_uri() + "?mode=ro", uri=True, timeout=0)
        self.addCleanup(reader.close)
        self.assertEqual(reader.execute("SELECT count(*) FROM usage_events").fetchone()[0], 1)

    def test_daily_backup_keeps_the_state_before_the_first_import_and_three_dated_copies(self):
        self.transcript("a")
        self.assertIsNone(self.run_import()["backup"])
        parent = self.destination.parent
        historical = parent / "performance-history.before-20261027202000.sqlite"
        historical.write_text("historical")
        for day in ("20260101", "20260102", "20260103", "20260104"):
            (parent / f"performance-history.before-{day}.sqlite").write_text(day)
        today = parent / dt.datetime.now(ZoneInfo("America/Sao_Paulo")).strftime("performance-history.before-%Y%m%d.sqlite")
        self.transcript("b")
        self.assertEqual(self.run_import()["backup"], str(today))
        self.assertEqual(self.run_import()["backup"], str(today))
        self.assertEqual(sorted(path.name for path in parent.glob("performance-history.before-*")),
                         sorted([historical.name, "performance-history.before-20260103.sqlite",
                                 "performance-history.before-20260104.sqlite", today.name]))
        self.assertEqual(historical.read_text(), "historical")
        self.assertEqual(self.rows(today)[0], 1)
        self.assertEqual(self.rows()[0], 2)

    def test_failed_import_leaves_history_unchanged(self):
        self.transcript("a")
        self.run_import()
        before = self.rows()
        self.transcript("b")
        corrupt = self.home / ".codex/sessions/rollout.jsonl.zst"
        corrupt.parent.mkdir(parents=True)
        corrupt.write_bytes(b"not zstd")
        with self.assertRaisesRegex(RuntimeError, "Could not decompress"):
            self.run_import()
        self.assertEqual(self.rows(), before)

    def test_unreadable_t3_databases_are_skipped_and_the_import_completes(self):
        self.transcript("a")
        locked = self.home / ".t3/userdata/statev2.sqlite"
        corrupt = self.home / ".t3/backup-userdata/state.sqlite"
        for path in (locked, corrupt):
            path.parent.mkdir(parents=True)
        locked.touch(mode=0)
        corrupt.write_bytes(b"not a database" * 512)
        with contextlib.redirect_stderr(io.StringIO()) as stderr:
            receipt = self.run_import()
        self.assertEqual(self.rows()[0], 1)
        self.assertEqual(receipt["skippedSources"], [
            {"source": str(corrupt), "error": "file is not a database"},
            {"source": str(locked), "error": "unable to open database file"}])
        self.assertIn(str(locked), stderr.getvalue())

    def test_t3_database_failing_mid_read_is_skipped_without_partial_rows(self):
        state = self.t3_database("{}", "{}", "{broken")
        with contextlib.redirect_stderr(io.StringIO()):
            receipt = self.run_import()
        self.assertEqual(receipt["skippedSources"], [{"source": str(state), "error": "malformed JSON"}])
        self.assertEqual(self.rows()[1], 0)

    def test_t3_rows_that_fail_to_parse_skip_the_source_without_partial_rows(self):
        self.transcript("a")
        state = self.home / ".t3/userdata/state.sqlite"
        state.parent.mkdir(parents=True, exist_ok=True)
        with contextlib.closing(sqlite3.connect(state)) as db, db:
            db.executescript("""CREATE TABLE projection_turns (thread_id, turn_id, row_id, started_at, completed_at);
                CREATE TABLE projection_thread_sessions (thread_id, provider_name);
                CREATE TABLE provider_session_runtime (thread_id, resume_cursor_json);
                INSERT INTO projection_thread_sessions VALUES ('a', 'claude'), ('b', 'claude'), ('c', 'claude');
                INSERT INTO projection_turns VALUES ('a', 't', 1, '2026-10-28T12:00:00Z', NULL),
                    ('b', 't', 2, '2026-10-28T12:00:00Z', NULL), ('c', 't', 3, '2026-10-28T12:00:00Z', NULL);
                INSERT INTO provider_session_runtime VALUES ('a', '{"sessionId": "a"}'),
                    ('b', '{"sessionId": "b"}'), ('c', '{broken');""")
        with contextlib.redirect_stderr(io.StringIO()):
            receipt = self.run_import()
        self.assertEqual([skip["source"] for skip in receipt["skippedSources"]], [str(state)])
        self.assertEqual(self.rows()[:2], (1, 0))

    def test_a_locked_history_fails_the_run_instead_of_skipping_t3(self):
        self.t3_database("{}")
        self.run_import()
        self.assertEqual(self.rows()[1], 1)
        writer = sqlite3.connect(self.destination, isolation_level=None)
        self.addCleanup(writer.close)
        writer.execute("BEGIN IMMEDIATE")
        with contextlib.redirect_stderr(io.StringIO()) as stderr, \
                self.assertRaisesRegex(sqlite3.OperationalError, "database is locked"):
            self.run_import()
        self.assertNotIn("Skipped", stderr.getvalue())


class CodexBarTests(unittest.TestCase):
    def test_unknown_accounts_stay_separate_and_percentages_use_recorded_time(self):
        spec = importlib.util.spec_from_file_location("codexbar", Path(__file__).with_name("import-codexbar-history.py"))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as root:
            data = {"version": 1, "accounts": {"codex:v1:provider-account:known": [{"name": "weekly", "windowMinutes": 10080,
                "entries": [{"capturedAt": "2026-08-19T21:59:10Z", "resetsAt": "2026-08-20T12:24:06Z", "usedPercent": 67}]}],
                "old": [{"name": "session", "windowMinutes": 300, "entries": [{"capturedAt": "2026-08-19T21:59:10Z", "usedPercent": 25}]}]}}
            Path(root, "codex.json").write_text(json.dumps(data))
            rows, accounts = module.observations(Path(root), {"codex:v1:provider-account:known": "current"})
            self.assertEqual(rows[0][0], "current")
            self.assertEqual(rows[0][5], 33)
            self.assertEqual(rows[0][4], 1787176750000)
            self.assertTrue(rows[1][0].startswith("codexbar:"))
            self.assertFalse(accounts[1]["matched"])


if __name__ == "__main__":
    unittest.main()
