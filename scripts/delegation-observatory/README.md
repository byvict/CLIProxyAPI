# Real T3 delegation observatory pipeline

Python 3.9+ standard library only, on macOS or Linux (`fcntl`). This pipeline reads real persisted T3 dispatch metadata and imports separately audited judgments. It never executes agent work, reads authentication tables, or infers acceptance/application from result text. `extract.py` and `metrics.py` retain the verified collection's extraction and aggregation rules; `curate.py` is deliberately not a generic auditor.

Run from the repository root. Set `OBSERVATORY_DATA_DIR` to the persistent private data directory on the external SSD, `OBSERVATORY_SEED_DIR` to the preserved verified collection, and `CLIPROXY_CONFIG_DIR` to the directory containing the proxy configuration. All directory paths must be canonical, with no symlink components. The data and publication parent directories must already exist. The SSD must already be mounted; do not create a replacement mount or an internal-disk fallback.

```sh
PIPELINE="$PWD/scripts/delegation-observatory/pipeline.py"
python3 "$PIPELINE" --data-dir "$OBSERVATORY_DATA_DIR" seed \
  --from-dir "$OBSERVATORY_SEED_DIR" --owner t3-delegation-observatory
python3 "$PIPELINE" --data-dir "$OBSERVATORY_DATA_DIR" aggregate
python3 "$PIPELINE" --data-dir "$OBSERVATORY_DATA_DIR" validate
python3 "$PIPELINE" --data-dir "$OBSERVATORY_DATA_DIR" publish \
  --publish-dir "$CLIPROXY_CONFIG_DIR/delegation-observatory"
```

`seed` copies allowlisted factual and judgment files byte for byte, verifies referenced private evidence files against their hashes, and records a hashed manifest. Original evidence remains in its original location. Raw reports/transcripts are not recopied. Reimporting the identical seed is a no-op; changed judgments use `import-judgments`. The initial unrevisioned sparse dispatch annotations are combined only where fields agree, with evidence IDs unioned; conflicts fail. Original bytes remain preserved. The legacy `reported_findings` field is private, unused, and never exported: its original collection sometimes counted object keys instead of findings.

## Daily procedure

Attach these steps to the existing T3 routine at **09:00 America/Sao_Paulo**. Scheduling and dispatching the Sol auditor remain the routine owner's responsibility; this CLI does not create a timer or dispatch models.

1. Read the routine's state and referenced manifest. Verify its previous collection/audit owner is finished; an unknown owner status retains that ownership. Confirm the SSD is mounted. A competing CLI writer is refused immediately by the kernel lock; do not remove a lock file to bypass an active writer.
2. Choose one fixed UTC tick end and retain it in the tick record. Collect the cumulative interval using the original start. The source defaults to `$HOME/.t3/userdata/statev2.sqlite`; `--source` accepts an explicit read-only database path for fixtures. A source-path change is refused after initialization.

   ```sh
   TICK_END="$(date -u '+%Y-%m-%dT%H:%M:%S.000Z')"
   python3 "$PIPELINE" --data-dir "$OBSERVATORY_DATA_DIR" collect \
     --since 2026-10-21T14:40:15.993Z --until "$TICK_END"
   ```

   If this tick uses a new observatory coordinator thread, supply `--audit-root <actual-thread-id>` to persist its confirmed root and exclude its descendants as measurement overhead. Never classify unrelated work as overhead from title text.
3. Read `review-queue.json`. `new_dispatch_ids` and `source_changed_dispatch_ids` compare the last two factual snapshots, ignoring observation timestamps. Revisit `changed_since_inspection_dispatch_ids` and `application_pending_contribution_ids`. `unreviewed_dispatch_ids` is the persistent backlog, not a claim that every ID is new today. Resume bounded backlog work from the routine's cursor. Preserve reviewed stable IDs; do not reanalyze all unchanged history.
4. Dispatch exactly one `gpt-6.1-sol` auditor through the supported T3 tool, inheriting reasoning settings. If unavailable, retain the queue and report the provider error. The auditor reads exact, bounded evidence and writes a separate judgment directory. It does not rerun historical workloads. Import only completed, sanitized judgments:

   ```sh
   python3 "$PIPELINE" --data-dir "$OBSERVATORY_DATA_DIR" import-judgments \
     --from-dir "$REVIEWED_JUDGMENT_DIR" --owner "$SOL_AUDITOR_TASK_ID" \
     --auditor-model gpt-6.1-sol
   ```

5. After successful collection/audit, run `aggregate`, `validate`, and `publish` as above. Stop on any nonzero command; the previous published generation remains usable. If only factual collection completed, an aggregate may be published with the previous disposition timestamp and explicit unchanged judgment coverage. Record that the semantic audit is still pending. Update the routine's state with bundle/generation IDs and its processed-ID cursor. Report new material results or failures in Portuguese, with denominators and coverage limits.

## Persistent data and judgment contract

`owner.json` records the delivery owner, original cohort start, source path, seed identity, and confirmed audit roots. Every writer command holds one nonblocking exclusive `flock` on `.writer.lock` for its entire operation; a crashed process releases the kernel lock. Separate routine-level ownership covers the interval while the Sol auditor is running.

- `runs/<content-id>/` contains immutable seed/collection files and their SHA256 manifest. Each collection pins one SQLite read transaction (`mode=ro`, `query_only=ON`); all timestamps/counts/statuses come from that snapshot. Statuses are the latest projection at `source_as_of`, including changes after the cohort end. File statistics are metadata, not a database backup. The initial historical seed predates inode metadata; subsequent reads record/check device/inode identity.
- `judgments/<content-id>/` contains immutable Sol bundles and their owner/model manifest. Supported files are `contributions.jsonl`, `panels.jsonl`, `attribution-coverage.jsonl`, `dispatch-annotations.jsonl`, `attribution-evidence.jsonl`, `api-reconciliation.json`, and required `attribution-metadata.json` (`as_of` UTC and an explicit `selection` scope). Each file may contain only new/revised records; earlier records remain in the persistent store.
- Contributions use stable `contribution_id` and immutable `cause_id`. Contributions, panels, coverage and annotations use monotonically increasing positive integer `revision`; identical duplicates are ignored and conflicting equal revisions fail. Missing revision is accepted as `1` for the historical seed. A revision replaces the whole previous record, so include all retained evidence/discoverers. No deletion or implicit tombstone exists. Explicitly correct a disposition by revision, preserving the cause and evidence trail.
- Coverage has one revisioned row per inspected `dispatch_id`, with boolean `evaluable`, exact `evidence_ids`, and `disposition_as_of`. A fully inspected report with no contribution still gets coverage. Unlisted dispatches have unknown utilization. Accepted and applied are independent nullable booleans: explicit parent decisions support acceptance, inspected artifacts support application, and missing proof stays null. The auditor owns these decisions; importer flags record provenance rather than authenticate model execution.
- Annotations can set reviewed role/skill/panel/round/scope and evidenced retry relationships; they cannot change historical source identities. Evidence IDs must resolve. Evidence indexes contain allowlisted identity/hash/pointer metadata, with private `local_path` allowed only for hash verification. Do not put raw prompts, results, transcripts, credentials, patient data, or arbitrary auth/config content in any bundle.
- `review-queue.json` is a factual resumption aid. `discrepancies/` plus `discrepancy-runs/` preserve rejected source snapshots. Missing historical dispatches, equal-revision source conflicts, changed known historical identities, or a replaced database stop collection. Reconcile the source explicitly; do not erase prior judgments or shorten the window to hide the discrepancy.
- Collection bounds remain cumulative. A different start or an earlier end is refused. This prevents rolling windows from silently dropping old attribution records. Purged/unpersisted/remote/native-unreported history stays outside the provable local census. Collection creates no semantic attribution.

## Immutable publication

`aggregate` reads preserved files only and creates `export/<24-lowercase-hex-id>/dashboard.json` plus a generation-local `publication.json`. `latest-generation.json` selects the locally validated generation. It does not publish to the proxy. Identical inputs reproduce identical bytes and IDs; existing generation files are never overwritten with different bytes.

`publish --publish-dir <config-dir>/delegation-observatory` validates schema, content-derived generation ID, summary recomputation, record/evidence references, exact path, and file SHA256. It copies/verifies the immutable dashboard before atomically replacing the served `publication.json` last. `--generation <id>` selects an explicit preserved generation. Symlinks, traversal, malformed IDs, invalid schema/digests and private export fields are refused. No service is changed or restarted.

The envelope keeps `schema_version: "t3-observatory/1.0"`, original dispatch/contribution/panel metrics, exact `window`, source/disposition timestamps, sanitized `source_snapshot`, historical `source_snapshots` and `disposition_snapshots`, and sanitized evidence. `attribution_coverage` exports **every inspected dispatch**, including no-finding reports, with `dispatch_id`, `revision`, `evaluable`, `evidence_ids`, disposition time and optional role/panel. Its `application_not_applicable` and `application_unknown` booleans reproduce the original dispatch-rate exclusions. Verification-artifact-only contributions are application-not-applicable; accepted corrective contributions without inspected application remain unknown. Remaining audited no-accepted dispatches stay in the defined rate denominators. Filtered consumers must join coverage by dispatch ID and retain these exclusions. Contribution counts are separate from dispatch rates. Unknown usage remains null; no cost or causal family ranking is inferred.

## Verification

Fixtures use small SQLite metadata projections, never simulated agent jobs. Keep test scratch on the mounted SSD:

```sh
OBSERVATORY_TEST_TMPDIR="$OBSERVATORY_VERIFICATION_DIR/fixtures" \
  python3 -m unittest discover -s scripts/delegation-observatory -p 'test_*.py' -v
```

The suite covers source boundaries/identity, unknowns, no-finding coverage, collection without attribution, revisions/conflicts, idempotence, cross-process locking, immutable schema/digest validation, traversal/symlinks and preservation of the previous publication on failure. Real seed verification additionally compares the original cohort's 3,015 work dispatches, 19 evaluable dispatches, six complete panels, 22 accepted contributions and seven artifact-proven applications. It must publish only into a dedicated verification directory until the owner performs live delivery.
