#!/usr/bin/env python3
"""Recover observed CLI usage and executions without modifying source stores."""

import argparse
import contextlib
import datetime as dt
import hashlib
import json
import math
import os
import sqlite3
import subprocess
import sys
from collections import Counter
from pathlib import Path
from zoneinfo import ZoneInfo

PROVIDERS = {"claudeAgent": "claude", "claude": "claude", "codex": "codex",
             "grok": "grok", "muse": "meta", "meta": "meta"}
BACKUPS_KEPT = 3
SCHEMA = """CREATE TABLE IF NOT EXISTS usage_events (
 id TEXT PRIMARY KEY, provider TEXT NOT NULL, session_id TEXT NOT NULL,
 at_ms INTEGER NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 source TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS executions (
 id TEXT PRIMARY KEY, provider TEXT NOT NULL, session_id TEXT NOT NULL,
 started_ms INTEGER NOT NULL, completed_ms INTEGER, input_tokens INTEGER,
 output_tokens INTEGER, partial INTEGER NOT NULL, source TEXT NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS usage_time ON usage_events(at_ms);
CREATE INDEX IF NOT EXISTS execution_time ON executions(started_ms);
CREATE TABLE IF NOT EXISTS imports (source TEXT PRIMARY KEY, size INTEGER, mtime_ns INTEGER);
"""


def timestamp(value):
    if isinstance(value, str):
        return int(dt.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp() * 1000)
    if isinstance(value, (int, float)) and math.isfinite(value):
        return int(value * 1000 if value < 1e12 else value)
    return None


def count(value):
    return isinstance(value, int) and not isinstance(value, bool) and value >= 0


def codex_usage(raw):
    if not isinstance(raw, dict) or not count(raw.get("input_tokens")) or not count(raw.get("output_tokens")):
        return None
    usage = {key: raw.get(key, 0) for key in ("input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens")}
    usage["total_tokens"] = raw.get("total_tokens", usage["input_tokens"] + usage["output_tokens"])
    if not all(count(v) for v in usage.values()):
        return None
    if usage["cached_input_tokens"] + usage["cache_write_input_tokens"] > usage["input_tokens"] or usage["reasoning_output_tokens"] > usage["output_tokens"] or usage["total_tokens"] < usage["input_tokens"] + usage["output_tokens"]:
        return None
    return usage


def digest(*parts):
    return hashlib.sha256(json.dumps(parts, separators=(",", ":")).encode()).hexdigest()


def read_t3(path):
    """Return the parsed executions of one T3 database and its turns without identity."""
    executions, without_identity = [], 0
    with contextlib.closing(sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)) as source:
        source.execute("BEGIN")
        tables = {r[0] for r in source.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        if "orchestration_v2_projection_provider_turns" in tables:
            rows = source.execute("""SELECT t.provider_turn_id,p.provider,t.started_at,t.completed_at,
                json_extract(p.payload_json,'$.nativeThreadRef.nativeId'),
                json_extract(t.payload_json,'$.turnTokenUsage')
                FROM orchestration_v2_projection_provider_turns t
                JOIN orchestration_v2_projection_provider_threads p
                ON p.provider_thread_id=t.provider_thread_id AND p.thread_id=t.thread_id""").fetchall()
            for key, provider, start, end, session, usage in rows:
                u = json.loads(usage) if usage else {}
                valid = u.get("usageScope") == "main_agent" and u.get("usageStatus") in ("partial", "complete") and count(u.get("inputTokens")) and count(u.get("outputTokens"))
                executions.append((key, provider, session or key, timestamp(start), timestamp(end), "t3:v2",
                                   u["inputTokens"] if valid else None, u["outputTokens"] if valid else None,
                                   int(u.get("usageStatus") == "partial")))
        elif "projection_turns" in tables:
            rows = source.execute("""SELECT t.thread_id,t.turn_id,t.row_id,s.provider_name,
                t.started_at,t.completed_at,r.resume_cursor_json FROM projection_turns t
                JOIN projection_thread_sessions s ON s.thread_id=t.thread_id
                LEFT JOIN provider_session_runtime r ON r.thread_id=t.thread_id""").fetchall()
            for thread, turn, row, provider, start, end, cursor in rows:
                session = json.loads(cursor).get("sessionId") if cursor else None
                if not session:
                    without_identity += 1
                    continue
                executions.append((f"t3:v1:{thread}:{turn or row}", provider, session,
                                   timestamp(start), timestamp(end), "t3:v1"))
    return executions, without_identity


class Importer:
    def __init__(self, db):
        self.db = db
        self.stats = Counter()
        self.skipped = []

    def usage(self, provider, session, at, inp, out, key, source):
        if provider not in PROVIDERS or not session or at is None or not count(inp) or not count(out):
            self.stats["invalidUsage"] += 1
            return
        if inp + out == 0:
            return
        provider = PROVIDERS[provider]
        identity = digest(provider, key) if key else digest(provider, session, at, inp, out)
        self.db.execute("INSERT OR IGNORE INTO usage_events VALUES (?,?,?,?,?,?,?)",
                        (identity, provider, session, at, inp, out, source))

    def execution(self, key, provider, session, start, end, source, inp=None, out=None, partial=0):
        if provider not in PROVIDERS or not session or start is None:
            return
        self.db.execute("""INSERT INTO executions VALUES (?,?,?,?,?,?,?,?,?)
            ON CONFLICT(id) DO UPDATE SET completed_ms=COALESCE(excluded.completed_ms,completed_ms),
            input_tokens=COALESCE(excluded.input_tokens,input_tokens),
            output_tokens=COALESCE(excluded.output_tokens,output_tokens), partial=excluded.partial""",
            (key, PROVIDERS[provider], session, start, end, inp, out, partial, source))

    def cache(self, path):
        data = json.loads(path.read_text())
        if data.get("version") not in range(4, 10):
            raise ValueError(f"Unsupported cache version: {path}")
        for entry in data["files"].values():
            provider = entry["p"]
            if provider not in PROVIDERS:
                continue
            for row in entry.get("r", []) + entry.get("t", []):
                if len(row) < 11 or any(not count(n) for n in row[3:8]):
                    raise ValueError(f"Invalid cache usage row: {path}")
                if data["models"][row[1]] == "<synthetic>":
                    continue
                # Cache v4-v9 stores disjoint input categories; reasoning is in output.
                self.usage(provider, data["sessions"][row[2]], int(row[0]),
                           sum(row[3:6]), row[6], row[8], str(path))

    def t3(self, path):
        try:
            executions, without_identity = read_t3(path)
        except Exception as error:
            # T3's database belongs to another program. One that cannot be opened or parsed must
            # not stop the other sources, and nothing from it is written. macOS's SQLite cannot
            # open a WAL database read-only after its writer removed -wal and -shm; T3 is closed
            # then, so a later run imports the same turns.
            print(f"Skipped {path}: {error}", file=sys.stderr, flush=True)
            self.skipped.append({"source": str(path), "error": str(error)})
            return
        if without_identity:
            self.stats["legacyExecutionsWithoutIdentity"] += without_identity
        for execution in executions:
            self.execution(*execution)

    def transcript(self, path, provider):
        stat = path.stat()
        previous = self.db.execute("SELECT size,mtime_ns FROM imports WHERE source=?", (str(path),)).fetchone()
        if previous == (stat.st_size, stat.st_mtime_ns):
            self.stats["unchangedFiles"] += 1
            return
        self.stats["parsedFiles"] += 1
        with contextlib.ExitStack() as stack:
            if path.suffix == ".zst":
                process = subprocess.Popen(["zstd", "-dc", str(path)], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
                stream = process.stdout
                stack.callback(stream.close)
            else:
                process = None
                stream = stack.enter_context(path.open("rb"))
            state = {"session": "", "model": "", "previous": None, "signature": None, "fork": None}
            runs = {}
            claude_active = None
            grok_start = None
            markers = {
                "claude": (b'"user"', b'"assistant"', b'"turn_duration"'),
                "codex": (b'"session_meta"', b'"turn_context"', b'"token_count"', b'"task_started"', b'"task_complete"', b'"turn_aborted"'),
                "grok": (b'"turn_completed"', b'"user_message_chunk"'),
                "muse": (b'"model_completed"', b'"automated_review_completed"', b'"model_request_configured"', b'"terminal"'),
            }[provider]
            for line in stream:
                if not any(marker in line for marker in markers):
                    continue
                try:
                    record = json.loads(line)
                except (ValueError, UnicodeDecodeError):
                    self.stats["malformedLines"] += 1
                    continue
                if provider == "claude":
                    session = record.get("sessionId", "")
                    at = timestamp(record.get("timestamp"))
                    message = record.get("message", {})
                    if record.get("type") == "user" and isinstance(message, dict):
                        content = message.get("content")
                        tool = isinstance(content, list) and any(isinstance(c, dict) and c.get("type") == "tool_result" for c in content)
                        if not tool and not record.get("isMeta"):
                            key = "cli:claude:" + record.get("uuid", digest(session, at))
                            self.execution(key, provider, session, at, None, "cli:claude")
                            claude_active = (key, session, at)
                    if record.get("type") == "assistant" and isinstance(message, dict):
                        u = message.get("usage")
                        if u and message.get("model") != "<synthetic>":
                            inp = sum(u.get(k, 0) for k in ("input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"))
                            self.usage(provider, session, at, inp, u.get("output_tokens", 0),
                                       f"{message.get('id', '')}:{record.get('requestId', '')}", str(path))
                        if claude_active and message.get("stop_reason") in ("end_turn", "stop_sequence"):
                            self.execution(claude_active[0], provider, claude_active[1], claude_active[2], at, "cli:claude")
                    if record.get("subtype") == "turn_duration" and claude_active:
                        self.execution(claude_active[0], provider, claude_active[1], claude_active[2], at, "cli:claude")
                elif provider == "codex":
                    self.codex(record, state, runs, path)
                elif provider == "grok":
                    params = record.get("params", {})
                    u = params.get("update", {})
                    session = params.get("sessionId", "")
                    at = timestamp(params.get("_meta", {}).get("agentTimestampMs", record.get("timestamp")))
                    if u.get("sessionUpdate") == "user_message_chunk":
                        grok_start = grok_start or at
                    if u.get("sessionUpdate") == "turn_completed":
                        usage = u.get("usage", {})
                        prompt = u.get("prompt_id") or str(at)
                        self.execution(f"cli:grok:{session}:{prompt}", provider, session, grok_start or at, at if grok_start else None, "cli:grok")
                        grok_start = None
                        for model, part in (usage.get("modelUsage") or {"grok": usage}).items():
                            self.usage(provider, session, at, part.get("inputTokens", 0), part.get("outputTokens", 0),
                                       f"{session}:{prompt}:{model}", str(path))
                else:
                    payload = record.get("payload", {})
                    event = payload.get("event", {})
                    session = record.get("stream", {}).get("id", "")
                    at = record.get("recorded_at", 0) // 1000
                    run = payload.get("run_id")
                    if payload.get("kind") == "run" and run:
                        if event.get("kind") == "model_request_configured":
                            runs.setdefault(run, at)
                            self.execution("cli:muse:" + run, provider, session, runs[run], None, "cli:muse")
                        if event.get("kind") == "terminal" and run in runs:
                            self.execution("cli:muse:" + run, provider, session, runs[run], at, "cli:muse")
                    if event.get("kind") in ("model_completed", "automated_review_completed"):
                        usage = event.get("usage", {})
                        key = json.dumps(["muse", payload.get("source_run_record_id") or record["id"]], separators=(",", ":"))
                        self.usage(provider, session, at, usage.get("input_tokens", 0), usage.get("output_tokens", 0), key, str(path))
            if process and process.wait() != 0:
                raise RuntimeError(f"Could not decompress {path}")
        self.db.execute("INSERT OR REPLACE INTO imports VALUES (?,?,?)", (str(path), stat.st_size, stat.st_mtime_ns))

    def codex(self, record, state, runs, path):
        p = record.get("payload", {})
        typ = record.get("type")
        at = timestamp(record.get("timestamp"))
        if typ == "session_meta" and not state["session"]:
            state["session"] = p.get("id", p.get("session_id", ""))
            source = p.get("source")
            spawned = source.get("subagent", {}).get("thread_spawn", {}) if isinstance(source, dict) else {}
            if p.get("forked_from_id") or spawned.get("parent_thread_id"):
                state["fork"] = at
        if typ == "turn_context":
            state["model"] = p.get("model", "")
        if p.get("type") == "task_started":
            turn = p.get("turn_id")
            if turn:
                runs.setdefault(turn, at)
                self.execution("cli:codex:" + turn, "codex", state["session"], runs[turn], None, "cli:codex")
        if p.get("type") in ("task_complete", "turn_aborted"):
            turn = p.get("turn_id")
            if turn in runs:
                self.execution("cli:codex:" + turn, "codex", state["session"], runs[turn], at, "cli:codex")
        if p.get("type") != "token_count" or not isinstance(p.get("info"), dict):
            return
        info = p["info"]
        last = codex_usage(info.get("last_token_usage"))
        raw_cumulative = info.get("total_token_usage")
        cumulative = codex_usage(raw_cumulative)
        previous = state["previous"]
        signature = json.dumps(last, sort_keys=True)
        if cumulative:
            state["previous"] = cumulative
        if state["fork"] is not None and at is not None:
            if at - state["fork"] < 1000:
                state["fork"] = at
                state["signature"] = signature
                return
            state["fork"] = None
        if not last or not state["model"] or (raw_cumulative is not None and cumulative is None):
            self.stats["invalidCodexUsage"] += 1
            return
        if cumulative == previous and cumulative is not None or cumulative is None and signature == state["signature"]:
            return
        if last["total_tokens"] != last["input_tokens"] + last["output_tokens"]:
            self.stats["invalidCodexUsage"] += 1
            return
        if cumulative is not None:
            restarted = cumulative == last
            reconciled = all(cumulative[k] - previous[k] == last[k] for k in last) if previous else codex_usage({k: cumulative[k] - last[k] for k in last}) is not None
            if not restarted and not reconciled:
                self.stats["invalidCodexUsage"] += 1
                return
        else:
            state["previous"] = {k: (previous[k] if previous else 0) + last[k] for k in last}
        state["signature"] = signature
        self.usage("codex", state["session"], at, last.get("input_tokens"), last.get("output_tokens"), None, str(path))


def daily_backup(db, destination):
    """Copy a non-empty history before the first import of each São Paulo day.
    Older 14-digit per-run copies fall outside the retention pattern."""
    path = destination.with_suffix(dt.datetime.now(ZoneInfo("America/Sao_Paulo")).strftime(".before-%Y%m%d.sqlite"))
    if path.exists():
        return path
    if not destination.stat().st_size:
        return None
    # An interrupted copy must not count as the day's backup.
    partial = path.with_name(path.name + ".partial")
    partial.unlink(missing_ok=True)
    with contextlib.closing(sqlite3.connect(partial)) as copy:
        db.backup(copy)
    partial.replace(path)
    for old in sorted(destination.parent.glob(destination.stem + ".before-" + "[0-9]" * 8 + ".sqlite"))[:-BACKUPS_KEPT]:
        old.unlink()
    return path


def run(args):
    os.umask(0o077)
    destination = args.destination.resolve()
    if not args.dry_run:
        destination.parent.mkdir(parents=True, exist_ok=True)
    backup = None
    with contextlib.closing(sqlite3.connect(":memory:" if args.dry_run else destination)) as db:
        db.execute("PRAGMA busy_timeout=5000")
        if not args.dry_run:
            backup = daily_backup(db, destination)
            # The proxy keeps reading committed rows while an import transaction is open.
            db.execute("PRAGMA journal_mode=WAL")
        db.executescript(SCHEMA)
        importer = Importer(db)
        before = db.execute("SELECT count(*) FROM usage_events").fetchone()[0]
        with db:
            for root in sorted((args.home / ".t3").glob("*userdata*")):
                for path in sorted(root.glob("usage-scan-cache*.json"), key=lambda p: p.stat().st_mtime, reverse=True):
                    importer.cache(path)
                for name in ["statev2.sqlite", "state.sqlite"]:
                    if (root / name).is_file():
                        importer.t3(root / name)
            if not args.cache_only:
                for provider, roots, pattern in [
                    ("claude", [args.home / ".claude/projects", args.home / ".claude-swap-backup"], "*.jsonl"),
                    ("codex", [args.home / ".codex/sessions", args.home / ".codex/archived_sessions", args.home / ".codex/archives"], "*.jsonl*"),
                    ("grok", [args.home / ".grok/sessions"], "updates.jsonl"),
                    ("muse", [args.home / ".local/share/muse/sessions"], "session.jsonl"),
                ]:
                    for root in roots:
                        for path in root.rglob(pattern):
                            if path.is_file() and path.suffix in (".jsonl", ".zst"):
                                importer.transcript(path, provider)
                    print(json.dumps({"scanned": provider, **importer.stats}), flush=True)
        zone = ZoneInfo("America/Sao_Paulo")
        coverage = []
        for provider, records, tokens, first, last in db.execute("""SELECT provider,count(*),
            sum(input_tokens+output_tokens),min(at_ms),max(at_ms) FROM usage_events GROUP BY provider"""):
            coverage.append({"provider": provider, "records": records, "tokens": tokens,
                             "from": dt.datetime.fromtimestamp(first / 1000, zone).isoformat(),
                             "to": dt.datetime.fromtimestamp(last / 1000, zone).isoformat()})
        result = {"dryRun": args.dry_run, "database": str(destination), "backup": str(backup) if backup else None,
                  "newUsageEvents": db.execute("SELECT count(*) FROM usage_events").fetchone()[0] - before,
                  "coverage": coverage, "executionsBySource": dict(db.execute("SELECT source,count(*) FROM executions GROUP BY source")),
                  "skippedSources": importer.skipped, "stats": dict(importer.stats), "integrity": db.execute("PRAGMA quick_check").fetchone()[0]}
        if args.receipt:
            args.receipt.write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--home", type=Path, default=Path.home())
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--receipt", type=Path)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--cache-only", action="store_true")
    run(parser.parse_args())
