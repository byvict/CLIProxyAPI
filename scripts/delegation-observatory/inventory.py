#!/usr/bin/env python3
"""Tier-0 round inventory, seat coverage and quota estimate from a validated export."""

import argparse
import collections
import contextlib
import datetime as dt
import json
import re
import sqlite3
from pathlib import Path

from export import validate_envelope
from extract import EFFORT_OPTIONS, request_tokens
from storage import digest, dump, load, timestamp

ROUND_GAP = dt.timedelta(minutes=30)
SIBLING_WINDOW = dt.timedelta(hours=2)
NATIVE_GAP = dt.timedelta(minutes=1)
QUOTA_GAP = dt.timedelta(hours=6)
HOUR = dt.timedelta(hours=1)
QUOTA_PROVIDERS = {
    "codex": "codex",
    "claude": "claudeAgent",
    "xai": "grok",
    "meta": "muse",
    "antigravity": "antigravity",
}
SCOPE_MODELS = {
    "all": None,
    "fable": ("fable",),
    "gemini-models": ("gemini",),
    "claude-and-gpt-models": ("claude", "gpt"),
}
SEAT_LINE = re.compile(r"([^#:][^:]*):\s*(.+)")
SEAT_ENTRY = re.compile(
    r"(?P<provider>[^/\s]+)/(?P<model>\S+)(?P<options>(?:\s+[\w.-]+=\S+)*)"
)


def start(dispatch):
    return timestamp(dispatch["started_at"])


def review_or_design(dispatch):
    return dispatch["role"].startswith("review") or dispatch["role"] == "design"


def panel_role(dispatch):
    return review_or_design(dispatch) or dispatch["role"] in ("release", "verification")


def split_by_ordinal(group, requests):
    slots = collections.defaultdict(set)
    for d in group:
        members = requests[d["dispatch_id"]][1]
        slot = tuple(m for m in members if not m.isdigit())
        ordinal = tuple(m for m in members if m.isdigit())
        if slot:
            slots[slot].add(ordinal)
    if not any(len(ordinals) > 1 for ordinals in slots.values()):
        return [group]
    parts = collections.defaultdict(list)
    for d in group:
        parts[tuple(m for m in requests[d["dispatch_id"]][1] if m.isdigit())].append(d)
    return list(parts.values())


def one_token_apart(before, after):
    return (
        before is not None
        and after is not None
        and len(before[0]) == len(after[0])
        and sum(a != b for a, b in zip(before[0], after[0])) == 1
    )


def place(found, dispatch, accepts):
    matches = [
        group
        for group in found
        if group[0]["dispatcher_thread_id"] == dispatch["dispatcher_thread_id"]
        and accepts(group, dispatch)
    ]
    if matches:
        max(matches, key=len).append(dispatch)
    else:
        found.append([dispatch])


def rounds(dispatches):
    work = sorted(
        (
            d
            for d in dispatches
            if not d["audit_overhead"] and d["dispatch_kind"] != "continuation"
        ),
        key=lambda d: (start(d), d["dispatch_id"]),
    )
    requests = {d["dispatch_id"]: request_tokens(d["dispatch_id"]) for d in work}
    stems = collections.defaultdict(list)
    for d in work:
        if requests[d["dispatch_id"]]:
            key = (d["dispatcher_thread_id"], tuple(requests[d["dispatch_id"]][0]))
            stems[key].append(d)
    found = []
    for members in stems.values():
        found.append([members[0]])
        for d in members[1:]:
            last = found[-1][-1]
            if (
                start(d) - start(last) > ROUND_GAP
                and d["dispatcher_run_id"] != last["dispatcher_run_id"]
            ):
                found.append([d])
            else:
                found[-1].append(d)
    found = [part for group in found for part in split_by_ordinal(group, requests)]
    singles = sorted((group[0] for group in found if len(group) == 1), key=start)
    found = [group for group in found if len(group) > 1]

    def sibling(group, d):
        if any(m["dispatch_id"] == d["tier0"]["replacement_of"] for m in group):
            return True
        starts = [start(m) for m in group]
        completed = {m["tier0"]["family"] for m in group if m["status"] == "completed"}
        return (
            panel_role(d)
            and min(starts) - ROUND_GAP <= start(d) <= max(starts) + SIBLING_WINDOW
            and d["tier0"]["family"] not in completed
            and any(
                one_token_apart(requests[m["dispatch_id"]], requests[d["dispatch_id"]])
                for m in group
            )
        )

    def native(group, d):
        return any(
            m["dispatcher_run_id"] == d["dispatcher_run_id"]
            and abs(start(m) - start(d)) <= NATIVE_GAP
            for m in group
        )

    for d in singles:
        place(found, d, sibling)
    for d in work:
        if not requests[d["dispatch_id"]]:
            place(found, d, native)
    return [
        sorted(group, key=lambda d: (start(d), d["dispatch_id"]))
        for group in found
        if any(review_or_design(d) for d in group)
    ]


def round_record(members, coverage, panels):
    families = sorted({m["tier0"]["family"] for m in members} - {"unknown"})
    completed = sorted(
        {m["tier0"]["family"] for m in members if m["status"] == "completed"}
        - {"unknown"}
    )
    inspected = [m for m in members if m["dispatch_id"] in coverage]
    roles = {
        "design" if m["role"] == "design" else "review"
        for m in members
        if review_or_design(m)
    }
    return {
        "round_id": digest(sorted(m["dispatch_id"] for m in members))[:16],
        "dispatcher_thread_id": members[0]["dispatcher_thread_id"],
        "root_thread_id": members[0]["root_thread_id"],
        "project_id": members[0]["project_id"],
        "role": roles.pop() if len(roles) == 1 else "mixed",
        "started_at": members[0]["started_at"],
        "roster_size": len(members),
        "families": families,
        "family_count": len(families),
        "completed_families": completed,
        "completed_family_count": len(completed),
        "unknown_family_members": sum(
            m["tier0"]["family"] == "unknown" for m in members
        ),
        "inspected_members": len(inspected),
        "evaluable_members": sum(coverage[m["dispatch_id"]] for m in inspected),
        "coverage_state": (
            "covered"
            if len(inspected) == len(members)
            else "partial" if inspected else "uncovered"
        ),
        "panel_ids": sorted(
            {p for m in members for p in panels.get(m["dispatch_id"], ())}
        ),
        "members": [
            {
                "dispatch_id": m["dispatch_id"],
                "role": m["role"],
                "status": m["status"],
                "family": m["tier0"]["family"],
                "provider": m["tier0"]["provider"],
                "model": m["tier0"]["model"],
                "effort": m["tier0"]["effort"],
                "replacement_of": m["tier0"]["replacement_of"],
                "inspected": m["dispatch_id"] in coverage,
                "evaluable": coverage.get(m["dispatch_id"]),
            }
            for m in members
        ],
    }


def family_table(records):
    table = {}
    for r in records:
        row = table.setdefault(
            str(r["completed_family_count"]),
            dict.fromkeys(
                (
                    "rounds",
                    "covered",
                    "partial",
                    "uncovered",
                    "dispatches",
                    "inspected_dispatches",
                ),
                0,
            ),
        )
        row["rounds"] += 1
        row[r["coverage_state"]] += 1
        row["dispatches"] += r["roster_size"]
        row["inspected_dispatches"] += r["inspected_members"]
    return dict(sorted(table.items()))


def panel_reason(match, panel, record, extras):
    if match == "exact":
        return None
    if match != "contained":
        return "Panel members fall outside a single inferred review or design round."
    parts = []
    for m in extras:
        if m["family"] not in panel["expected_families"]:
            parts.append(
                f"one {m['status']} {m['family']} member, a family outside the panel's three"
            )
        elif any(x["replacement_of"] == m["dispatch_id"] for x in record["members"]):
            parts.append(
                f"one {m['status']} {m['family']} attempt that a panel member replaced"
            )
        else:
            parts.append(
                f"one {m['status']} {m['family']} sibling outside the panel roster"
            )
    return "The inferred round adds " + "; ".join(parts) + "."


def panel_check(envelope, records):
    by_dispatch = {m["dispatch_id"]: r for r in records for m in r["members"]}
    rows = []
    for panel in envelope["panels"]:
        if not panel["complete"]:
            continue
        ids = set(panel["dispatch_ids"])
        found = sorted({by_dispatch[i]["round_id"] for i in ids if i in by_dispatch})
        extras = []
        if not ids <= by_dispatch.keys():
            match = "missing"
        elif len(found) > 1:
            match = "split"
        else:
            record = by_dispatch[next(iter(ids))]
            extras = [m for m in record["members"] if m["dispatch_id"] not in ids]
            match = "contained" if extras else "exact"
        rows.append(
            {
                "panel_id": panel["panel_id"],
                "families": sorted(panel["expected_families"]),
                "round_ids": found,
                "match": match,
                "reason": panel_reason(
                    match, panel, by_dispatch.get(next(iter(ids))), extras
                ),
            }
        )
    return {
        "generation_id": envelope["generation_id"],
        "complete_panels": len(rows),
        "matches": dict(collections.Counter(row["match"] for row in rows)),
        "panels": rows,
    }


def round_inventory(envelope):
    coverage = {
        row["dispatch_id"]: row["evaluable"] for row in envelope["attribution_coverage"]
    }
    panels = collections.defaultdict(list)
    for panel in envelope["panels"]:
        for ident in panel["dispatch_ids"]:
            panels[ident].append(panel["panel_id"])
    records = [
        round_record(members, coverage, panels)
        for members in rounds(envelope["dispatches"])
    ]
    table = family_table(records)
    panels_found = panel_check(envelope, records)
    return {
        "kind": "heuristic_round_inventory",
        "generation_id": envelope["generation_id"],
        "window": envelope["window"],
        "method": (
            "Work dispatches except continuations, grouped per dispatcher thread by request-name stem "
            f"(member tokens removed), split when a stem restarts more than {ROUND_GAP} later in "
            "another dispatcher run, and split by numeric member token when a named member repeats "
            "across numbers. A single dispatch joins a round when it is the tier-0 replacement of a "
            "member, or when it has a review, design, release or verification role, its stem differs "
            f"by one token, it starts within {ROUND_GAP} before or {SIBLING_WINDOW} after the round, "
            "and no completed member has its family. Native dispatches join a round of the same "
            f"dispatcher run started within {NATIVE_GAP}. Rounds with at least one review or design "
            "member are kept. Families exclude unknown. family_count counts every roster family; "
            "completed_family_count counts the families with at least one completed member and keys "
            "by_completed_family_count, three_family and four_family. Durations are hours:minutes:seconds."
        ),
        "coverage_states": {
            "covered": "every member has an attribution coverage row",
            "partial": "some members have an attribution coverage row",
            "uncovered": "no member has an attribution coverage row",
        },
        "summary": {
            "rounds": len(records),
            "by_completed_family_count": table,
            "three_family": table.get("3"),
            "four_family": table.get("4"),
            "known_complete_panels": panels_found["matches"],
        },
        "known_complete_panels": panels_found,
        "rounds": records,
    }


def parse_sheet(text):
    seats = {}
    for line in text.splitlines():
        match = SEAT_LINE.fullmatch(line.strip())
        if not match:
            continue
        role, value = match.groups()
        entries = [SEAT_ENTRY.fullmatch(e.strip()) for e in value.split(",")]
        entries = [e for e in entries if e]
        families = {e["model"].rsplit("/", 1)[-1].split("-")[0] for e in entries}
        for entry in entries:
            options = dict(o.split("=", 1) for o in entry["options"].split())
            option = next((key for key in options if key in EFFORT_OPTIONS), None)
            key = (entry["provider"], entry["model"], options.get(option))
            seat = seats.setdefault(
                key,
                {
                    "provider": entry["provider"],
                    "model": entry["model"],
                    "family": entry["model"].rsplit("/", 1)[-1].split("-")[0],
                    "effort_option": option,
                    "effort": options.get(option),
                    "roles": [],
                    "multi_family_roles": [],
                },
            )
            seat["roles"].append(role)
            if len(families) > 1:
                seat["multi_family_roles"].append(role)
    return list(seats.values())


def history(rows, multi_family, coverage):
    return {
        "dispatches": len(rows),
        "completed": sum(d["status"] == "completed" for d in rows),
        "with_own_tokens": sum(
            d["tier0"]["own_input_tokens"] is not None for d in rows
        ),
        "review_or_design": sum(review_or_design(d) for d in rows),
        "in_three_plus_family_rounds": sum(
            d["dispatch_id"] in multi_family for d in rows
        ),
        "inspected": sum(d["dispatch_id"] in coverage for d in rows),
        "evaluable": sum(coverage.get(d["dispatch_id"]) is True for d in rows),
        "providers": dict(collections.Counter(d["tier0"]["provider"] for d in rows)),
    }


def seat_coverage(envelope, sheet, inventory):
    work = [d for d in envelope["dispatches"] if not d["audit_overhead"]]
    coverage = {
        row["dispatch_id"]: row["evaluable"] for row in envelope["attribution_coverage"]
    }
    multi_family = {
        m["dispatch_id"]
        for r in inventory["rounds"]
        if r["completed_family_count"] >= 3
        for m in r["members"]
    }
    seats = parse_sheet(sheet)
    for seat in seats:
        exact = [
            d
            for d in work
            if d["tier0"]["model"] == seat["model"]
            and d["tier0"]["effort"] == seat["effort"]
        ]
        same_model = collections.Counter(
            str(d["tier0"]["effort"])
            for d in work
            if d["tier0"]["model"] == seat["model"]
            and d["tier0"]["effort"] != seat["effort"]
        )
        same_family = collections.Counter(
            d["tier0"]["model"]
            for d in work
            if d["tier0"]["family"] == seat["family"]
            and d["tier0"]["model"] != seat["model"]
        )
        seat["history"] = history(exact, multi_family, coverage)
        seat["same_model_other_efforts"] = dict(same_model)
        seat["same_family_other_models"] = dict(same_family)
        seat["coverage"] = (
            "model_and_effort"
            if exact
            else (
                "model_other_effort"
                if same_model
                else "family_only" if same_family else "none"
            )
        )
    seated = {(s["model"], s["effort"]) for s in seats}
    absent = collections.defaultdict(list)
    for d in work:
        if (d["tier0"]["model"], d["tier0"]["effort"]) not in seated:
            absent[
                (d["tier0"]["provider"], d["tier0"]["model"], d["tier0"]["effort"])
            ].append(d)
    unseated = [
        dict(
            provider=provider,
            model=model,
            effort=effort,
            **history(rows, multi_family, coverage),
        )
        for (provider, model, effort), rows in sorted(
            absent.items(), key=lambda item: (-len(item[1]), json.dumps(item[0]))
        )
    ]
    return {
        "kind": "seat_coverage",
        "generation_id": envelope["generation_id"],
        "window": envelope["window"],
        "match": "A seat matches history by child model and normalized effort; provider instances are listed separately.",
        "summary": {
            "seats": len(seats),
            "by_coverage": dict(collections.Counter(s["coverage"] for s in seats)),
            "multi_family_seats": sum(bool(s["multi_family_roles"]) for s in seats),
            "historical_dispatches_outside_current_seats": sum(
                row["dispatches"] for row in unseated
            ),
        },
        "seats": seats,
        "history_outside_current_seats": unseated,
    }


def observations(path):
    uri = Path(path).absolute().as_uri() + "?mode=ro"
    with contextlib.closing(sqlite3.connect(uri, uri=True)) as connection:
        connection.execute("PRAGMA query_only=ON")
        return connection.execute(
            "SELECT dense_rank() OVER (ORDER BY credential), provider, window_id, label, "
            "observed_at_ms, remaining, reset_at_ms, period_hours FROM observations "
            "ORDER BY observed_at_ms"
        ).fetchall()


def moment(milliseconds):
    return dt.datetime.fromtimestamp(milliseconds / 1000, dt.timezone.utc)


def hour_floor(moment_):
    return moment_.replace(minute=0, second=0, microsecond=0)


def hours(begin, end):
    if end <= begin:
        return {hour_floor(begin): 1.0}
    shares = {}
    cursor = begin
    while cursor < end:
        bucket = hour_floor(cursor)
        stop = min(end, bucket + HOUR)
        shares[bucket] = (stop - cursor) / (end - begin)
        cursor = stop
    return shares


def window_key(provider, window_id, period):
    scope = (
        "fable"
        if window_id == "seven-day-fable"
        else window_id.split("/")[0] if "/" in window_id else "all"
    )
    return (provider, scope, period)


def observed_hours(rows, first, last):
    series = collections.defaultdict(list)
    for row in rows:
        series[(row[0], row[1], row[2], row[3])].append(row)
    observed = collections.defaultdict(lambda: [0.0, 0.0, None, []])
    for ident, points in series.items():
        for before, after in zip(points, points[1:]):
            begin, end = moment(before[4]), moment(after[4])
            if (
                None in (before[5], after[5], before[7])
                or end - begin > QUOTA_GAP
                or after[5] > before[5]
                or end <= begin
                or (before[6] is not None and before[6] <= after[4])
            ):
                continue
            key = window_key(ident[1], ident[2], before[7])
            for bucket, share in hours(begin, end).items():
                if first <= bucket < last:
                    slot = observed[(key, bucket, ident)]
                    slot[0] += share * (end - begin).total_seconds()
                    slot[1] += share * (before[5] - after[5])
                    slot[2] = after[6] and moment(after[6])
                    slot[3].append((max(begin, bucket), min(end, bucket + HOUR)))
    chosen = {}
    for (key, bucket, _), slot in sorted(
        observed.items(), key=lambda item: str(item[0])
    ):
        if (key, bucket) not in chosen or slot[:2] > chosen[(key, bucket)][:2]:
            chosen[(key, bucket)] = slot
    return {
        hour: {
            "coverage": min(1.0, seconds / 3600),
            "points": points_used,
            "reset_at": hour_floor(reset + HOUR / 2).isoformat() if reset else None,
            "intervals": intervals,
        }
        for hour, (seconds, points_used, reset, intervals) in chosen.items()
    }


def demand(work):
    spans = {}
    for d in work:
        record = d["tier0"]
        if record["own_input_tokens"] is None and record["own_output_tokens"] is None:
            continue
        total = (record["own_input_tokens"] or 0) + (record["own_output_tokens"] or 0)
        end = timestamp(d["completed_at"]) if d["completed_at"] else start(d)
        spans[d["dispatch_id"]] = (start(d), end, total)
    return spans


def observed_tokens(span, intervals):
    begin, end, total = span
    if end <= begin:
        return total if any(b <= begin < e for b, e in intervals) else 0.0
    seconds = sum(
        max(0.0, (min(end, e) - max(begin, b)).total_seconds()) for b, e in intervals
    )
    return total * seconds / (end - begin).total_seconds()


def in_scope(dispatch, provider, scope):
    models = SCOPE_MODELS.get(scope, (scope,))
    return dispatch["tier0"]["provider"] == QUOTA_PROVIDERS.get(provider) and (
        models is None
        or any(token in (dispatch["tier0"]["model"] or "") for token in models)
    )


def quota_estimate(envelope, rows):
    work = {
        d["dispatch_id"]: d for d in envelope["dispatches"] if not d["audit_overhead"]
    }
    first = timestamp(envelope["window"]["start_inclusive"]).replace(
        minute=0, second=0, microsecond=0
    )
    last = timestamp(envelope["window"]["end_inclusive"])
    supply = observed_hours(rows, first, last)
    spans = demand(work.values())
    windows = {}
    estimates = collections.defaultdict(lambda: collections.defaultdict(float))
    covered = collections.defaultdict(lambda: collections.defaultdict(float))
    for ((provider, scope, period), bucket), hour in sorted(supply.items()):
        key = (provider, scope, period, hour["reset_at"])
        window = windows.setdefault(
            key,
            {
                "quota_provider": provider,
                "provider_instance": QUOTA_PROVIDERS.get(provider),
                "scope": scope,
                "period_hours": period,
                "reset_at": hour["reset_at"],
                "first_hour": bucket.isoformat(),
                "observed_hours": 0.0,
                "points_observed": 0.0,
                "points_allocated": 0.0,
                "dispatch_tokens_in_observed_time": 0.0,
                "dispatches": set(),
            },
        )
        window["last_hour"] = bucket.isoformat()
        window["observed_hours"] += hour["coverage"]
        window["points_observed"] += hour["points"]
        eligible = {
            ident: observed_tokens(span, hour["intervals"])
            for ident, span in spans.items()
            if in_scope(work[ident], provider, scope)
        }
        eligible = {ident: amount for ident, amount in eligible.items() if amount}
        total = sum(eligible.values())
        window["dispatch_tokens_in_observed_time"] += total
        if not total:
            continue
        window["points_allocated"] += hour["points"]
        for ident, amount in eligible.items():
            label = f"{provider}/{scope}/{period:g}h"
            estimates[ident][label] += hour["points"] * amount / total
            covered[ident][label] += amount
            window["dispatches"].add(ident)
    window_rows = []
    for window in windows.values():
        window["dispatches"] = len(window["dispatches"])
        window["points_unallocated"] = (
            window["points_observed"] - window["points_allocated"]
        )
        window["points_per_million_tokens"] = (
            window["points_allocated"]
            / window["dispatch_tokens_in_observed_time"]
            * 1e6
            if window["dispatch_tokens_in_observed_time"]
            else None
        )
        window_rows.append(window)
    seats = collections.defaultdict(
        lambda: {"points": 0.0, "covered_tokens": 0.0, "dispatches": set()}
    )
    for ident, labels in estimates.items():
        record = work[ident]["tier0"]
        for label, points in labels.items():
            seat = seats[(record["provider"], record["model"], record["effort"], label)]
            seat["points"] += points
            seat["covered_tokens"] += covered[ident][label]
            seat["dispatches"].add(ident)
    seat_rows = []
    for (provider, model, effort, label), seat in sorted(
        seats.items(), key=lambda item: json.dumps(item[0])
    ):
        totals = [
            (d["tier0"]["own_input_tokens"] or 0)
            + (d["tier0"]["own_output_tokens"] or 0)
            for d in work.values()
            if (d["tier0"]["provider"], d["tier0"]["model"], d["tier0"]["effort"])
            == (provider, model, effort)
            and d["tier0"]["own_input_tokens"] is not None
        ]
        rate = (
            seat["points"] / seat["covered_tokens"] if seat["covered_tokens"] else None
        )
        seat_rows.append(
            {
                "provider": provider,
                "model": model,
                "effort": effort,
                "window": label,
                "dispatches_in_observed_time": len(seat["dispatches"]),
                "estimated_points": seat["points"],
                "points_per_million_tokens": rate * 1e6 if rate is not None else None,
                "dispatches_with_own_tokens": len(totals),
                "extrapolated_points_per_dispatch": (
                    rate * sum(totals) / len(totals)
                    if rate is not None and totals
                    else None
                ),
            }
        )
    providers = collections.Counter(d["tier0"]["provider"] for d in work.values())
    with_tokens = collections.Counter(
        d["tier0"]["provider"]
        for d in work.values()
        if d["tier0"]["own_input_tokens"] is not None
    )
    return {
        "kind": "estimate",
        "estimate": True,
        "generation_id": envelope["generation_id"],
        "window": envelope["window"],
        "method": (
            "Quota points are drops of remaining percent between consecutive readings of one "
            f"collector series, skipped across window resets, failure or sign-out readings without a "
            f"value, and gaps over {QUOTA_GAP}, spread evenly over UTC hours. Where collectors of one "
            "account overlap, each hour keeps the series that observed it longest. Each hour's points "
            "are distributed over the own input plus output tokens that work dispatches of the matching "
            "provider and scope spent inside that series' observed intervals of the hour, with each "
            "dispatch's tokens spread evenly over its duration."
        ),
        "limits": [
            "Upper bound for dispatch cost: root threads and work outside T3 share the same quota but have no dispatch tokens in the denominator.",
            "Hours without readings and dispatches without own tokens receive no estimate.",
            "Remaining percent is reported in whole points by most collectors; short intervals carry rounding noise.",
            "Extrapolated points per dispatch multiply the seat's observed rate by its mean own tokens over the whole cohort.",
        ],
        "provider_map": QUOTA_PROVIDERS,
        "summary": {
            "windows": len(window_rows),
            "points_observed": sum(w["points_observed"] for w in window_rows),
            "points_allocated": sum(w["points_allocated"] for w in window_rows),
            "dispatches_estimated": len(estimates),
            "work_dispatches_by_provider": dict(providers),
            "work_dispatches_with_own_tokens_by_provider": dict(with_tokens),
        },
        "windows": window_rows,
        "seats": seat_rows,
        "dispatches": [
            {"dispatch_id": ident, "estimated_points": dict(labels)}
            for ident, labels in sorted(estimates.items())
        ],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dashboard", required=True, type=Path)
    parser.add_argument("--model-sheet", required=True, type=Path)
    parser.add_argument("--quota-history", required=True, type=Path)
    parser.add_argument("--out-dir", required=True, type=Path)
    args = parser.parse_args()
    envelope = validate_envelope(load(args.dashboard))
    inventory = round_inventory(envelope)
    outputs = {
        "round-inventory.json": inventory,
        "seat-coverage.json": seat_coverage(
            envelope, args.model_sheet.read_text(encoding="utf-8"), inventory
        ),
        "quota-estimate.json": quota_estimate(
            envelope, observations(args.quota_history)
        ),
    }
    args.out_dir.mkdir(parents=True, exist_ok=True)
    for name, value in outputs.items():
        dump(args.out_dir / name, value)
    print(
        json.dumps(
            {name: value["summary"] for name, value in outputs.items()},
            indent=2,
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
