"""Behavior checks for recovered history using synthetic source files only."""
import importlib.util
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path

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
