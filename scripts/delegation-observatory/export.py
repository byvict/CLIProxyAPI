"""Allowlisted public envelopes and validation before publication."""

import copy
import hashlib
import json
import math

from extract import tier0
from metrics import summarize
from storage import VERSION, digest, publication_file, timestamp

DISPATCH_FIELDS = [
    "dispatch_id",
    "revision",
    "environment",
    "project_id",
    "root_thread_id",
    "dispatcher_thread_id",
    "child_thread_id",
    "dispatcher_run_id",
    "timeline_item_id",
    "root",
    "dispatcher",
    "child",
    "origin",
    "status",
    "started_at",
    "completed_at",
    "status_as_of",
    "role",
    "skill",
    "panel_id",
    "round",
    "source_revision",
    "task_id",
    "observed_attempt_index_in_window",
    "dispatch_kind",
    "retry_of",
    "retry_status",
    "usage",
    "audit_overhead",
    "evidence_ids",
    "root_relationship",
    "dispatcher_relationship",
    "tier0",
]
TIER0_FIELDS = [
    "status",
    "interrupted",
    "failed",
    "replacement_of",
    "replaced_by",
    "retry_of",
    "retried_by",
    "latency_ms",
    "own_input_tokens",
    "own_cached_input_tokens",
    "own_cache_creation_tokens",
    "own_output_tokens",
    "own_reasoning_tokens",
    "provider",
    "model",
    "family",
    "effort",
    "dispatcher_family",
    "root_family",
]
CONTRIBUTION_FIELDS = [
    "contribution_id",
    "revision",
    "cause_id",
    "panel_id",
    "label",
    "kind",
    "severity",
    "role",
    "independence",
    "independence_evidence_ids",
    "decision_evidence_ids",
    "artifact_evidence_ids",
    "disposition",
    "accepted",
    "applied",
    "pending",
    "evidence_level",
    "disposition_as_of",
    "limits",
]
DISCOVERER_FIELDS = [
    "dispatch_id",
    "child_family",
    "original_finding_id",
    "source_json_pointer",
    "source_severity",
    "evidence_ids",
]
PANEL_FIELDS = [
    "panel_id",
    "revision",
    "complete",
    "completeness_basis",
    "dispatch_ids",
    "evidence_ids",
    "expected_families",
    "independence",
    "independence_limit",
    "method_limits",
    "predefined_baseline_pair",
    "baseline_comparison",
    "scope",
]
EVIDENCE_FIELDS = [
    "evidence_id",
    "kind",
    "table",
    "source_id",
    "thread_id",
    "run_id",
    "parent_node_id",
    "child_thread_id",
    "started_at",
    "revision",
    "observed_at",
    "source_item_id",
    "updated_at",
    "sha256",
    "json_pointer",
    "base",
    "head",
    "blob_sha256",
    "diff_sha256",
    "link",
    "allowlist_sha256",
]
COVERAGE_FIELDS = [
    "dispatch_id",
    "revision",
    "disposition_as_of",
    "evaluable",
    "evidence_ids",
    "panel_id",
    "role",
    "application_not_applicable",
    "application_unknown",
]
SOURCE_FIELDS = [
    "as_of",
    "transaction",
    "window",
    "schema_sha256",
    "projection_thread_count",
    "projection_run_count",
    "cohort_rows",
    "cohort_min_started_at",
    "cohort_max_started_at",
    "null_start_rows",
    "timeline_subagent_rows",
    "cohort_missing_timeline_ids",
    "archived_threads",
    "deleted_threads",
    "history_origins",
]


def allow(row, fields):
    return {key: row[key] for key in fields if key in row}


def public_envelope(
    dispatches, contributions, panels, coverage, evidence, sources, audits
):
    summary = summarize(dispatches, contributions, panels, coverage)
    records = tier0(dispatches)
    attribution = summary["attribution"]
    not_applicable = set(attribution["application_not_applicable_dispatch_ids"])
    application_unknown = set(attribution["application_unknown_dispatch_ids"])
    public_coverage = []
    for row in coverage:
        record = allow(row, COVERAGE_FIELDS)
        record["application_not_applicable"] = row["dispatch_id"] in not_applicable
        record["application_unknown"] = row["dispatch_id"] in application_unknown
        public_coverage.append(record)
    public_contributions = []
    for row in contributions:
        record = allow(row, CONTRIBUTION_FIELDS)
        record["discoverers"] = [
            allow(item, DISCOVERER_FIELDS) for item in row["discoverers"]
        ]
        public_contributions.append(record)
    source = sources[-1]
    audit = (
        audits[-1]
        if audits
        else {"as_of": None, "selection": "No semantic audit imported"}
    )
    envelope = {
        "schema_version": VERSION,
        "window": source["window"],
        "source_as_of": source["as_of"],
        "disposition_as_of": audit["as_of"],
        "timezone": "America/Sao_Paulo",
        "coverage": {
            "dispatch_census": "Complete for local persisted subagent projections; not universal remote/import history",
            "attribution_evaluable": sum(
                row.get("evaluable") is True for row in coverage
            ),
            "attribution_scope": audit["selection"],
            "remote": "not collected",
            "native": "persisted native subagent projection only",
            "imported": "v1_import metadata retained; missing imported dispatch events cannot be recovered",
            "unknown_usage": "null, never zero",
        },
        "source_snapshot": allow(source, SOURCE_FIELDS),
        "source_snapshots": [allow(item, SOURCE_FIELDS) for item in sources],
        "disposition_snapshots": [
            allow(
                item,
                [
                    "as_of",
                    "selection",
                    "unreviewed",
                    "owner",
                    "auditor_model",
                    "bundle_id",
                ],
            )
            for item in audits
        ],
        "summary": summary,
        "dispatches": [
            allow(dict(row, tier0=records[row["dispatch_id"]]), DISPATCH_FIELDS)
            for row in dispatches
        ],
        "contributions": public_contributions,
        "panels": [allow(row, PANEL_FIELDS) for row in panels],
        "attribution_coverage": public_coverage,
        "evidence": [allow(row, EVIDENCE_FIELDS) for row in evidence],
    }
    envelope["generation_id"] = digest(envelope)[:24]
    validate_envelope(envelope)
    return envelope


def unique(rows, key):
    index = {}
    for row in rows:
        ident = row[key]
        if ident in index:
            raise ValueError("Duplicate " + key + ": " + ident)
        index[ident] = row
    return index


def require_references(ids, index):
    if not isinstance(ids, list) or any(value not in index for value in ids):
        raise ValueError("Unresolved evidence or record reference")


def no_private_fields(value):
    forbidden = {
        "local_path",
        "source_file",
        "source_file_stat",
        "prompt",
        "text",
        "result",
        "input",
        "output",
        "command",
        "transcript",
        "payload_json",
        "auth",
        "credentials",
    }
    if isinstance(value, dict):
        for key, child in value.items():
            if key.lower() in forbidden:
                raise ValueError("Private field in public envelope: " + key)
            no_private_fields(child)
    elif isinstance(value, list):
        for child in value:
            no_private_fields(child)
    elif isinstance(value, str) and any(
        part in value
        for part in ("/Users/", "/Volumes/", "/home/", "file://", "-----BEGIN ")
    ):
        raise ValueError("Local path or credential marker in public envelope")


def closed(row, fields, nested=()):
    if not isinstance(row, dict) or set(row) - set(fields):
        raise ValueError("Unexpected public schema field")
    for key, value in row.items():
        if key not in nested and (
            type(value) not in (str, int, float, bool, type(None))
            or (type(value) is float and not math.isfinite(value))
        ):
            raise ValueError("Expected scalar public field")


def strings(values):
    if not isinstance(values, list) or any(
        not isinstance(item, str) for item in values
    ):
        raise ValueError("Expected public reference list")


def validate_shape(envelope):
    nested = {
        "window",
        "coverage",
        "source_snapshot",
        "source_snapshots",
        "disposition_snapshots",
        "summary",
        "dispatches",
        "contributions",
        "panels",
        "attribution_coverage",
        "evidence",
    }
    closed(
        envelope,
        nested
        | {
            "schema_version",
            "generation_id",
            "source_as_of",
            "disposition_as_of",
            "timezone",
        },
        nested,
    )
    closed(envelope["window"], ["start_inclusive", "end_inclusive"])
    closed(
        envelope["coverage"],
        [
            "dispatch_census",
            "attribution_evaluable",
            "attribution_scope",
            "remote",
            "native",
            "imported",
            "unknown_usage",
        ],
    )
    for source in [envelope["source_snapshot"], *envelope["source_snapshots"]]:
        closed(source, SOURCE_FIELDS, ["window", "history_origins"])
        closed(source["window"], ["start_inclusive", "end_inclusive"])
        if any(
            type(n) is not int or n < 0
            for n in source.get("history_origins", {}).values()
        ):
            raise ValueError("Invalid history-origin count")
    for audit in envelope["disposition_snapshots"]:
        closed(
            audit,
            ["as_of", "selection", "unreviewed", "owner", "auditor_model", "bundle_id"],
        )
    for row in envelope["dispatches"]:
        closed(
            row,
            DISPATCH_FIELDS,
            ["root", "dispatcher", "child", "usage", "evidence_ids", "tier0"],
        )
        strings(row["evidence_ids"])
        if "tier0" in row:
            closed(row["tier0"], TIER0_FIELDS)
        for key in ["root", "dispatcher", "child"]:
            identity = row[key]
            closed(
                identity,
                [
                    "run_id",
                    "provider_instance_id",
                    "harness",
                    "model",
                    "family",
                    "settings",
                    "basis",
                    "settings_unknown",
                ],
                ["settings"],
            )
            if identity.get("settings") is not None:
                if not isinstance(identity["settings"], list):
                    raise ValueError("Invalid model settings")
                for option in identity["settings"]:
                    closed(option, ["id", "value"])
                    if not isinstance(option.get("id"), str) or type(
                        option.get("value")
                    ) not in (str, bool, int, float):
                        raise ValueError("Invalid scalar model setting")
        usage = row["usage"]
        maps = [
            "own",
            "reported_main_agent",
            "descendant_inclusive",
            "unknown_scope",
            "scope_counts",
            "usage_status_counts",
        ]
        closed(
            usage,
            maps
            + [
                "availability",
                "note",
                "has_subagents_turns",
                "provider_turn_count",
                "source_provider_turn_ids",
            ],
            maps + ["source_provider_turn_ids"],
        )
        strings(usage["source_provider_turn_ids"])
        for key in maps:
            value = usage.get(key)
            if value is None:
                continue
            if not isinstance(value, dict):
                raise TypeError("Invalid usage counters")
            if key not in ["scope_counts", "usage_status_counts"]:
                closed(
                    value,
                    [
                        "inputTokens",
                        "outputTokens",
                        "cachedInputTokens",
                        "cacheCreationTokens",
                        "reasoningTokens",
                    ],
                )
            if any(
                n is not None
                and (type(n) not in (int, float) or not math.isfinite(n) or n < 0)
                for n in value.values()
            ):
                raise ValueError("Invalid usage count")
    for row in envelope["contributions"]:
        arrays = [
            "discoverers",
            "independence_evidence_ids",
            "decision_evidence_ids",
            "artifact_evidence_ids",
        ]
        closed(row, CONTRIBUTION_FIELDS + ["discoverers"], arrays)
        for key in arrays[1:]:
            strings(row.get(key, []))
        for discoverer in row["discoverers"]:
            closed(discoverer, DISCOVERER_FIELDS, ["evidence_ids"])
            strings(discoverer.get("evidence_ids", []))
    for row in envelope["panels"]:
        arrays = [
            "dispatch_ids",
            "evidence_ids",
            "expected_families",
            "predefined_baseline_pair",
        ]
        closed(row, PANEL_FIELDS, arrays)
        for key in arrays:
            if row.get(key) is not None:
                strings(row[key])
    for row in envelope["attribution_coverage"]:
        closed(row, COVERAGE_FIELDS, ["evidence_ids"])
        strings(row["evidence_ids"])
    for row in envelope["evidence"]:
        closed(row, EVIDENCE_FIELDS)


def has_tier0(envelope):
    return "tier0_by_child_seat" in envelope["summary"]


def without_tier0(envelope):
    legacy = copy.deepcopy(envelope)
    legacy.pop("generation_id")
    del legacy["summary"]["tier0_by_child_seat"]
    for row in legacy["dispatches"]:
        del row["tier0"]
    legacy["generation_id"] = digest(legacy)[:24]
    return legacy


def validate_envelope(envelope):
    validate_shape(envelope)
    if envelope.get("schema_version") != VERSION:
        raise ValueError("Unsupported dashboard schema")
    expected = copy.deepcopy(envelope)
    generation = expected.pop("generation_id", None)
    if generation != digest(expected)[:24]:
        raise ValueError("Generation content hash mismatch")
    no_private_fields(envelope)
    window = envelope["window"]
    if timestamp(window["start_inclusive"]) > timestamp(window["end_inclusive"]):
        raise ValueError("Reversed cohort bounds")
    timestamp(envelope["source_as_of"])
    if envelope["disposition_as_of"] is not None:
        timestamp(envelope["disposition_as_of"])
    dispatches = unique(envelope["dispatches"], "dispatch_id")
    contributions = unique(envelope["contributions"], "contribution_id")
    panels = unique(envelope["panels"], "panel_id")
    evidence = unique(envelope["evidence"], "evidence_id")
    coverage = unique(envelope["attribution_coverage"], "dispatch_id")
    causes = set()
    tiered = has_tier0(envelope)
    records = tier0(list(dispatches.values()))
    for row in dispatches.values():
        if row.get("tier0") != (records[row["dispatch_id"]] if tiered else None):
            raise ValueError("Tier-0 record does not reproduce from published dispatch")
        if (
            not timestamp(window["start_inclusive"])
            <= timestamp(row["started_at"])
            <= timestamp(window["end_inclusive"])
        ):
            raise ValueError("Dispatch outside cumulative cohort")
        require_references(row["evidence_ids"], evidence)
        if row.get("panel_id") is not None and row["panel_id"] not in panels:
            raise ValueError("Dispatch refers to unknown panel")
    for row in coverage.values():
        if row["dispatch_id"] not in dispatches or type(row["evaluable"]) is not bool:
            raise ValueError("Invalid evaluable coverage")
        require_references(row["evidence_ids"], evidence)
        if row.get("panel_id") is not None and row["panel_id"] not in panels:
            raise ValueError("Coverage refers to unknown panel")
    for row in contributions.values():
        if row.get("panel_id") is not None and row["panel_id"] not in panels:
            raise ValueError("Contribution refers to unknown panel")
        if row["cause_id"] in causes:
            raise ValueError("Duplicate cause/decision unit")
        causes.add(row["cause_id"])
        for key in ("accepted", "applied"):
            if row.get(key) is not None and type(row[key]) is not bool:
                raise ValueError("Disposition booleans must be true, false or null")
        if row.get("accepted") is True and not row.get("decision_evidence_ids"):
            raise ValueError("Accepted contribution lacks parent decision proof")
        if row.get("applied") is True and not row.get("artifact_evidence_ids"):
            raise ValueError("Applied contribution lacks artifact proof")
        if row["kind"] == "verification_artifact" and row.get("applied") is True:
            raise ValueError(
                "Source-code application does not apply to verification artifacts"
            )
        for key in (
            "decision_evidence_ids",
            "artifact_evidence_ids",
            "independence_evidence_ids",
        ):
            require_references(row.get(key, []), evidence)
        if not row["discoverers"]:
            raise ValueError("Contribution has no discoverer")
        for discoverer in row["discoverers"]:
            dispatch = dispatches.get(discoverer["dispatch_id"])
            if (
                dispatch is None
                or discoverer["child_family"] != dispatch["child"]["family"]
            ):
                raise ValueError("Discoverer identity discrepancy")
            if (
                row.get("panel_id") is not None
                and discoverer["dispatch_id"]
                not in panels[row["panel_id"]]["dispatch_ids"]
            ):
                raise ValueError("Contribution discoverer is outside its panel roster")
            require_references(discoverer.get("evidence_ids", []), evidence)
    for panel in envelope["panels"]:
        require_references(panel["dispatch_ids"], dispatches)
        require_references(panel["evidence_ids"], evidence)
        if len(panel["dispatch_ids"]) != len(set(panel["dispatch_ids"])):
            raise ValueError("Duplicate panel roster dispatch")
        if panel.get("complete") and (
            len(panel["dispatch_ids"]) != 3
            or len(set(panel["expected_families"])) != 3
            or "unknown" in panel["expected_families"]
        ):
            raise ValueError(
                "Complete three-family panels require three distinct known families"
            )
        if panel.get("complete") and sorted(
            dispatches[ident]["child"]["family"] for ident in panel["dispatch_ids"]
        ) != sorted(panel["expected_families"]):
            raise ValueError("Complete panel roster does not match expected families")
        if panel.get("complete") and not all(
            coverage.get(ident, {}).get("evaluable") is True
            for ident in panel["dispatch_ids"]
        ):
            raise ValueError("Complete panel lacks full inspected coverage")
    for row in evidence.values():
        link = row.get("link")
        if link and not link.strip("<>").startswith(("https://", "t3-thread://")):
            raise ValueError("Unsafe evidence link")
    summary = summarize(
        list(dispatches.values()),
        list(contributions.values()),
        envelope["panels"],
        list(coverage.values()),
    )
    if not tiered:
        del summary["tier0_by_child_seat"]
    if summary != envelope["summary"]:
        raise ValueError("Summary does not reproduce from published records")
    attribution = summary["attribution"]
    for row in coverage.values():
        for field, key in (
            ("application_not_applicable", "application_not_applicable_dispatch_ids"),
            ("application_unknown", "application_unknown_dispatch_ids"),
        ):
            if type(row.get(field)) is not bool or row[field] != (
                row["dispatch_id"] in attribution[key]
            ):
                raise ValueError("Application coverage exclusion mismatch")
    source = envelope["source_snapshot"]
    if (
        source["window"] != window
        or source["as_of"] != envelope["source_as_of"]
        or source["cohort_rows"] != len(dispatches)
    ):
        raise ValueError("Source snapshot boundary/count mismatch")
    if (
        sum(row.get("evaluable") is True for row in coverage.values())
        != envelope["coverage"]["attribution_evaluable"]
    ):
        raise ValueError("Evaluable denominator mismatch")
    return envelope


def validate_publication(root, publication):
    if (
        publication.get("schema_version") != VERSION
        or publication.get("status") != "published"
    ):
        raise ValueError("Unsupported publication schema or status")
    path = publication_file(root, publication)
    content = path.read_bytes()
    if hashlib.sha256(content).hexdigest() != publication.get("sha256"):
        raise ValueError("Published file SHA256 mismatch")
    envelope = validate_envelope(json.loads(content))
    for key in ("generation_id", "window", "source_as_of", "disposition_as_of"):
        if publication.get(key) != envelope[key]:
            raise ValueError("Publication metadata mismatch: " + key)
    return envelope
