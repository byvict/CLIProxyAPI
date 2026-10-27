# Run the subscription monitor

Build the separate executable with Go 1.26.10 or later:

```sh
go build -o subscription-monitor ./cmd/subscription-monitor
```

Start it with a dedicated, absolute data directory:

```sh
./subscription-monitor --data-dir /Volumes/Arquivos/cliproxyapi-monitor-v1 --listen 127.0.0.1:8318
```

Open `http://127.0.0.1:8318`. Enter the key from `access.key` in that data directory. The key file is private. The browser keeps an opaque HttpOnly, SameSite=Strict session for up to 12 hours. Restarting the process discards sessions. Tools can send the key in an `Authorization: Bearer` header. Keep it out of command arguments and shell history.

The process collects with the panel closed. The browser keeps fetching while you inspect a chart or keep settings open. It preserves those controls and shows a paused-display notice until you leave the interaction or apply the pending data. Stop it with SIGINT or SIGTERM. One process owns each data directory. SQLite uses WAL, schema migrations, idempotent attempts, account bindings, and independent window histories. A source that stops responding stays pending until lifecycle cancellation. Other sources and the panel remain available.

## Native credential sources

The native CLI remains the only token renewal writer. The monitor reads one credential snapshot per probe and never refreshes it.

| Provider | Source and identity |
| --- | --- |
| Codex | `CODEX_HOME/auth.json`, or `~/.codex/auth.json`. Identity combines issuer, native account ID, and access-token subject. |
| Claude | `CLAUDE_CODE_OAUTH_TOKEN` takes precedence. Otherwise use the active macOS Keychain credential, with the native username filter and service suffix derived from `CLAUDE_CONFIG_DIR` or `CLAUDE_SECURESTORAGE_CONFIG_DIR`. The native `.credentials.json` file is the fallback when the Keychain item is absent, and the source on other platforms. The same access token supplies `oauth/profile` and `oauth/usage`. |
| Grok | The supported issuer entry in `~/.grok/auth.json`. Identity combines issuer, native `user_id`, and token subject. API-key mode is unavailable for subscription monitoring. |
| Muse | Explicit `MUSE_AUTH_PATH`, otherwise the native macOS Keychain item, with the XDG `muse/auth.json` file as fallback. The authenticated mint response supplies both quota and normalized `user_email`, labeled `provider_email`. |

The Claude inherited environment token may have usage access without profile access. That source remains `identity_unverified`. Do not replace its identity with a cached profile or silently fall back to a different login. To run against a Keychain login instead, start this process without that environment token. This changes neither the credential store nor the environment of another process.

An unverified identity receives no account history. Token rotation with the same principal preserves history. Switching accounts closes the old source binding, even when quota is unavailable after the new identity is verified. Old accounts remain inspectable as signed out. Email values appear only after an authenticated POST for the selected account.

Muse mint can issue an API key. The monitor discards it and records `credential_exchange_requested`. Its cadence is at least 30 minutes, including manual refresh. Other sources start at five minutes. Manual refresh can advance their normal schedule, while provider backoff and the Muse floor remain binding. Local credential absence uses ordinary cadence. Reservations and provider backoff survive restarts. Valid Retry-After values are a lower bound measured from response receipt. Values over a year away are rejected as implausible. There are no quota request deadlines, client total timeouts, response-header timeouts, or TLS handshake deadlines. Lifecycle cancellation stops outstanding work without recording a provider failure. Local Keychain acquisition has a ten-second bound.

## Read the data

Authenticated routes are:

- `GET /api/v1/snapshot` returns current and retained accounts, sources, independent windows, ages, freshness, limiting windows, and available pace estimates.
- `GET /api/v1/snapshot?format=t3-usage` returns only the current native account per provider in the existing allocation input format. Stale or failed sources do not become `ok`. The shared `checkedAt` is the oldest contributing observation. When no source contributes, it is the oldest available attempt; before any attempt it is the Unix epoch. `checkedAtBasis` distinguishes these cases, and `generatedAt` is the generation time.
- `GET /api/v1/view?provider=claude&window=weekly&period=24h&lookback=1h&horizon=7d&recurrence=true` returns history and analysis for an explicit selection. Use a window ID from the snapshot to select a model scope or capacity basis. Add `account=<id>` to inspect one retained account.
- `POST /api/v1/refresh` accepts `{}`, `{"source":"claude-native"}`, or `{"account":"<id>"}`. It returns immediately with queued, pending, and next-allowed states.
- `POST /api/v1/accounts/<id>/email` reveals only that account's email.
- `POST /session` accepts the monitor key and establishes the browser session.

Browser mutations require a matching Origin. All routes validate loopback Host and remote address. Data responses use `no-store`. There is no permissive CORS. The proxy's deprecated management routes are not used.

Rates use time-weighted, continuous observation intervals. A reset, adjustment, failed read, inactive binding, or collection gap breaks deltas. Reset timestamp drift up to one minute remains in the same cycle. A current null percentage is an observed unknown balance; it does not discard other current windows. Hourly and daily bars include only intervals contained entirely in a bin. Cross-boundary deltas remain explicitly unattributed, with amounts listed once and per-bin boundary flags. Peaks require a fully observed one-hour interval. Completed cycles expose a bound on unused quota from the last pre-reset sample rather than an exact unobserved balance.

Forecasts need at least three continuous observations. They remain between zero and capacity. A reported reset restores capacity. Later recurrences are labeled inferred and can be disabled. A passed reset without a new observation suspends the forecast. Unreported plan capacity stays separate per account. Provider percentages, session windows, weekly windows, and model scopes are never combined into common capacity.

API history reads are bounded to the selected period, plus the predecessor reading for each window, the predecessor attempt for each source, and the latest sample metadata for retained windows. Indexed range searches and point lookups read this data without scanning archived series. Startup, refresh and email lookup do not load series. No history is pruned.

Codex reset-credit counts and per-model availability are shown when the provider reports them. Model availability stays separate from percentage windows and never creates a consumption rate or an inferred entitlement cause. Optional return times accept Unix seconds and RFC3339; an unknown time never discards a known availability value. The count is available_count, not a claim of applicable or eligible credits. Other reset-credit counts remain unknown. Reset actions are disabled because these native readers expose no eligible redemption operation. The monitor never sends inference, wakes Muse, changes routing, or changes the allocation policy.

## Verify the implementation

Run the deterministic tests, race checks, and both executable builds:

```sh
scripts/verify-subscription-monitor.sh
```

The script uses the isolated SSD toolchain and test paths from this workspace. Set `SUBMON_BUILD_ROOT` and `SUBMON_VERIFY_ROOT` to equivalent isolated roots on another machine. Each run writes its evidence outside Git.

Run real quota checks against the current native sources without inference or renewal:

```sh
scripts/verify-subscription-monitor.sh --live
```

This reuses `SUBMON_VERIFY_ROOT/live` to preserve cadence and backoff. The receipt identifies cached outcomes. It contains no tokens or identity values. Environment overrides are honored, including a Claude token inherited by the current shell.

Start a test-only server with deterministic SQLite history and the real embedded UI and authenticated handler:

```sh
scripts/verify-subscription-monitor.sh --serve-fixtures
```

The command prints its URL, key-file path, and PID-file path. It creates a fresh `fixture` subdirectory for each run. It never reads native credentials. Use SIGTERM with that PID to stop it. There is no production demo mode or production seeding endpoint.

With Playwright and Chromium installed, verify all four screens at desktop and narrow widths:

```sh
node scripts/subscription-monitor-ui-check.mjs http://127.0.0.1:18318 /absolute/verification-run/fixture/access.key /absolute/verification-run/ui-evidence
```

Set `SUBMON_PLAYWRIGHT_MODULE` to an absolute Playwright module path when it is outside normal Node resolution. The script checks keyboard chart inspection, account email guards, chart collapse, JavaScript errors, and page overflow. It records screenshots and `ui-receipt.json`.

Validate an exported fixture or live `t3-usage` snapshot with the allocation consumer only through `escalar.py --catalog <existing-catalog> --usage <snapshot> --dry-run`. This does not call its normal quota collector or rewrite the model sheet. Eligibility failures are distinct from malformed input.

Check delayed automatic responses and session renewal with the same fixture server:

```sh
node scripts/subscription-monitor-delayed-ui-check.mjs http://127.0.0.1:18318 /absolute/verification-run/fixture/access.key /absolute/verification-run/delayed-ui-evidence
```

This checks focus acquired after a refresh starts, continued fetching during interaction, deferred display, settings-close resumption, explicit application of pending data, and an expired session response that arrives after a new login. Its receipt is `delayed-ui-receipt.json`.
