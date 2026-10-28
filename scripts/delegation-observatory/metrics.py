"""Observational dispatch and panel metrics from audited judgments."""

import collections

from storage import VERSION, latest


def panel_metric(panel, contributions):
    if not panel.get("complete"):
        return {
            "panel_id": panel["panel_id"],
            "complete": False,
            "all_three_exclusive": None,
            "families": None,
            "observed_union": None,
        }
    useful = [
        c
        for c in contributions
        if c.get("panel_id") == panel["panel_id"]
        and (c.get("accepted") is True or c.get("applied") is True)
    ]
    families = panel["expected_families"]
    result = {}
    for f in families:
        exclusive = []
        shared = []
        for c in useful:
            found = {d["child_family"] for d in c["discoverers"]}
            if f in found:
                (exclusive if found == {f} else shared).append(c["contribution_id"])
        result[f] = {
            "exclusive_contributions": len(exclusive),
            "shared_contributions": len(shared),
            "exclusive_ids": exclusive,
            "shared_ids": shared,
            "observed_union_without_family": len(useful) - len(exclusive),
            "observed_union_lost_without_family": len(exclusive),
        }
    return {
        "panel_id": panel["panel_id"],
        "complete": True,
        "observed_union": len(useful),
        "families": result,
        "all_three_exclusive": all(
            result[f]["exclusive_contributions"] > 0 for f in families
        ),
        "interpretation": "Leave-one-out counts describe this observed accepted/applied union only; no causal or prospective policy gain.",
        "baseline_pair": panel.get("predefined_baseline_pair"),
        "baseline_comparison": None,
    }


def summarize(dispatches, contributions, panels, coverage):
    ds = latest(dispatches, "dispatch_id")
    cs = latest(contributions, "contribution_id")
    ps = latest(panels, "panel_id")
    work = [d for d in ds if not d.get("audit_overhead")]
    ev = {c["dispatch_id"] for c in coverage if c.get("evaluable")}
    uses = collections.defaultdict(list)
    for c in cs:
        for discoverer in c["discoverers"]:
            uses[discoverer["dispatch_id"]].append(c)

    def units(group):
        eligible = [d for d in group if d["dispatch_id"] in ev]
        accepted = [
            d["dispatch_id"]
            for d in eligible
            if any(c.get("accepted") is True for c in uses[d["dispatch_id"]])
        ]
        applied = [
            d["dispatch_id"]
            for d in eligible
            if any(c.get("applied") is True for c in uses[d["dispatch_id"]])
        ]
        used = sorted(set(accepted + applied))
        n = len(eligible)
        not_applicable = [
            d["dispatch_id"]
            for d in eligible
            if uses[d["dispatch_id"]]
            and all(
                (c["kind"] == "verification_artifact") for c in uses[d["dispatch_id"]]
            )
        ]
        application_unknown = [
            d["dispatch_id"]
            for d in eligible
            if d["dispatch_id"] not in not_applicable
            and d["dispatch_id"] not in applied
            and any(
                c.get("accepted") is True and c.get("applied") is None
                for c in uses[d["dispatch_id"]]
            )
        ]
        applied_n = n - len(not_applicable) - len(application_unknown)
        return {
            "dispatched": len(group),
            "completed": sum(d["status"] == "completed" for d in group),
            "evaluable": n,
            "attribution_unknown_dispatches": len(group) - n,
            "used": len(used),
            "accepted_dispatches": len(accepted),
            "applied_dispatches": len(applied),
            "accepted_rate": len(accepted) / n if n else None,
            "applied_evaluable": applied_n,
            "application_unknown_dispatches": len(application_unknown),
            "application_not_applicable_dispatches": len(not_applicable),
            "applied_rate": len(applied) / applied_n if applied_n else None,
            "applied_observed_lower_bound_rate": (
                len(applied) / (n - len(not_applicable))
                if n > len(not_applicable)
                else None
            ),
            "used_rate": len(used) / n if n else None,
            "used_dispatch_ids": used,
            "accepted_dispatch_ids": sorted(accepted),
            "applied_dispatch_ids": sorted(applied),
            "evaluable_dispatch_ids": sorted(d["dispatch_id"] for d in eligible),
            "application_unknown_dispatch_ids": sorted(application_unknown),
            "application_not_applicable_dispatch_ids": sorted(not_applicable),
            "unit": "dispatches; accepted/used rates divide by evaluable; applied rate divides by applied_evaluable dispatches",
        }

    def groups(keys):
        grouped = collections.defaultdict(list)
        for d in work:
            vals = tuple(
                (
                    d[k]["family"]
                    if k in ["root", "dispatcher", "child"]
                    else d.get(k) or "unknown"
                )
                for k in keys
            )
            grouped[vals].append(d)
        return [
            dict(zip(keys, vals), **units(grouped[vals])) for vals in sorted(grouped)
        ]

    metrics = [panel_metric(p, cs) for p in ps]
    complete = [p for p in metrics if p["complete"]]
    counts = dict(collections.Counter(c["disposition"] for c in cs))
    applied = sum(c.get("applied") is True for c in cs)
    return {
        "schema_version": VERSION,
        "dispatch_totals": {
            "all": len(ds),
            "work": len(work),
            "overhead": len(ds) - len(work),
            "status": dict(collections.Counter(d["status"] for d in work)),
            "overhead_status": dict(
                collections.Counter(d["status"] for d in ds if d.get("audit_overhead"))
            ),
            "origin": dict(collections.Counter(d["origin"] for d in work)),
            "projects": dict(collections.Counter(d["project_id"] for d in work)),
            "continuations": sum(d["dispatch_kind"] == "continuation" for d in work),
            "confirmed_replacements": sum(d.get("retry_of") is not None for d in work),
            "retry_unclassified": sum(
                d.get("retry_status", "").startswith("unknown") for d in work
            ),
        },
        "attribution": dict(
            units(work),
            contribution_clusters=len(cs),
            disposition_counts=counts,
            applied_contributions=applied,
            application_unknown_contributions=sum(
                c.get("applied") is None
                and c["kind"] != "verification_artifact"
                for c in cs
            ),
            application_not_applicable_contributions=sum(
                (c["kind"] == "verification_artifact") for c in cs
            ),
            kind_counts=dict(collections.Counter(c["kind"] for c in cs)),
            severity_counts=dict(collections.Counter(c["severity"] for c in cs)),
            accepted_by_kind=dict(
                collections.Counter(c["kind"] for c in cs if c.get("accepted"))
            ),
            accepted_by_severity=dict(
                collections.Counter(c["severity"] for c in cs if c.get("accepted"))
            ),
        ),
        "root_family_x_child_family": groups(["root", "child"]),
        "immediate_dispatcher_family_x_child_family": groups(["dispatcher", "child"]),
        "by_role_and_child_family": groups(["role", "child"]),
        "by_role_round_root_child": groups(["role", "round", "root", "child"]),
        "panel_metrics": metrics,
        "panel_totals": {
            "complete": len(complete),
            "incomplete": len(metrics) - len(complete),
            "all_three_exclusive": sum(p["all_three_exclusive"] for p in complete),
            "all_three_exclusive_rate": (
                sum(p["all_three_exclusive"] for p in complete) / len(complete)
                if complete
                else None
            ),
            "denominator": "complete inspected panels only; purposive subset",
        },
        "usage_coverage": {
            "work_dispatches_with_any_own_input_or_output": sum(
                any(
                    d["usage"]["own"][k] is not None
                    for k in ["inputTokens", "outputTokens"]
                )
                for d in work
            ),
            "work_dispatches_without_own_usage": sum(
                all(
                    d["usage"]["own"][k] is None
                    for k in ["inputTokens", "outputTokens"]
                )
                for d in work
            ),
            "ambiguous_has_subagents_dispatches": sum(
                d["usage"]["has_subagents_turns"] > 0 for d in work
            ),
            "not_summed": "Cumulative tokenUsage context counters and hasSubagents=true telemetry are not own tokens. No prices/costs inferred.",
        },
        "unknowns": {
            "root_model": sum(d["root"]["family"] == "unknown" for d in work),
            "child_model": sum(d["child"]["family"] == "unknown" for d in work),
            "no_child_thread": sum(d["child_thread_id"] is None for d in work),
            "role": sum(d["role"] == "unknown" for d in work),
        },
        "audit_overhead": {
            "dispatch_ids": [d["dispatch_id"] for d in ds if d.get("audit_overhead")],
            "dispatches": len(ds) - len(work),
            "worker_usage": "Retained per excluded dispatch in dispatch records; absent usage stays null.",
            "root_own_usage": None,
            "root_own_usage_limit": "Root coordinator is not a subagent dispatch. Its own turn tokens were not extracted in this dispatch census; no zero or work-denominator attribution is inferred.",
            "later_audit_workers": "Audit-root descendants are excluded whenever present in the cumulative cohort.",
        },
        "association_limit": "Observational, selected semantic subset. No family ranking, randomized comparison, best-pair policy, inferred non-use from silence, or subscription cost.",
    }
