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
