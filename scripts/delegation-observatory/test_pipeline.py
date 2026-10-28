"""Collector fixtures exercise records and publication, never delegated agent workloads."""

import argparse
import copy
import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import export
import extract
import pipeline
from storage import digest, dump, jsonl, latest, load, load_rows, writer

START = "2026-10-21T14:40:15.993Z"
END = "2026-10-28T14:40:15.993Z"
STARTED = "2026-10-22T00:00:00.000Z"
COMPLETED = "2026-10-22T00:01:00.000Z"


def options(**kwargs):
    return argparse.Namespace(**kwargs)


def fixture_database(path):
    connection = sqlite3.connect(path)
    columns = {
        "threads": "thread_id, project_id, created_at, updated_at, deleted_at, archived_at, payload_json",
        "runs": "run_id, thread_id, ordinal, provider, provider_instance_id, status, requested_at, completed_at, payload_json",
        "subagents": "subagent_id, thread_id, run_id, parent_node_id, child_thread_id, origin, status, started_at, completed_at, updated_at, provider, driver, provider_instance_id, payload_json",
        "nodes": "node_id, thread_id, run_id, parent_node_id, root_node_id, kind",
        "provider_turns": "provider_turn_id, thread_id, node_id, run_attempt_id, status, started_at, completed_at, payload_json",
        "turn_items": "turn_item_id, thread_id, run_id, parent_item_id, type, payload_json",
    }
    for table, names in columns.items():
        connection.execute(
            "CREATE TABLE "
            + extract.P
            + table
            + " ("
            + ",".join(name.strip() + " TEXT" for name in names.split(","))
            + ")"
        )

    def add(table, **values):
        connection.execute(
            "INSERT INTO "
            + extract.P
            + table
            + " ("
            + ",".join(values)
            + ") VALUES ("
            + ",".join("?" for _ in values)
            + ")",
            list(values.values()),
        )

    for thread in ("parent", "child"):
        add(
            "threads",
            thread_id=thread,
            project_id="project",
            created_at=STARTED,
            updated_at=STARTED,
            payload_json=json.dumps({"lineage": None, "prompt": "DO_NOT_PERSIST"}),
        )
    for run, thread, model in (
        ("r1", "parent", "gpt-6.1-sol"),
        ("r2", "child", "muse-wonder-full-1.0"),
    ):
        add(
            "runs",
            run_id=run,
            thread_id=thread,
            ordinal="1",
            provider="test-harness",
            provider_instance_id="fixture",
            status="completed",
            requested_at=STARTED,
            completed_at=COMPLETED,
            payload_json=json.dumps(
                {
                    "startedAt": STARTED,
                    "modelSelection": {
                        "model": model,
                        "options": [
                            {
                                "id": "effort",
                                "value": "high",
                                "secret": "DO_NOT_PERSIST",
                            }
                        ],
                    },
                }
            ),
        )
    add(
        "nodes",
        node_id="n",
        thread_id="parent",
        run_id="r1",
        root_node_id="n",
        kind="provider_turn",
    )
    for dispatch, child in (("d", "child"), ("unknown", None)):
        add(
            "subagents",
            subagent_id=dispatch,
            thread_id="parent",
            run_id="r1",
            parent_node_id="n",
            child_thread_id=child,
            origin="app_owned",
            status="completed",
            started_at=STARTED,
            completed_at=COMPLETED,
            updated_at=COMPLETED,
            provider="fixture",
            driver="fixture",
            provider_instance_id="fixture",
            payload_json=json.dumps(
                {
                    "title": "DO_NOT_PERSIST review",
                    "model": None,
                    "result": "DO_NOT_PERSIST accepted applied",
                }
            ),
        )
        add(
            "turn_items",
            turn_item_id="i" + dispatch,
            thread_id="parent",
            run_id="r1",
            type="subagent",
            payload_json=json.dumps({"subagentId": dispatch, "text": "DO_NOT_PERSIST"}),
        )
    add(
        "provider_turns",
        provider_turn_id="p1",
        thread_id="child",
        node_id="n",
        status="completed",
        started_at=STARTED,
        completed_at=COMPLETED,
        payload_json=json.dumps(
            {
                "turnTokenUsage": {
                    "usageScope": "main_agent",
                    "hasSubagents": True,
                    "usageStatus": "complete",
                    "inputTokens": 100,
                    "outputTokens": 5,
                    "prompt": "DO_NOT_PERSIST",
                }
            }
        ),
    )
    connection.execute("CREATE TABLE authentication (secret TEXT)")
    connection.execute("INSERT INTO authentication VALUES ('DO_NOT_PERSIST')")
    connection.commit()
    connection.close()


class PipelineTests(unittest.TestCase):
    def test_nested_model_setting_never_becomes_exported_content(self):
        with self.assertRaises(ValueError):
            extract.identity(
                run={
                    "selection": {
                        "model": "gpt-6",
                        "options": [
                            {"id": "effort", "value": {"api_key": "SYNTHETIC_SECRET"}}
                        ],
                    }
                }
            )

    def test_rehashed_unknown_export_field_is_rejected(self):
        _, _, envelope = self.generation()
        envelope["unexpected_payload"] = {"api_key": "SYNTHETIC_SECRET"}
        envelope.pop("generation_id")
        envelope["generation_id"] = digest(envelope)[:24]
        with self.assertRaises(ValueError):
            export.validate_envelope(envelope)

    def test_applied_implementation_artifact_stays_in_application_denominator(self):
        from metrics import summarize

        _, _, envelope = self.generation()
        first = envelope["contributions"][0]
        first.update(
            kind="implementation_artifact",
            applied=True,
            artifact_evidence_ids=["decision"],
        )
        second = copy.deepcopy(first)
        second.update(
            contribution_id="second",
            cause_id="second",
            kind="test_coverage",
            discoverers=[{"dispatch_id": "unknown", "child_family": "unknown"}],
        )
        summary = summarize(
            envelope["dispatches"],
            [first, second],
            [],
            envelope["attribution_coverage"],
        )
        self.assertEqual(summary["attribution"]["applied_rate"], 1.0)

    def test_panel_contribution_cannot_come_from_outside_its_roster(self):
        from metrics import summarize

        _, _, envelope = self.generation()
        envelope["contributions"][0]["panel_id"] = "panel"
        envelope["panels"] = [
            {
                "panel_id": "panel",
                "complete": False,
                "dispatch_ids": ["unknown"],
                "evidence_ids": ["decision"],
                "expected_families": ["unknown"],
            }
        ]
        envelope["summary"] = summarize(
            envelope["dispatches"],
            envelope["contributions"],
            envelope["panels"],
            envelope["attribution_coverage"],
        )
        envelope.pop("generation_id")
        envelope["generation_id"] = digest(envelope)[:24]
        with self.assertRaises(ValueError):
            export.validate_envelope(envelope)

    def setUp(self):
        scratch = Path(os.environ["OBSERVATORY_TEST_TMPDIR"])
        scratch.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=scratch)
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.source = self.base / "fixture.sqlite"
        fixture_database(self.source)
        self.seed_dir = self.base / "seed"
        self.seed_dir.mkdir()
        extract.extract(self.seed_dir, self.source, START, END, [pipeline.AUDIT_ROOT])
        self.audit(self.seed_dir)
        self.root = self.base / "data"
        self.root.mkdir()
        pipeline.seed(self.root, options(from_dir=self.seed_dir, owner="test-owner"))

    def audit(self, folder):
        dump(
            folder / "attribution-metadata.json",
            {
                "as_of": "2026-10-28T15:10:16+00:00",
                "selection": "Fixture full reports, including a no-finding report",
            },
        )
        jsonl(
            folder / "attribution-evidence.jsonl",
            [{"evidence_id": "decision", "kind": "reviewed_decision"}],
        )
        jsonl(
            folder / "contributions.jsonl",
            [
                {
                    "contribution_id": "c",
                    "cause_id": "cause",
                    "revision": 1,
                    "panel_id": None,
                    "label": "Supported fixture proposal",
                    "kind": "test_coverage",
                    "severity": "warning",
                    "role": "review",
                    "discoverers": [
                        {
                            "dispatch_id": "d",
                            "child_family": "muse",
                            "evidence_ids": ["decision"],
                        }
                    ],
                    "accepted": True,
                    "applied": None,
                    "disposition": "accepted",
                    "pending": True,
                    "decision_evidence_ids": ["decision"],
                    "artifact_evidence_ids": [],
                    "disposition_as_of": "2026-10-28T15:10:16+00:00",
                }
            ],
        )
        jsonl(
            folder / "attribution-coverage.jsonl",
            [
                {
                    "dispatch_id": ident,
                    "evaluable": True,
                    "revision": 1,
                    "evidence_ids": ["decision"],
                    "disposition_as_of": "2026-10-28T15:10:16+00:00",
                    "reported_findings": 999,
                }
                for ident in ("d", "unknown")
            ],
        )

    def generation(self):
        result = pipeline.aggregate(self.root)
        publication = load(self.root / "latest-generation.json")
        return result, publication, load(self.root / publication["file"])

    def test_seed_and_aggregate_are_idempotent_without_source_reads(self):
        pipeline.seed(self.root, options(from_dir=self.seed_dir, owner="test-owner"))
        with mock.patch(
            "sqlite3.connect", side_effect=AssertionError("Unexpected source read")
        ):
            first, _, _ = self.generation()
            second, _, _ = self.generation()
        self.assertEqual(first, second)
        self.assertEqual(len(list((self.root / "runs").iterdir())), 1)

    def test_unknown_usage_model_and_accepted_application_remain_null(self):
        _, _, envelope = self.generation()
        unknown = next(
            row for row in envelope["dispatches"] if row["dispatch_id"] == "unknown"
        )
        self.assertEqual(unknown["child"]["family"], "unknown")
        self.assertIsNone(unknown["child"]["model"])
        self.assertIsNone(unknown["usage"]["own"]["inputTokens"])
        self.assertIsNone(envelope["contributions"][0]["applied"])
        self.assertEqual(envelope["summary"]["attribution"]["applied_evaluable"], 1)

    def test_judged_no_findings_are_exported_for_filtered_denominators(self):
        _, _, envelope = self.generation()
        self.assertEqual(len(envelope["attribution_coverage"]), 2)
        self.assertEqual(envelope["summary"]["attribution"]["evaluable"], 2)
        self.assertEqual(envelope["summary"]["attribution"]["accepted_rate"], 0.5)
        self.assertNotIn("reported_findings", envelope["attribution_coverage"][0])
        self.assertEqual(envelope["attribution_coverage"][1]["dispatch_id"], "unknown")

    def test_no_raw_text_auth_or_unallowlisted_setting_is_persisted(self):
        for path in self.seed_dir.iterdir():
            self.assertNotIn("DO_NOT_PERSIST", path.read_text())
        _, _, envelope = self.generation()
        self.assertNotIn("DO_NOT_PERSIST", json.dumps(envelope))
        self.assertNotIn("source_file", envelope["source_snapshot"])

    def test_revisions_duplicates_and_conflicts(self):
        first = {"id": "a", "revision": 1, "accepted": None}
        second = {**first, "revision": 2, "accepted": True}
        self.assertEqual(latest([first, first, second, first], "id"), [second])
        with self.assertRaisesRegex(ValueError, "Conflicting equal revision"):
            latest([first, {**first, "accepted": False}], "id")

    def test_source_history_order_and_revision_regression(self):
        original = next(
            row
            for row in load_rows(self.seed_dir / "dispatches.jsonl")
            if row["dispatch_id"] == "unknown"
        )
        original["status_as_of"] = "2026-10-28T15:00:00Z"
        advanced = copy.deepcopy(original)
        advanced.update(
            revision="2026-10-28T16:00:00Z",
            status_as_of="2026-10-28T17:00:00Z",
            source_record_sha256="new-source-hash",
        )
        advanced["child"].update(model="gpt-6.1-sol", family="gpt")
        self.assertEqual(pipeline.choose_dispatches([advanced, original]), [advanced])
        regressed = copy.deepcopy(original)
        regressed["status_as_of"] = "2026-10-28T18:00:00Z"
        with self.assertRaisesRegex(ValueError, "revision regressed"):
            pipeline.choose_dispatches([original, regressed, advanced])

    def test_cli_import_preserves_sol_and_terra_auditor_provenance(self):
        bundle = self.base / "model-judgments"
        bundle.mkdir()
        self.audit(bundle)
        for model in ("gpt-6.1-sol", "gpt-6.1-terra"):
            with self.subTest(model=model):
                result = subprocess.run(
                    [
                        sys.executable,
                        str(Path(pipeline.__file__)),
                        "--data-dir",
                        str(self.root),
                        "import-judgments",
                        "--from-dir",
                        str(bundle),
                        "--owner",
                        "actual-auditor",
                        "--auditor-model",
                        model,
                    ],
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                ident = json.loads(result.stdout)["judgment_bundle"]
                manifest = load(self.root / "judgments" / ident / "manifest.json")
                self.assertEqual(manifest["auditor_model"], model)
                self.assertEqual(manifest["owner"], "actual-auditor")
                audits = pipeline.build(self.root)["disposition_snapshots"]
                self.assertTrue(any(row.get("auditor_model") == model for row in audits))

    def test_judgment_revision_preserves_cause_identity(self):
        bundle = self.base / "judgments"
        bundle.mkdir()
        self.audit(bundle)
        row = load_rows(bundle / "contributions.jsonl")[0]
        row.update(
            revision=2,
            accepted=False,
            applied=False,
            disposition="rejected",
            pending=False,
        )
        jsonl(bundle / "contributions.jsonl", [row])
        pipeline.import_judgments(
            self.root,
            options(from_dir=bundle, owner="sol-auditor", auditor_model="gpt-6.1-sol"),
        )
        self.assertFalse(pipeline.build(self.root)["contributions"][0]["accepted"])
        row.update(revision=3, cause_id="different")
        jsonl(bundle / "contributions.jsonl", [row])
        with self.assertRaisesRegex(ValueError, "Cause identity changed"):
            pipeline.import_judgments(
                self.root,
                options(
                    from_dir=bundle, owner="sol-auditor", auditor_model="gpt-6.1-sol"
                ),
            )

    def test_equal_revision_judgment_conflict_does_not_persist(self):
        bundle = self.base / "conflict"
        bundle.mkdir()
        self.audit(bundle)
        row = load_rows(bundle / "contributions.jsonl")[0]
        row["accepted"] = False
        jsonl(bundle / "contributions.jsonl", [row])
        with self.assertRaisesRegex(ValueError, "Conflicting equal revision"):
            pipeline.import_judgments(
                self.root,
                options(
                    from_dir=bundle, owner="sol-auditor", auditor_model="gpt-6.1-sol"
                ),
            )
        self.assertFalse((self.root / "judgments").exists())

    def test_collect_creates_no_new_attribution_and_refreshes_status(self):
        connection = sqlite3.connect(self.source)
        connection.execute(
            "UPDATE "
            + extract.P
            + "subagents SET status='failed', updated_at=? WHERE subagent_id='unknown'",
            ("2026-10-28T16:00:00.000Z",),
        )
        connection.commit()
        connection.close()
        result = pipeline.collect(
            self.root, options(source=self.source, since=START, until=END)
        )
        self.assertEqual(result["judgments_created"], 0)
        envelope = pipeline.build(self.root)
        self.assertEqual(len(envelope["contributions"]), 1)
        self.assertEqual(
            next(
                row for row in envelope["dispatches"] if row["dispatch_id"] == "unknown"
            )["status"],
            "failed",
        )
        self.assertIn(
            "unknown",
            load(self.root / "review-queue.json")[
                "changed_since_inspection_dispatch_ids"
            ],
        )

    def test_cumulative_source_loss_preserves_prior_and_records_discrepancy(self):
        before = pipeline.build(self.root)
        connection = sqlite3.connect(self.source)
        connection.execute(
            "DELETE FROM " + extract.P + "subagents WHERE subagent_id='unknown'"
        )
        connection.commit()
        connection.close()
        with self.assertRaisesRegex(ValueError, "missing from cumulative source"):
            pipeline.collect(
                self.root, options(source=self.source, since=START, until=END)
            )
        self.assertEqual(pipeline.build(self.root), before)
        self.assertEqual(len(list((self.root / "discrepancy-runs").iterdir())), 1)

    def test_source_identity_change_is_refused(self):
        connection = sqlite3.connect(self.source)
        connection.execute(
            "UPDATE "
            + extract.P
            + "subagents SET child_thread_id='other', updated_at=? WHERE subagent_id='d'",
            ("2026-10-28T16:00:00.000Z",),
        )
        connection.commit()
        connection.close()
        with self.assertRaisesRegex(ValueError, "Source identity discrepancy"):
            pipeline.collect(
                self.root, options(source=self.source, since=START, until=END)
            )

    def test_replaced_source_file_with_same_records_is_refused(self):
        replacement = self.base / "replacement.sqlite"
        shutil.copyfile(self.source, replacement)
        os.replace(replacement, self.source)
        with self.assertRaisesRegex(ValueError, "file identity changed"):
            pipeline.collect(
                self.root, options(source=self.source, since=START, until=END)
            )

    def test_collect_does_not_infer_attribution_from_result_text(self):
        jsonl(self.seed_dir / "contributions.jsonl", [])
        jsonl(self.seed_dir / "attribution-coverage.jsonl", [])
        root = self.base / "unjudged"
        root.mkdir()
        pipeline.seed(root, options(from_dir=self.seed_dir, owner="test-owner"))
        pipeline.collect(root, options(source=self.source, since=START, until=END))
        envelope = pipeline.build(root)
        self.assertEqual(envelope["contributions"], [])
        self.assertEqual(envelope["attribution_coverage"], [])
        self.assertIsNone(envelope["summary"]["attribution"]["accepted_rate"])

    def test_new_audit_root_is_persisted_and_excluded_from_work(self):
        pipeline.collect(
            self.root,
            options(source=self.source, since=START, until=END, audit_root=["parent"]),
        )
        self.assertIn("parent", load(self.root / "owner.json")["audit_root_ids"])
        self.assertEqual(
            pipeline.build(self.root)["summary"]["dispatch_totals"]["work"], 0
        )

    def test_changed_seed_cannot_replace_historical_judgments(self):
        jsonl(self.seed_dir / "contributions.jsonl", [])
        with self.assertRaisesRegex(ValueError, "only its original seed"):
            pipeline.seed(
                self.root, options(from_dir=self.seed_dir, owner="test-owner")
            )
        self.assertEqual(len(pipeline.build(self.root)["contributions"]), 1)

    def test_rolling_bound_refused_and_old_judgments_retained(self):
        with self.assertRaisesRegex(ValueError, "cumulative cohort start"):
            pipeline.collect(
                self.root,
                options(source=self.source, since="2026-10-27T00:00:00Z", until=END),
            )
        self.assertEqual(len(pipeline.build(self.root)["attribution_coverage"]), 2)

    def test_source_snapshot_metadata_exact_inclusive_bounds_and_empty_window(self):
        snapshot = load(self.seed_dir / "source-metadata.json")
        self.assertEqual(
            snapshot["window"], {"start_inclusive": START, "end_inclusive": END}
        )
        self.assertEqual(snapshot["cohort_rows"], 2)
        self.assertEqual(snapshot["cohort_min_started_at"], STARTED)
        self.assertIn("mode=ro", snapshot["transaction"])
        self.assertIn("query_only=ON", snapshot["transaction"])
        empty = self.base / "empty"
        empty.mkdir()
        extract.extract(empty, self.source, END, END, [])
        self.assertEqual(load_rows(empty / "dispatches.jsonl"), [])
        self.assertIsNone(load(empty / "source-metadata.json")["cohort_min_started_at"])
        exact = self.base / "exact"
        exact.mkdir()
        extract.extract(exact, self.source, STARTED, STARTED, [])
        self.assertEqual(len(load_rows(exact / "dispatches.jsonl")), 2)

    def test_publication_digest_schema_and_traversal_are_checked(self):
        _, publication, _ = self.generation()
        export.validate_publication(self.root, publication)
        for key, value in (
            ("sha256", "0" * 64),
            ("schema_version", "future"),
            ("file", "../dashboard.json"),
            ("generation_id", "../bad"),
        ):
            with self.subTest(key=key), self.assertRaises(ValueError):
                export.validate_publication(self.root, {**publication, key: value})

    def rehash(self, envelope):
        envelope.pop("generation_id")
        envelope["generation_id"] = digest(envelope)[:24]
        return envelope

    def test_reference_and_applied_proof_invariants(self):
        _, _, original = self.generation()
        for changes, error in (
            ({"panel_id": "missing-panel"}, "unknown panel"),
            ({"decision_evidence_ids": ["missing-proof"]}, "Unresolved"),
            ({"applied": True}, "artifact proof"),
        ):
            envelope = copy.deepcopy(original)
            envelope["contributions"][0].update(changes)
            with self.subTest(changes=changes), self.assertRaisesRegex(
                ValueError, error
            ):
                export.validate_envelope(self.rehash(envelope))

    def test_application_exclusions_and_source_boundaries_are_validated(self):
        _, _, original = self.generation()
        envelope = copy.deepcopy(original)
        envelope["attribution_coverage"][0]["application_unknown"] = False
        with self.assertRaisesRegex(ValueError, "coverage exclusion mismatch"):
            export.validate_envelope(self.rehash(envelope))
        envelope = copy.deepcopy(original)
        envelope["source_snapshot"]["cohort_rows"] += 1
        with self.assertRaisesRegex(ValueError, "boundary/count mismatch"):
            export.validate_envelope(self.rehash(envelope))

    def test_validation_failure_cannot_replace_previous_publication(self):
        _, publication, _ = self.generation()
        target = self.base / "published"
        args = options(publish_dir=target, generation=None)
        pipeline.publish(self.root, args)
        before = (target / "publication.json").read_bytes()
        path = self.root / publication["file"]
        path.write_bytes(path.read_bytes() + b"corrupt")
        with self.assertRaisesRegex(ValueError, "SHA256 mismatch"):
            pipeline.publish(self.root, args)
        self.assertEqual((target / "publication.json").read_bytes(), before)

    def test_publish_idempotence_and_symlink_refusal(self):
        self.generation()
        target = self.base / "published"
        args = options(publish_dir=target, generation=None)
        first = pipeline.publish(self.root, args)
        second = pipeline.publish(self.root, args)
        self.assertEqual(first, second)
        export.validate_publication(target, load(target / "publication.json"))
        linked = self.base / "linked"
        linked.symlink_to(target, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "Symlink"):
            pipeline.publish(self.root, options(publish_dir=linked, generation=None))

    def test_real_nonblocking_writer_excludes_a_second_process(self):
        code = "from pathlib import Path; from storage import writer; import sys\nwith writer(Path(sys.argv[1])): pass"
        with writer(self.root):
            process = subprocess.run(
                [sys.executable, "-c", code, str(self.root)],
                cwd=Path(__file__).parent,
                check=False,
                capture_output=True,
                text=True,
            )
        self.assertNotEqual(process.returncode, 0)
        self.assertIn("Another observatory writer", process.stderr)
        with writer(self.root):
            pass

    def test_missing_data_or_publication_parent_is_not_created(self):
        missing = self.base / "missing-volume" / "data"
        with self.assertRaisesRegex(ValueError, "does not exist"), writer(missing):
            pass
        self.assertFalse(missing.parent.exists())
        self.generation()
        with self.assertRaisesRegex(ValueError, "does not exist"):
            pipeline.publish(self.root, options(publish_dir=missing, generation=None))
        self.assertFalse(missing.parent.exists())

    def test_source_mode_is_readonly_and_auth_tables_are_never_read(self):
        original_connect = sqlite3.connect
        observed = []

        def guarded_connect(database_uri, **kwargs):
            self.assertIn("mode=ro", database_uri)
            self.assertTrue(kwargs["uri"])
            connection = original_connect(database_uri, **kwargs)
            with self.assertRaises(sqlite3.OperationalError):
                connection.execute("CREATE TABLE refused_write (value TEXT)")

            def authorizer(action, name, *_):
                if action == sqlite3.SQLITE_READ:
                    observed.append(name)
                    if name == "authentication":
                        return sqlite3.SQLITE_DENY
                return sqlite3.SQLITE_OK

            connection.set_authorizer(authorizer)
            return connection

        output = self.base / "readonly-proof"
        output.mkdir()
        before = self.source.read_bytes()
        with mock.patch("extract.sqlite3.connect", side_effect=guarded_connect):
            extract.extract(output, self.source, START, END, [])
        self.assertEqual(self.source.read_bytes(), before)
        self.assertNotIn("authentication", observed)

    def test_panel_shared_and_exclusive_and_incomplete_semantics(self):
        from metrics import panel_metric

        panel = {
            "panel_id": "p",
            "complete": True,
            "expected_families": ["muse", "claude", "gpt"],
        }
        contribution = {
            "contribution_id": "shared",
            "panel_id": "p",
            "accepted": True,
            "discoverers": [{"child_family": "muse"}, {"child_family": "claude"}],
        }
        metric = panel_metric(panel, [contribution])
        self.assertEqual(metric["observed_union"], 1)
        self.assertEqual(
            metric["families"]["muse"]["observed_union_lost_without_family"], 0
        )
        self.assertIsNone(
            panel_metric({**panel, "complete": False}, [contribution])["observed_union"]
        )

    def test_legacy_sparse_annotations_merge_but_conflicts_fail(self):
        merged = pipeline.normalize_legacy_annotations(
            [
                {"dispatch_id": "d", "role": "review", "evidence_ids": ["a"]},
                {"dispatch_id": "d", "retry_of": "old", "evidence_ids": ["b"]},
            ]
        )
        self.assertEqual(merged[0]["evidence_ids"], ["a", "b"])
        with self.assertRaisesRegex(ValueError, "Conflicting legacy"):
            pipeline.normalize_legacy_annotations(
                [
                    {"dispatch_id": "d", "role": "review"},
                    {"dispatch_id": "d", "role": "implementation"},
                ]
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
