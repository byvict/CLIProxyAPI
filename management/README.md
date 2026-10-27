# Build the management panel fork

This directory extends the complete upstream CLI Proxy API Management Center.
`upstream.json` fixes its source revision. `refinements.patch` changes the existing
navigation and quota timeline. `src/` hosts Quota History inside the same React app.
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

Build and run the proxy from this branch to provide the authenticated
`GET` and `POST /v8/management/quota-history` endpoints, plus the read-only
`/quota-history/snapshot` and `/quota-history/view` endpoints for the v1 renderer.
Observations are stored in
`quota-history.sqlite` beside the proxy configuration. No credentials or tokens
are stored in this database. Existing v1 monitor data is left intact.
`scripts/import-quota-history-v1.py --help` describes the optional import of v1
history. It matches verified identities, keeps different capacity bases separate,
preserves failure barriers, and backs up the destination before writing. Use
`--dry-run` to inspect matched providers without changing either database.

The connected panel collects quota readings every five minutes through the
existing provider readers. Muse keeps a 30-minute minimum interval because its
quota endpoint can issue an API key. Poll reservations persist across tabs and
panel restarts. Collection stops when the panel closes. History remains
on the server between sessions. Every successful quota reader, including manual
refreshes and resets, records observations through the same adapter wrapper.
Forecasts use observations inside the preceding X hours, where X is the time remaining
until reset. A partial history produces a visible estimate with its observed coverage.
Gaps, resets, and adjustments are excluded from consumption. Stale data or fewer than
two valid consumption intervals suspend the estimate.

The exhaustion marker extrapolates the current balance even when exhaustion falls
after reset. Its color uses the absolute distance from reset: green through 6 hours,
blue through 12 hours, yellow through 24 hours, and red beyond 24 hours.

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
all four colors with partial history, existing navigation, the v1 controls, and the
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
