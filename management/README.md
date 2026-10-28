# Build the management panel fork

This directory extends the complete upstream CLI Proxy API Management Center.
`upstream.json` fixes its source revision. `refinements.patch` changes the existing
navigation, compact quota cards, and quota timeline. `src/` hosts Quota History inside the same React app.
All existing providers, OAuth, configuration, logs, and plugin pages remain in the build.

Quota History reuses the v1 markup, CSS, chart renderer, filters, and keyboard
interaction from `v1-source/`. The build verifies the reference hashes and extracts
only the history functions. A shadow root contains the v1 styles within the original
layout. The server reuses the v1 analysis functions for hourly and daily consumption,
rolling rates, coverage, unassigned intervals, and the heatmap.

Use Bun 1.4.3 and run this command from the repository root:

```sh
scripts/build-management.sh /absolute/build-root /absolute/static/management.html
```

The script checks out the upstream revision in a fresh build directory, applies
the refinements, and runs the complete frontend test suite, lint, and build.
It produces the original single-file `management.html` deployment artifact.
Keep `management.disable-auto-update-panel: true` when installing this artifact,
so upstream automatic updates do not replace the fork.

The committed patch includes the compact summaries for all four native providers,
the shared dashed current-time line, provider logos and weekly balances, and the
Codex reset-credit triangles. Build from these committed inputs. A previously
installed HTML file can contain local changes that were never added to the patch.

Check the summary cards in the built artifact with synthetic provider responses:

```sh
PLAYWRIGHT_MODULE=/absolute/playwright/index.mjs node scripts/compact-quota-ui-check.mjs \
  http://127.0.0.1:18317 /absolute/management-build /absolute/card-evidence
```

This checker intercepts every browser request and loads the build's `dist/index.html`.
It checks all four cards on desktop and mobile without using live credentials.
The compact-card unit tests also run in every `scripts/build-management.sh` build.

Build and run the proxy from this branch to provide the authenticated
`GET` and `POST /v8/management/quota-history` endpoints, plus the read-only
`/quota-history/snapshot` and `/quota-history/view` endpoints for the v1 renderer.
Observations are stored in
`quota-history.sqlite` beside the proxy configuration. No credentials or tokens
are stored in this database. Existing v1 monitor data is left intact.
`scripts/import-quota-history-v1.py --help` describes the optional import of v1
history. It links only verified identities, keeps unmatched accounts as separate
inactive historical accounts, keeps different capacity bases separate,
preserves failure barriers, and backs up the destination before writing. Use
`--dry-run` to inspect matched providers without changing either database.

The connected panel collects quota readings every five minutes through the
existing provider readers. Muse keeps a 30-minute minimum interval because its
quota endpoint can issue an API key. Poll reservations persist across tabs and
panel restarts. Collection stops when the panel closes. History remains
on the server between sessions. Every successful quota reader, including manual
refreshes and resets, records observations through the same adapter wrapper.
The timeline estimates the remaining lifetime from the last 24 hours of the current
quota cycle. Shorter quota windows use their own duration. A changed reset or an
upward balance adjustment starts a new segment. Observed idle time counts; missing
intervals do not count as zero consumption. The base does not shrink as reset approaches.

An estimate requires at least one observed hour and two valid intervals. The panel
labels it provisional until the requested base reaches 24 hours (or the window's
shorter duration), coverage reaches 80%, and the measured decline reaches 2 percentage
points. Coverage measures observed time within the eligible part of this cycle;
it is distinct from the age of the cycle and is not a confidence score.

The last six hours also provide a stability check. If they contain at least 4.8
observed hours and their rate is less than half or more than twice the baseline
rate, the panel reports a changing pace and suppresses the date. These thresholds
are operational disclosure rules, not statistically calibrated confidence bounds.
An absence of measured decline is labeled "no detectable drop in observed intervals".

The summary expands with a click or keyboard to show the base, observed duration,
coverage, decline, hourly rate, and reading age. Provisional markers are dashed.
Dates are approximate, and the estimate assumes the observed pace continues.
If the balance lasts until reset, the panel reports that outcome without drawing
an exhaustion marker after renewal. Marker colors describe how far an exhaustion
before reset falls from renewal, not the reliability of the prediction.

Replay the legacy and proposed methods against recorded outcomes without writing
to the database:

```sh
git show dbec77b3:management/src/features/quotaHistory/projection.ts > /absolute/evidence/legacy-projection.ts
# Replace its missing upstream duration import with: const HOUR_MS = 3_600_000;
bun scripts/backtest-quota-projection.ts /absolute/quota-history.sqlite /absolute/evidence/legacy-projection.ts > /absolute/evidence/backtest.json
```

The replay uses hourly origins, same-cycle outcomes at six and 24 hours, and the
same eligible observations for all three methods: legacy, whole current cycle,
and the proposed capped current cycle. It reports excluded estimates, signed and
absolute balance errors, false exhaustion warnings, and missed exhaustion. It
uses no future readings in its inputs and exports no account identifiers.
Overlapping outcomes are correlated. Sparse histories and no observed exhaustion
cannot establish a calibrated probability or prove that warnings improved.

To verify the built UI with synthetic credentials, create an isolated fixture:

```sh
python3 scripts/quota-history-fixture.py create /absolute/fixture
cp /absolute/static/management.html /absolute/fixture/static/management.html
go build -o /absolute/fixture/cli-proxy-api ./cmd/server
/absolute/fixture/cli-proxy-api --config /absolute/fixture/config.yaml --local-model
```

In another terminal, seed and check the fixture:

```sh
python3 scripts/quota-history-fixture.py seed /absolute/fixture --history-hours 12
PLAYWRIGHT_MODULE=/absolute/playwright/index.mjs node scripts/quota-history-ui-check.mjs \
  http://127.0.0.1:18317 /absolute/fixture /absolute/ui-evidence
```

The checker intercepts provider quota requests with synthetic responses. It checks
all four colors with partial history, compact provider labels, the shared dashed
time line, reset-credit triangles, existing navigation, the v1 controls, and the
mobile layout. It also compares rendered panels and SVG charts against the original
v1 at `http://127.0.0.1:8318` using identical data and width. Set `V1_REFERENCE_URL`
to use another v1 server. Screenshots and `receipt.json` are written to the evidence directory.

To verify a registered Claude account against its actual quota history, run:

```sh
PLAYWRIGHT_MODULE=/absolute/playwright/index.mjs node scripts/quota-history-live-check.mjs \
  http://127.0.0.1:8317 /absolute/management-key.txt /absolute/live-evidence
```

This check refreshes the selected Claude quota through the original reader, verifies
that the observation is saved, and inspects the visible projection and v1 charts.
It allows only GET operations to the provider. It requires enough observed consumption
for a finite estimate. The evidence directory contains screenshots of your account.

## Performance and GitHub

**Performance** follows **Quota Management** in the sidebar. Its **Todos**, **Claude**,
**Codex**, **Grok**, and **Meta** tabs share the annual activity calendar and the
30-day token and agent charts. **Todos** adds the four providers' counts and tokens.
Streaks use the union of active days, and longest execution uses the maximum duration.

The authenticated `GET /v8/management/performance` endpoint reads
`~/.t3/userdata/statev2.sqlite` on the server in SQLite read-only mode. It reads v2
provider-thread and provider-turn projections, without prompts or credentials.
One agent means one recorded turn. Durations use completed turns. Tokens use
`turnTokenUsage.inputTokens + turnTokenUsage.outputTokens`; cached input and
reasoning are already included. Missing token reports remain unavailable, and
partial reports have a visible coverage notice. Dates use `America/Sao_Paulo`.
The page shows the first available record and does not infer activity from quotas.
If T3code runs under another user or on another machine, the page reports that its
local history is unavailable.

**Infra** follows **AI Providers** and reuses its header, category list, and resource
panel layout. It currently supports GitHub through the existing `server.github-token`
configuration and environment fallback. `GET /v8/management/infra/github` validates
the connection and returns the authenticated account's annual contribution calendar,
PRs, reviews, and commits. `POST /v8/management/infra/github` accepts `{"token":"..."}`,
validates the token with GitHub GraphQL, and saves it only after validation succeeds.
These endpoints do not return the token. The form clears the token after a successful
save and does not store it in browser storage. Private contribution visibility follows
the token's permissions. GitHub activity belongs to the entire account and is not
assigned to AI providers.

### Recover local history

Run `python3 scripts/import-performance-history.py --destination /absolute/config-directory/performance-history.sqlite --receipt /absolute/evidence/import.json`.
Use `--dry-run` to preview the import in memory. The importer reads T3 v1 and v2
projections, userdata backups, usage caches v4 through v9, native CLI logs, and
archived Codex transcripts, including `.jsonl.zst` files. It requires Python 3.9+
and `zstd` for compressed transcripts. Source stores remain read-only. The archive
contains timestamps, identifiers, counts, and source paths. It contains no prompts
or credentials. Existing archives receive a SQLite backup before each import.
Unchanged transcripts are skipped on subsequent runs.

Performance combines this archive with current T3 projections. Execution IDs remove
snapshot duplicates. Native turns that overlap a T3 turn in the same provider and
native session count once. Request IDs remove repeated Claude content blocks, Grok
model breakdowns, and Muse event copies. Codex events use session, timestamp, and
usage counters. Input cache categories in the T3 scan cache are disjoint. Reasoning
is already part of output. Native token events replace the overlapping part of a
T3 token report. A larger T3 report contributes only its remaining recorded tokens.
Native timestamps preserve token activity across midnight in São Paulo.

Cached calls without preserved execution boundaries contribute tokens and calendar
activity, but do not create agents or durations. Native-only token coverage is
marked partial. Legacy T3 turns without a recoverable native identity are excluded
from the execution count to avoid counting the same CLI execution again. The import
receipt reports these exclusions. Run the importer again to refresh direct CLI
history; current T3 activity continues to update without another import.

Run `python3 scripts/import-codexbar-history.py --help` to recover CodexBar quota
history. This import uses recorded capture times and percentages. It links a Codex
account to a proxy credential only when the provider account ID matches exactly.
Unknown accounts remain separate inactive historical accounts. It backs up the
quota database and ignores duplicate observations. It does not infer balances from
tokens. In Quota History, select **Todo o histórico** to inspect older readings.
Select a historical account explicitly to inspect an account outside the current
login set. Gaps retain the existing coverage rules.

Verify the importers with `python3 -m unittest discover -s scripts -p 'test_import_performance_history.py'`.
Verify the readers and endpoints with `go test ./internal/performance ./internal/quotahistory ./internal/api/handlers/management`.

The existing management key protects all three endpoints. Home mode disables these
local management routes under the same policy as the rest of the v8 API.

To check the built pages against an isolated proxy with an empty auth directory, run:

```sh
PLAYWRIGHT_MODULE=/absolute/playwright/index.mjs node scripts/performance-ui-check.mjs \
  http://127.0.0.1:18327 /absolute/fixture /absolute/ui-evidence
```

The fixture directory must contain `management.key` for that proxy. The checker uses
synthetic activity and GitHub responses. It verifies navigation order, all provider
tabs, token coverage, connection validation, keyboard chart inspection, and mobile
layout. Screenshots and `receipt.json` are saved in the evidence directory.
