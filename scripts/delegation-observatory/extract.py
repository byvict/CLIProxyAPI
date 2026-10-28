"""Allowlisted, read-only T3 projection census. No transcript text is exported."""

import collections
import datetime as dt
import json
import math
import re
import sqlite3

from storage import digest, dump, jsonl, timestamp

P = "orchestration_v2_projection_"


def family(model):
    if not model:
        return "unknown"
    for prefix, name in [
        ("gpt", "gpt"),
        ("claude", "claude"),
        ("grok", "grok"),
        ("muse", "muse"),
    ]:
        if model.lower().startswith(prefix):
            return name
    return "unknown"


def identity(run=None, model=None, provider=None, driver=None, basis="unknown"):
    sel = (run or {}).get("selection") or {}
    m = sel.get("model") or model
    if any(
        value is not None and not isinstance(value, str)
        for value in [m, sel.get("instanceId"), provider, driver]
    ):
        raise ValueError("Model identities must contain strings")
    opts = sel.get("options")
    if opts is not None and (
        not isinstance(opts, list)
        or any(
            not isinstance(option, dict)
            or type(option.get("value")) not in (str, bool, int, float)
            or (
                type(option.get("value")) is float
                and not math.isfinite(option["value"])
            )
            for option in opts
        )
    ):
        raise ValueError("Model settings must contain scalar option values")
    safe = (
        None
        if opts is None
        else [
            {key: value for key, value in o.items() if key in ("id", "value")}
            for o in opts
            if o.get("id")
            in [
                "reasoningEffort",
                "reasoning_effort",
                "effort",
                "contextWindow",
                "serviceTier",
                "thinkingLevel",
                "thinkingBudget",
                "maxThinkingTokens",
                "temperature",
                "fastMode",
            ]
        ]
    )
    return {
        "run_id": (run or {}).get("run_id"),
        "provider_instance_id": (run or {}).get("provider_instance_id") or provider,
        "harness": (run or {}).get("provider") or driver,
        "model": m,
        "family": family(m),
        "settings": safe,
        "basis": basis,
        "settings_unknown": opts is None,
    }


def active_run(runs, when):
    candidates = [
        r
        for r in runs
        if timestamp(r.get("started_at") or r["requested_at"]) <= timestamp(when)
        and (
            r.get("completed_at") is None
            or timestamp(when) < timestamp(r["completed_at"])
        )
    ]
    return candidates[0] if len(candidates) == 1 else None


def role(title):
    s = (title or "").lower()
    for r, rx in [
        ("review", "interrogat|review|thermo|adversar|sicko|no.comments|audit|judge"),
        (
            "verification",
            "verif|proof|prove|qualif|release|benchmark|measurement|measure|test|perf",
        ),
        ("design", "architect|design|plan"),
        ("research", "research|discover|investig|explor|diagnos|analysis|reconcil"),
        ("implementation", "fix|implement|correct|repair|build|owner|migrat|refactor"),
    ]:
        if re.search(rx, s):
            return r
    return "unknown"


def usage_sum(tr, scope, only_unambiguous=False):
    us = [
        x["usage"]
        for x in tr
        if x["usage"]
        and x["usage"].get("usageScope") == scope
        and (
            not only_unambiguous
            or (
                x["usage"].get("hasSubagents") is False
                and x["usage"].get("usageStatus") in ["complete", "partial"]
            )
        )
    ]
    return {
        k: (
            sum(u[k] for u in us if isinstance(u.get(k), (int, float)))
            if any(isinstance(u.get(k), (int, float)) for u in us)
            else None
        )
        for k in [
            "inputTokens",
            "outputTokens",
            "cachedInputTokens",
            "cacheCreationTokens",
            "reasoningTokens",
        ]
    }


def extract(out, source, since, until, audit_roots):
    START, END = since, until
    URI = source.as_uri() + "?mode=ro"
    c = sqlite3.connect(URI, uri=True)
    try:
        c.row_factory = sqlite3.Row
        c.execute("PRAGMA query_only=ON")
        c.execute("BEGIN")
        c.execute(f"SELECT count(*) FROM {P}subagents").fetchone()
        asof = dt.datetime.now(dt.timezone.utc).isoformat()
        file_identity = {
            key: getattr(source.stat(), key) for key in ("st_dev", "st_ino")
        }
        schema = {
            t: [dict(r) for r in c.execute("PRAGMA table_info(" + P + t + ")")]
            for t in [
                "subagents",
                "runs",
                "threads",
                "nodes",
                "provider_turns",
                "turn_items",
            ]
        }
        threads = {
            r["thread_id"]: dict(r)
            for r in c.execute(
                f"SELECT thread_id,project_id,created_at,updated_at,deleted_at,archived_at,json_extract(payload_json,'$.lineage') lineage,json_extract(payload_json,'$.historyOrigin') history_origin,json_extract(payload_json,'$.forkedFrom') forked_from FROM {P}threads"
            )
        }
        for t in threads.values():
            for k in ["lineage", "forked_from"]:
                t[k] = json.loads(t[k]) if t[k] else None
                if isinstance(t[k], dict):
                    t[k] = {
                        key: value
                        for key, value in t[k].items()
                        if key
                        in (
                            "parentThreadId",
                            "sourceThreadId",
                            "sourceRunId",
                            "sourceNodeId",
                        )
                    }
                    if any(
                        value is not None and not isinstance(value, str)
                        for value in t[k].values()
                    ):
                        raise ValueError("Lineage references must be strings")
        runs = {
            r["run_id"]: dict(r)
            for r in c.execute(
                f"SELECT run_id,thread_id,ordinal,provider,provider_instance_id,status,requested_at,completed_at,json_extract(payload_json,'$.startedAt') started_at,json_extract(payload_json,'$.modelSelection') selection FROM {P}runs"
            )
        }
        bythread = collections.defaultdict(list)
        for r in runs.values():
            r["selection"] = json.loads(r["selection"]) if r["selection"] else None
            if r["selection"]:
                safe = identity(r)["settings"]
                r["selection"] = {
                    k: v
                    for (k, v) in r["selection"].items()
                    if k in ["model", "instanceId"]
                } | ({"options": safe} if safe is not None else {})
            bythread[r["thread_id"]].append(r)
        for rr in bythread.values():
            rr.sort(key=lambda x: (x["requested_at"], x["run_id"]))
        raw = [
            dict(r)
            for r in c.execute(
                f"SELECT subagent_id,thread_id,run_id,parent_node_id,child_thread_id,origin,status,started_at,completed_at,updated_at,provider,driver,provider_instance_id,json_extract(payload_json,'$.title') title,json_extract(payload_json,'$.model') model,json_extract(payload_json,'$.nativeTaskRef') native_task_ref FROM {P}subagents WHERE julianday(started_at)>=julianday(?) AND julianday(started_at)<=julianday(?) ORDER BY started_at,subagent_id",
                (START, END),
            )
        ]
        nodes = {
            r["node_id"]: dict(r)
            for r in c.execute(
                f"SELECT node_id,thread_id,run_id,parent_node_id,root_node_id,kind FROM {P}nodes WHERE node_id IN (SELECT parent_node_id FROM {P}subagents WHERE julianday(started_at)>=julianday(?) AND julianday(started_at)<=julianday(?))",
                (START, END),
            )
        }
        timeline = {
            r["subagent_id"]: dict(r)
            for r in c.execute(
                f"SELECT turn_item_id,thread_id,run_id,parent_item_id,json_extract(payload_json,'$.subagentId') subagent_id FROM {P}turn_items WHERE type='subagent'"
            )
        }
        turns = collections.defaultdict(list)
        for row in c.execute(
            f"SELECT p.provider_turn_id,p.thread_id,p.node_id,p.run_attempt_id,p.status,p.started_at,p.completed_at,json_extract(p.payload_json,'$.turnTokenUsage') usage FROM {P}provider_turns p WHERE p.thread_id IN (SELECT DISTINCT child_thread_id FROM {P}subagents WHERE julianday(started_at)>=julianday(?) AND julianday(started_at)<=julianday(?))",
            (START, END),
        ):
            r = dict(row)
            r["usage"] = json.loads(r["usage"]) if r["usage"] else None
            if isinstance(r["usage"], dict):
                r["usage"] = {
                    key: value
                    for key, value in r["usage"].items()
                    if key
                    in (
                        "usageScope",
                        "usageStatus",
                        "hasSubagents",
                        "inputTokens",
                        "outputTokens",
                        "cachedInputTokens",
                        "cacheCreationTokens",
                        "reasoningTokens",
                    )
                }
                for key, value in r["usage"].items():
                    if value is None:
                        continue
                    if key in ("usageScope", "usageStatus"):
                        valid = isinstance(value, str)
                    elif key == "hasSubagents":
                        valid = type(value) is bool
                    else:
                        valid = (
                            type(value) in (int, float)
                            and math.isfinite(value)
                            and value >= 0
                        )
                    if not valid:
                        raise ValueError(
                            "Telemetry fields must contain typed scalar values"
                        )
            turns[r["thread_id"]].append(r)
        meta = {
            "as_of": asof,
            "transaction": "BEGIN; query_only=ON; mode=ro; single connection",
            "window": {"start_inclusive": START, "end_inclusive": END},
            "source_file": str(source),
            "audit_root_ids": audit_roots,
            "source_file_identity": file_identity,
            "source_file_stat": {
                k: getattr(source.stat(), k) for k in ["st_size", "st_mtime_ns"]
            },
            "schema_sha256": digest(schema),
            "projection_thread_count": len(threads),
            "projection_run_count": len(runs),
            "cohort_rows": len(raw),
            "cohort_min_started_at": min((r["started_at"] for r in raw), default=None),
            "cohort_max_started_at": max((r["started_at"] for r in raw), default=None),
            "null_start_rows": c.execute(
                f"SELECT count(*) FROM {P}subagents WHERE started_at IS NULL"
            ).fetchone()[0],
            "timeline_subagent_rows": len(timeline),
            "cohort_missing_timeline_ids": sum(
                r["subagent_id"] not in timeline for r in raw
            ),
            "archived_threads": sum(
                t["archived_at"] is not None for t in threads.values()
            ),
            "deleted_threads": sum(
                t["deleted_at"] is not None for t in threads.values()
            ),
            "history_origins": dict(
                collections.Counter(
                    t["history_origin"] or "local_or_unspecified"
                    for t in threads.values()
                )
            ),
        }
        if file_identity != {
            key: getattr(source.stat(), key) for key in ("st_dev", "st_ino")
        }:
            raise ValueError("Source database was replaced during the read transaction")
    finally:
        c.rollback()
        c.close()

    def ancestry(tid):
        path = []
        seen = set()
        while tid and tid not in seen:
            seen.add(tid)
            path.append(tid)
            t = threads.get(tid)
            if t is None:
                return (path, "missing_thread")
            parent = (t.get("lineage") or {}).get("parentThreadId")
            if not parent:
                return (path, "resolved")
            tid = parent
        return (path, "cycle")

    groups = collections.defaultdict(list)
    for r in raw:
        groups[r["child_thread_id"] or r["subagent_id"]].append(r)
    records = []
    evidence = []
    for r in raw:
        sid = r["subagent_id"]
        tid = r["thread_id"]
        child = r["child_thread_id"]
        when = r["started_at"]
        path, astatus = ancestry(tid)
        root = path[-1]
        dr = runs.get(r["run_id"])
        rn = dr if root == tid else active_run(bythread[root], when)
        siblings = groups[child or sid]
        idx = siblings.index(r)
        next_start = (
            siblings[idx + 1]["started_at"] if idx + 1 < len(siblings) else None
        )
        child_runs = [
            x
            for x in bythread[child]
            if timestamp(x["requested_at"]) >= timestamp(when)
            and (
                next_start is None
                or timestamp(x["requested_at"]) < timestamp(next_start)
            )
        ]
        if not child_runs:
            candidate = active_run(bythread[child], when)
            child_runs = (
                [candidate]
                if candidate
                and (
                    next_start is None
                    or timestamp(candidate["requested_at"]) < timestamp(next_start)
                )
                else []
            )
        cr = child_runs[0] if child_runs else None
        own = identity(
            cr,
            model=r["model"],
            provider=r["provider_instance_id"],
            driver=r["driver"],
            basis=(
                "first_child_run"
                if cr
                else "subagent_model_projection" if r["model"] else "unknown"
            ),
        )
        dri = identity(dr, basis="dispatch_run_id" if dr else "unknown")
        ri = identity(
            rn,
            basis=(
                "dispatch_run_id"
                if root == tid and rn
                else (
                    "unique_root_active_run"
                    if rn
                    else "unknown_or_ambiguous_root_active_run"
                )
            ),
        )
        tr = [
            x
            for x in turns[child]
            if x["started_at"]
            and timestamp(x["started_at"]) >= timestamp(when)
            and (not next_start or timestamp(x["started_at"]) < timestamp(next_start))
            and (
                not r["completed_at"]
                or timestamp(x["started_at"]) <= timestamp(r["completed_at"])
            )
        ]

        scope_counts = dict(
            collections.Counter(
                (x["usage"] or {}).get("usageScope") or "unknown" for x in tr
            )
        )
        usage = {
            "provider_turn_count": len(tr),
            "scope_counts": scope_counts,
            "usage_status_counts": dict(
                collections.Counter(
                    (x["usage"] or {}).get("usageStatus") or "unknown" for x in tr
                )
            ),
            "has_subagents_turns": sum(
                (x["usage"] or {}).get("hasSubagents") is True for x in tr
            ),
            "own": usage_sum(tr, "main_agent", True),
            "reported_main_agent": usage_sum(tr, "main_agent"),
            "descendant_inclusive": None,
            "unknown_scope": usage_sum(tr, "unknown"),
            "source_provider_turn_ids": [x["provider_turn_id"] for x in tr],
            "availability": "no_telemetry" if not tr else "scoped_telemetry",
            "note": "Own sums only reported main_agent turns with hasSubagents=false and complete/partial telemetry; ambiguous subagent-inclusive turns stay reported only. Never sum tokenUsage context counters. Null is unknown, never zero.",
        }
        item = {
            "schema_version": "t3-observatory/1.0",
            "dispatch_id": sid,
            "revision": r["updated_at"],
            "source_record_sha256": digest(
                {k: v for (k, v) in r.items() if k != "title"}
            ),
            "environment": "local",
            "project_id": (threads.get(tid) or {}).get("project_id"),
            "root_thread_id": root,
            "dispatcher_thread_id": tid,
            "child_thread_id": child,
            "dispatcher_run_id": r["run_id"],
            "parent_node_id": r["parent_node_id"],
            "timeline_item_id": (timeline.get(sid) or {}).get("turn_item_id"),
            "ancestry": path,
            "ancestry_status": astatus,
            "root": ri,
            "dispatcher": dri,
            "child": own,
            "requested_child_model": r["model"],
            "origin": r["origin"],
            "history_origin": (threads.get(tid) or {}).get("history_origin"),
            "child_history_origin": (threads.get(child) or {}).get("history_origin"),
            "status": r["status"],
            "started_at": when,
            "completed_at": r["completed_at"],
            "status_as_of": asof,
            "role": role(r["title"]),
            "role_basis": "title_regex; coarse, unreviewed unless attribution record overrides",
            "skill": None,
            "panel_id": None,
            "round": None,
            "source_revision": None,
            "source_scope": None,
            "task_id": child or sid,
            "observed_attempt_index_in_window": idx + 1,
            "dispatch_kind": (
                "continuation"
                if "%3Athread-send%3A" in sid
                else "native" if r["origin"] == "provider_native" else "new_task"
            ),
            "retry_of": None,
            "retry_status": "unknown; continuations are not automatically retries",
            "child_run_ids": [x["run_id"] for x in child_runs],
            "usage": usage,
            "duration_ms": (
                round(
                    (
                        dt.datetime.fromisoformat(
                            r["completed_at"].replace("Z", "+00:00")
                        )
                        - dt.datetime.fromisoformat(when.replace("Z", "+00:00"))
                    ).total_seconds()
                    * 1000
                )
                if r["completed_at"]
                else None
            ),
            "audit_overhead": any(a in path or child == a for a in audit_roots),
            "evidence_ids": ["dispatch:" + sid],
        }
        for label, ident in [("root", ri), ("dispatcher", dri)]:
            item[label + "_relationship"] = (
                "unknown"
                if "unknown" in [ident["family"], own["family"]]
                else (
                    "same_family"
                    if ident["family"] == own["family"]
                    else "cross_family"
                )
            )
        records.append(item)
        evidence.append(
            {
                "evidence_id": "dispatch:" + sid,
                "kind": "sqlite_projection",
                "table": P + "subagents",
                "source_id": sid,
                "source_item_id": (timeline.get(sid) or {}).get("turn_item_id"),
                "thread_id": tid,
                "run_id": r["run_id"],
                "parent_node_id": r["parent_node_id"],
                "child_thread_id": child,
                "started_at": when,
                "revision": r["updated_at"],
                "allowlist_sha256": item["source_record_sha256"],
                "observed_at": asof,
            }
        )
    jsonl(
        out / "usage-source.jsonl",
        [
            x
            for tid in sorted(t for t in turns if t is not None)
            for x in sorted(turns[tid], key=lambda y: y["provider_turn_id"])
        ],
    )
    jsonl(out / "dispatches.jsonl", records)
    jsonl(out / "dispatch-evidence.jsonl", evidence)
    dump(out / "source-metadata.json", meta)
    dump(out / "schema-source.json", schema)
    dump(
        out / "lineage-source.json",
        {
            "threads": threads,
            "runs": runs,
            "parent_nodes": {
                r["parent_node_id"]: nodes.get(r["parent_node_id"]) for r in raw
            },
        },
    )
    return meta
