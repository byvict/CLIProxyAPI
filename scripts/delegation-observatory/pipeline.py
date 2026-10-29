#!/usr/bin/env python3
"""Collect real T3 facts, import audited judgments, aggregate offline, publish atomically."""

import argparse
import datetime as dt
import json
import os
import shutil
import sqlite3
import sys
import tempfile
from pathlib import Path

from export import has_tier0, public_envelope, validate_publication, without_tier0
from extract import extract
from storage import (
    VERSION,
    atomic_bytes,
    canonical,
    digest,
    dump,
    file_digest,
    immutable_bytes,
    latest,
    load,
    load_rows,
    regular_file,
    safe_directory,
    timestamp,
    writer,
)

AUDIT_ROOT = "8d182f3f-c6f5-486e-9712-555611a2a7b2"
FACT_FILES = (
    "dispatches.jsonl",
    "dispatch-evidence.jsonl",
    "source-metadata.json",
    "schema-source.json",
    "lineage-source.json",
    "usage-source.jsonl",
)
JUDGMENT_FILES = (
    "contributions.jsonl",
    "panels.jsonl",
    "attribution-coverage.jsonl",
    "dispatch-annotations.jsonl",
    "attribution-evidence.jsonl",
    "attribution-metadata.json",
    "api-reconciliation.json",
)
IDENTITY_FIELDS = (
    "environment",
    "project_id",
    "root_thread_id",
    "dispatcher_thread_id",
    "dispatcher_run_id",
    "parent_node_id",
    "child_thread_id",
    "origin",
    "started_at",
)
ANNOTATION_FIELDS = (
    "role",
    "role_basis",
    "skill",
    "panel_id",
    "round",
    "source_revision",
    "source_scope",
    "retry_of",
    "retry_status",
    "evidence_ids",
)


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def bundles(root, folder):
    parent = root / folder
    if not parent.exists():
        return []
    safe_directory(parent)
    result = []
    for directory in sorted(parent.iterdir()):
        safe_directory(directory)
        manifest = load(regular_file(directory / "manifest.json"))
        if (
            directory.name
            != digest(
                {key: value for key, value in manifest.items() if key != "bundle_id"}
            )[:24]
        ):
            raise ValueError("Immutable bundle manifest hash differs")
        if manifest["bundle_id"] != directory.name:
            raise ValueError("Bundle identity mismatch")
        for name, checksum in manifest["files"].items():
            if name not in FACT_FILES + JUDGMENT_FILES:
                raise ValueError("Unrecognized bundle file")
            if file_digest(regular_file(directory / name)) != checksum:
                raise ValueError("Preserved bundle file hash differs: " + name)
        result.append((directory, manifest))
    return result


def commit_bundle(root, stage, folder, kind, owner=None, auditor=None):
    manifest = {
        "schema_version": VERSION,
        "kind": kind,
        "files": {path.name: file_digest(path) for path in sorted(stage.iterdir())},
    }
    if owner:
        manifest["owner"] = owner
    if auditor:
        manifest["auditor_model"] = auditor
    ident = digest(manifest)[:24]
    manifest["bundle_id"] = ident
    dump(stage / "manifest.json", manifest)
    target = safe_directory(root / folder, create=True) / ident
    if target.exists() or target.is_symlink():
        safe_directory(target)
        if load(regular_file(target / "manifest.json")) != manifest:
            raise ValueError("Conflicting immutable bundle")
        for name, checksum in manifest["files"].items():
            if file_digest(regular_file(target / name)) != checksum:
                raise ValueError("Conflicting immutable bundle file")
        shutil.rmtree(stage)
    else:
        os.rename(stage, target)
    return ident


def copy_inputs(source, stage, names, required=()):
    safe_directory(source)
    for name in names:
        path = source / name
        if path.exists() or name in required:
            shutil.copyfile(regular_file(path), stage / name)


def stage_directory(root):
    return Path(tempfile.mkdtemp(prefix=".staging-", dir=root))


def metadata(root):
    return load(regular_file(root / "owner.json"))


def choose_dispatches(rows):
    grouped = {}
    for row in sorted(
        rows, key=lambda row: (timestamp(row["status_as_of"]), row["dispatch_id"])
    ):
        ident = row["dispatch_id"]
        previous = grouped.get(ident)
        if previous:
            if timestamp(row["revision"]) < timestamp(previous["revision"]):
                raise ValueError("Source revision regressed: " + ident)
            for key in IDENTITY_FIELDS:
                if previous.get(key) != row.get(key):
                    raise ValueError(
                        "Source identity discrepancy: " + ident + " / " + key
                    )
            if (
                row["revision"] == previous["revision"]
                and row["source_record_sha256"] != previous["source_record_sha256"]
            ):
                raise ValueError("Source equal-revision discrepancy: " + ident)
            for label in ("root", "dispatcher", "child"):
                before, after = previous[label], row[label]
                for key in (
                    "run_id",
                    "model",
                    "provider_instance_id",
                    "harness",
                    "settings",
                ):
                    if before.get(key) is not None and before[key] != after.get(key):
                        raise ValueError(
                            "Historical identity discrepancy: "
                            + ident
                            + " / "
                            + label
                            + "."
                            + key
                        )
        if previous is None or (
            timestamp(row["revision"]),
            timestamp(row["status_as_of"]),
        ) > (timestamp(previous["revision"]), timestamp(previous["status_as_of"])):
            grouped[ident] = row
        elif (row["revision"], row["status_as_of"]) == (
            previous["revision"],
            previous["status_as_of"],
        ) and row != previous:
            raise ValueError("Conflicting snapshot dispatch: " + ident)
    return [grouped[key] for key in sorted(grouped)]


def evidence_latest(rows):
    grouped = {}
    for row in rows:
        prior = grouped.get(row["evidence_id"])
        if prior is None:
            grouped[row["evidence_id"]] = row
            continue
        if row == prior:
            continue
        if row.get("kind") == prior.get("kind") == "sqlite_projection":
            for key in (
                "source_id",
                "thread_id",
                "run_id",
                "child_thread_id",
                "started_at",
            ):
                if row.get(key) != prior.get(key):
                    raise ValueError("Source evidence identity discrepancy")
            if timestamp(row["observed_at"]) > timestamp(prior["observed_at"]):
                grouped[row["evidence_id"]] = row
            elif timestamp(row["observed_at"]) == timestamp(prior["observed_at"]):
                raise ValueError("Conflicting equal-observation source evidence")
        else:
            selected = latest([prior, row], "evidence_id")
            grouped[row["evidence_id"]] = selected[0]
    return [grouped[key] for key in sorted(grouped)]


def records(root, extra_run=None, extra_judgment=None):
    runs = [path for path, _ in bundles(root, "runs")]
    judgments = [path for path, _ in bundles(root, "judgments")]
    if extra_run:
        runs.append(extra_run)
    if extra_judgment:
        judgments.append(extra_judgment)
    all_judgments = runs + judgments
    dispatches = choose_dispatches(
        [row for path in runs for row in load_rows(path / "dispatches.jsonl")]
    )
    index = {row["dispatch_id"]: row for row in dispatches}
    selected = {}
    for filename, key in (
        ("contributions.jsonl", "contribution_id"),
        ("panels.jsonl", "panel_id"),
        ("attribution-coverage.jsonl", "dispatch_id"),
        ("dispatch-annotations.jsonl", "dispatch_id"),
    ):
        rows = []
        for path in all_judgments:
            incoming = load_rows(path / filename)
            if filename == "dispatch-annotations.jsonl":
                incoming = normalize_legacy_annotations(incoming)
            rows.extend(incoming)
        for row in rows:
            row.setdefault("revision", 1)
            if type(row["revision"]) is not int or row["revision"] < 1:
                raise ValueError("Judgment revisions must be positive integers")
        if filename == "contributions.jsonl":
            causes = {}
            for row in rows:
                if (
                    row["contribution_id"] in causes
                    and causes[row["contribution_id"]] != row["cause_id"]
                ):
                    raise ValueError("Cause identity changed across judgment revisions")
                causes[row["contribution_id"]] = row["cause_id"]
        selected[filename] = latest(rows, key)
    for annotation in selected["dispatch-annotations.jsonl"]:
        if annotation["dispatch_id"] not in index:
            raise ValueError("Annotation refers to unknown dispatch")
        dispatch = index[annotation["dispatch_id"]]
        for key in ANNOTATION_FIELDS:
            if key in annotation:
                dispatch[key] = (
                    sorted(set(dispatch[key] + annotation[key]))
                    if key == "evidence_ids"
                    else annotation[key]
                )
    evidence = [
        row for path in runs for row in load_rows(path / "dispatch-evidence.jsonl")
    ]
    evidence += [
        row
        for path in all_judgments
        for row in load_rows(path / "attribution-evidence.jsonl")
    ]
    evidence += [
        row
        for path in all_judgments
        if (path / "api-reconciliation.json").exists()
        for row in load(path / "api-reconciliation.json")
    ]
    sources = sorted(
        [load(path / "source-metadata.json") for path in runs],
        key=lambda row: timestamp(row["as_of"]),
    )
    audits = []
    for path in all_judgments:
        if (path / "attribution-metadata.json").exists():
            audit = load(path / "attribution-metadata.json")
            if (path / "manifest.json").exists():
                manifest = load(path / "manifest.json")
                audit.update(
                    {
                        key: manifest[key]
                        for key in ("owner", "auditor_model", "bundle_id")
                        if key in manifest
                    }
                )
            audits.append(audit)
    audits.sort(key=lambda row: timestamp(row["as_of"]))
    return (
        dispatches,
        selected["contributions.jsonl"],
        selected["panels.jsonl"],
        selected["attribution-coverage.jsonl"],
        evidence_latest(evidence),
        sources,
        audits,
    )


def normalize_legacy_annotations(rows):
    legacy = {}
    revisioned = []
    for row in rows:
        if "revision" in row:
            revisioned.append(row)
            continue
        merged = legacy.setdefault(
            row["dispatch_id"], {"dispatch_id": row["dispatch_id"]}
        )
        for key, value in row.items():
            if key == "evidence_ids":
                merged[key] = sorted(set(merged.get(key, []) + value))
            elif key in merged and merged[key] != value:
                raise ValueError("Conflicting legacy annotation field: " + key)
            else:
                merged[key] = value
    return list(legacy.values()) + revisioned


def build(root, extra_run=None, extra_judgment=None):
    return public_envelope(*records(root, extra_run, extra_judgment))


def check_evidence_files(stage):
    for row in load_rows(stage / "attribution-evidence.jsonl"):
        if row.get("local_path") and file_digest(
            regular_file(Path(row["local_path"]))
        ) != row.get("sha256"):
            raise ValueError("Preserved evidence hash differs: " + row["evidence_id"])


def seed(root, args):
    stage = stage_directory(root)
    try:
        copy_inputs(
            args.from_dir,
            stage,
            FACT_FILES + JUDGMENT_FILES,
            required=(
                "dispatches.jsonl",
                "dispatch-evidence.jsonl",
                "source-metadata.json",
                "contributions.jsonl",
                "attribution-coverage.jsonl",
                "attribution-metadata.json",
            ),
        )
        check_evidence_files(stage)
        source = load(stage / "source-metadata.json")
        if (root / "owner.json").exists():
            owner = metadata(root)
            if owner["since"] != source["window"]["start_inclusive"]:
                raise ValueError("Seed cohort start differs from persistent data root")
            if source["source_file"] != owner["source_file"]:
                raise ValueError(
                    "Seed source identity differs from persistent data root"
                )
            original = load(
                regular_file(root / "runs" / owner["seed_bundle"] / "manifest.json")
            )
            current_files = {path.name: file_digest(path) for path in stage.iterdir()}
            if original["files"] != current_files:
                raise ValueError(
                    "A persistent root accepts only its original seed; import new judgments separately"
                )
            return {"seed_bundle": owner["seed_bundle"], "owner": owner["owner"]}
        build(root, extra_run=stage)
        bundle_id = commit_bundle(
            root, stage, "runs", "seed", args.owner, "gpt-6.1-sol"
        )
        if not (root / "owner.json").exists():
            owner = {
                "schema_version": VERSION,
                "owner": args.owner,
                "since": source["window"]["start_inclusive"],
                "source_file": source["source_file"],
                "audit_root_ids": [AUDIT_ROOT],
                "created_at": now(),
                "seed_bundle": bundle_id,
            }
            atomic_bytes(root / "owner.json", canonical(owner) + b"\n")
        atomic_bytes(root / "review-queue.json", canonical(audit_queue(root)) + b"\n")
        return {"seed_bundle": bundle_id, "owner": metadata(root)["owner"]}
    finally:
        if stage.exists():
            shutil.rmtree(stage)


def collect(root, args):
    owner = metadata(root)
    if args.since != owner["since"]:
        raise ValueError(
            "Use the persistent cumulative cohort start: " + owner["since"]
        )
    if timestamp(args.since) > timestamp(args.until):
        raise ValueError("Reversed collection bounds")
    source_path = args.source.expanduser().absolute()
    if str(source_path) != owner["source_file"]:
        raise ValueError(
            "Source database path changed; preserve this root and reconcile identity explicitly"
        )
    previous = records(root)
    last_source = previous[5][-1]
    if timestamp(args.until) < timestamp(last_source["window"]["end_inclusive"]):
        raise ValueError("A cumulative collection cannot shorten the previous cohort")
    stage = stage_directory(root)
    audit_roots = sorted(set(owner["audit_root_ids"] + getattr(args, "audit_root", [])))
    try:
        extract(
            stage,
            regular_file(source_path),
            args.since,
            args.until,
            audit_roots,
        )
        current_source = load(stage / "source-metadata.json")
        if (
            last_source.get("source_file_identity")
            and current_source.get("source_file_identity")
            != last_source["source_file_identity"]
        ):
            raise ValueError(
                "Source database file identity changed; reconcile replacement before collection"
            )
        incoming = load_rows(stage / "dispatches.jsonl")
        incoming_ids = {row["dispatch_id"] for row in incoming}
        missing = sorted(
            row["dispatch_id"]
            for row in previous[0]
            if row["dispatch_id"] not in incoming_ids
        )
        if missing:
            raise ValueError(
                "Previously preserved dispatches missing from cumulative source: "
                + json.dumps(missing)
            )
        choose_dispatches(previous[0] + incoming)
        build(root, extra_run=stage)
        bundle_id = commit_bundle(root, stage, "runs", "collection", owner["owner"])
        if audit_roots != owner["audit_root_ids"]:
            owner["audit_root_ids"] = audit_roots
            atomic_bytes(root / "owner.json", canonical(owner) + b"\n")
        queue = audit_queue(root)
        atomic_bytes(root / "review-queue.json", canonical(queue) + b"\n")
        return {
            "collection_bundle": bundle_id,
            "dispatches": len(incoming),
            "unreviewed_dispatches": len(queue["unreviewed_dispatch_ids"]),
            "judgments_created": 0,
        }
    except (ValueError, KeyError, TypeError) as exc:
        if (stage / "source-metadata.json").exists():
            failure = {
                "error": str(exc),
                "source_snapshot": load(stage / "source-metadata.json"),
            }
            atomic_bytes(
                safe_directory(root / "discrepancies", create=True)
                / (digest(failure)[:24] + ".json"),
                canonical(failure) + b"\n",
            )
            commit_bundle(
                root, stage, "discrepancy-runs", "source_discrepancy", owner["owner"]
            )
        raise
    finally:
        if stage.exists():
            shutil.rmtree(stage)


def audit_queue(root):
    dispatches, contributions, _, coverage, _, sources, _ = records(root)
    evaluated = {row["dispatch_id"] for row in coverage if row.get("evaluable") is True}
    inspection_times = {
        row["dispatch_id"]: row.get("disposition_as_of") for row in coverage
    }
    snapshots = sorted(
        (path for path, _ in bundles(root, "runs")),
        key=lambda path: timestamp(load(path / "source-metadata.json")["as_of"]),
    )
    current = {
        row["dispatch_id"]: row for row in load_rows(snapshots[-1] / "dispatches.jsonl")
    }
    prior = (
        {
            row["dispatch_id"]: row
            for row in load_rows(snapshots[-2] / "dispatches.jsonl")
        }
        if len(snapshots) > 1
        else current
    )

    def factual(row):
        return {key: value for key, value in row.items() if key != "status_as_of"}

    return {
        "schema_version": VERSION,
        "source_as_of": sources[-1]["as_of"],
        "previous_source_as_of": sources[-2]["as_of"] if len(sources) > 1 else None,
        "new_dispatch_ids": sorted(set(current) - set(prior)),
        "source_changed_dispatch_ids": sorted(
            ident
            for ident in current.keys() & prior.keys()
            if factual(current[ident]) != factual(prior[ident])
        ),
        "unreviewed_dispatch_ids": [
            row["dispatch_id"]
            for row in dispatches
            if not row.get("audit_overhead") and row["dispatch_id"] not in evaluated
        ],
        "changed_since_inspection_dispatch_ids": [
            row["dispatch_id"]
            for row in dispatches
            if inspection_times.get(row["dispatch_id"])
            and timestamp(row["revision"])
            > timestamp(inspection_times[row["dispatch_id"]])
        ],
        "application_pending_contribution_ids": [
            row["contribution_id"]
            for row in contributions
            if row.get("accepted") is True
            and row.get("applied") is None
            and not row["kind"].endswith("_artifact")
        ],
        "limits": "Factual queue only. An auditor must inspect exact evidence and import revisioned judgments. No inferred non-use.",
    }


def import_judgments(root, args):
    metadata(root)
    stage = stage_directory(root)
    try:
        copy_inputs(
            args.from_dir,
            stage,
            JUDGMENT_FILES,
            required=("attribution-metadata.json",),
        )
        check_evidence_files(stage)
        audit = load(stage / "attribution-metadata.json")
        timestamp(audit["as_of"])
        if not audit.get("selection"):
            raise ValueError("Judgment scope must be explicit")
        build(root, extra_judgment=stage)
        ident = commit_bundle(
            root, stage, "judgments", "judgment", args.owner, args.auditor_model
        )
        atomic_bytes(root / "review-queue.json", canonical(audit_queue(root)) + b"\n")
        return {"judgment_bundle": ident}
    finally:
        if stage.exists():
            shutil.rmtree(stage)


def aggregate(root, args=None):
    metadata(root)
    envelope = build(root)
    content = (
        json.dumps(
            envelope, indent=2, sort_keys=True, ensure_ascii=False, allow_nan=False
        ).encode()
        + b"\n"
    )
    ident = envelope["generation_id"]
    immutable_bytes(root / "export" / ident / "dashboard.json", content)
    publication = {
        key: envelope[key]
        for key in (
            "schema_version",
            "generation_id",
            "window",
            "source_as_of",
            "disposition_as_of",
        )
    }
    publication.update(
        status="published",
        file="export/" + ident + "/dashboard.json",
        sha256=file_digest(root / "export" / ident / "dashboard.json"),
    )
    validate_publication(root, publication)
    immutable_bytes(
        root / "export" / ident / "publication.json", canonical(publication) + b"\n"
    )
    atomic_bytes(root / "latest-generation.json", canonical(publication) + b"\n")
    totals = envelope["summary"]["attribution"]
    return {
        "generation_id": ident,
        "attribution": {
            key: totals[key]
            for key in (
                "evaluable",
                "contribution_clusters",
                "disposition_counts",
                "accepted_dispatches",
                "applied_contributions",
                "applied_dispatches",
                "applied_evaluable",
            )
        },
        "dispatch_totals": envelope["summary"]["dispatch_totals"],
    }


def selected_publication(root, generation=None):
    if generation:
        from storage import GENERATION

        if not GENERATION.fullmatch(generation):
            raise ValueError("Invalid generation ID")
        return load(regular_file(root / "export" / generation / "publication.json"))
    return load(regular_file(root / "latest-generation.json"))


def publish(root, args):
    publication = selected_publication(root, args.generation)
    validate_publication(root, publication)
    safe_directory(args.publish_dir.expanduser().absolute().parent)
    target = safe_directory(args.publish_dir, create=True)
    content = (root / publication["file"]).read_bytes()
    immutable_bytes(target / publication["file"], content)
    validate_publication(target, publication)
    atomic_bytes(target / "publication.json", canonical(publication) + b"\n")
    return {
        "publication": str(target / "publication.json"),
        "generation_id": publication["generation_id"],
        "sha256": publication["sha256"],
    }


def validate(root, args):
    publication = selected_publication(root, args.generation)
    envelope = validate_publication(root, publication)
    current = build(root)
    if envelope != (current if has_tier0(envelope) else without_tier0(current)):
        raise ValueError(
            "Selected generation is valid but not current; aggregate the latest preserved inputs"
        )
    return {"valid": True, "generation_id": envelope["generation_id"]}


def parser():
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--data-dir", required=True, type=Path)
    commands = result.add_subparsers(dest="command", required=True)
    seed_parser = commands.add_parser(
        "seed", help="Import the verified immutable initial collection"
    )
    seed_parser.add_argument("--from-dir", required=True, type=Path)
    seed_parser.add_argument("--owner", required=True)
    collector = commands.add_parser(
        "collect", help="Read cumulative factual metadata; create no judgments"
    )
    collector.add_argument(
        "--source", type=Path, default=Path.home() / ".t3/userdata/statev2.sqlite"
    )
    collector.add_argument("--since", required=True)
    collector.add_argument("--until", required=True)
    collector.add_argument(
        "--audit-root",
        action="append",
        default=[],
        help="Persist another confirmed observatory coordinator root as overhead",
    )
    importer = commands.add_parser(
        "import-judgments", help="Import reviewed, revisioned judgments"
    )
    importer.add_argument("--from-dir", required=True, type=Path)
    importer.add_argument("--owner", required=True)
    importer.add_argument(
        "--auditor-model", choices=["gpt-6.1-sol", "gpt-6.1-terra"], required=True
    )
    commands.add_parser(
        "aggregate", help="Build an immutable generation without reading SQLite"
    )
    for name in ("validate", "publish"):
        subparser = commands.add_parser(name)
        subparser.add_argument("--generation")
        if name == "publish":
            subparser.add_argument("--publish-dir", required=True, type=Path)
    return result


def main():
    args = parser().parse_args()
    operations = {
        "seed": seed,
        "collect": collect,
        "import-judgments": import_judgments,
        "aggregate": aggregate,
        "validate": validate,
        "publish": publish,
    }
    try:
        with writer(args.data_dir) as root:
            print(
                json.dumps(
                    operations[args.command](root, args),
                    sort_keys=True,
                    ensure_ascii=False,
                )
            )
    except (ValueError, KeyError, OSError, TypeError, sqlite3.Error) as exc:
        print("Observatory operation refused: " + str(exc), file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
