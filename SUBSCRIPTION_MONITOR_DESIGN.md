# Subscription monitor V1 design

The approved panel reads native CLI subscription quotas and retained observations.
It does not count inference requests or redirect T3 traffic. The requirements are
in [V1_SUBSCRIPTION_MONITORING.md](V1_SUBSCRIPTION_MONITORING.md). The approved
presentation is [subscription-monitor-v1.html](prototypes/subscription-monitor-v1.html).

## Usage

Build `cmd/subscription-monitor` from this repository and run it separately from
`cmd/server`:

```sh
subscription-monitor --data-dir /Volumes/Arquivos/cliproxyapi-monitor-v1 --listen 127.0.0.1:8318
```

The monitor owns its database, local access key, session authentication, and
background workers. Its panel is embedded in the binary. The original proxy at
`127.0.0.1:8317` remains unchanged and supplies the comparison baseline.

The command has one service call:

```go
errRun := submon.Run(ctx, submon.Options{
	DataDir: dataDir,
	ListenAddr: listenAddr,
})
```

The public interface hides database setup, source discovery, HTTP authentication,
collection scheduling, and shutdown. Constructors and wire types stay internal.

The authenticated panel reads two views:

```text
GET /api/v1/snapshot
GET /api/v1/view?provider=claude&window=weekly&period=24h&lookback=1h&horizon=7d
```

The allocation skill can consume `GET /api/v1/snapshot?format=t3-usage`. Allocation
policy and model choices remain in that skill.

## Synthesis decision

Three independent candidates and a fresh cross-judge all selected a separate
executable in this repository. Candidate A supplies the domain and conservative
analysis rules. Candidate C supplies one worker per source, a coalescing refresh
channel, persistent backoff, and a flat internal package. The cross-judge resolved
Muse identity, email revelation, and uncertain hourly attribution.

An in-process monitor would require another proxy instance to preserve the
original. That instance would also start executors, credential refresh, watchers,
and catalog updates. A c-shared plugin would depend on proxy credentials and
deprecated management routes. The separate executable avoids those dependencies
and leaves the upstream proxy implementation untouched.

Use `database/sql` with a pinned `modernc.org/sqlite` release compatible with the
existing `go 1.26.10` directive. Do not raise that directive. Pure Go supports the
repository's cgo and non-cgo build paths. The accepted cost is the new dependency.

## Domain

The organizing structure is a source registry, immutable probe results, and
independent account-window histories.

- A source describes a native credential location and a provider. It contains no
  secret and has one collection worker.
- An identity binds a credential to a provider principal. Its basis is
  `provider_account` or `provider_email`. A source slot or token hash is never
  used as an account identity.
- An account has a stable ID derived from that identity. Source bindings record
  when it was the signed-in account. Previous accounts retain their history.
- A window key includes kind, model scope, period, unit, and any necessary
  capacity basis. Session, weekly, and model-specific windows remain separate.
- A probe records an attempt and its normalized outcome. A reading and missing
  data are different variants. Numeric zero means exhausted, never absent.
- Freshness derives from the last successful observation and the source's
  cadence. A failed attempt retains the previous reading with its age and error.

No provider token, raw authentication response, or credential fingerprint is
persisted or sent to the panel. Validated identity metadata may be retained.

## Native credential ownership

Read the current credential once per probe and use that snapshot throughout it.
The native CLI remains the only process that renews or writes its login.

| Provider | Identity bound to the quota probe |
| --- | --- |
| Codex | Native `auth.json` supplies the access token, account ID, and token subject. The account ID used in the quota request belongs to that snapshot. Respect `CODEX_HOME`. |
| Claude | Read the active Keychain credential. Use the same token for `oauth/profile` and `oauth/usage`. The authenticated profile UUID is the identity. Do not infer identity from `~/.claude.json`. |
| Grok | Read the native issuer entry once. Use its user ID and token subject. API-key mode does not represent a subscription login. |
| Muse | One authenticated mint response supplies quota and `user_email`. Normalize that email as a labeled `provider_email` identity. An email change starts a different history. |

If no supported identity is available, record `identity_unverified` without
attaching its quota to an account. Never merge observations by source path.
Multiple sources for the same identified account must not duplicate capacity.

Muse's mint endpoint can issue an API key even when the caller only needs quota.
Discard that key, record the credential-exchange side effect, and use a minimum
30-minute cadence that manual refresh cannot bypass. Other providers start at
five-minute collection intervals. Honor provider backoff and `Retry-After`.
Never call an inference endpoint or wake a dormant Muse window.

The mint is also the quota request, so it has no per-request deadline. Quota
clients have no total timeout or response-header timeout. Lifecycle cancellation
remains available. Local credential acquisition can be bounded. Any transport
connect policy must respect the repository's network rules.

## Collection and persistence

One goroutine owns each source and a refresh channel of capacity one. Scheduled
and manual collection requests coalesce. A pending source cannot start another
probe. A stalled request affects that source alone, while the API serves its last
successful reading and increasing age.

Network access occurs before a short persistence transaction. No database
transaction or global lock spans a provider call. The store owns serialized
writes and prevents a second monitor process from owning the same runtime root.

SQLite retains accounts, source bindings, attempts, window readings, schema
version, and next allowed probe times. Use transactions, idempotent observation
keys, WAL, and an index on account, window, and observation time. Resume backoff
after a restart. Migrations never change the original proxy's data.

## Analysis

All calculations use persisted observations and an explicit clock. The browser
renders server-produced series and does not create sample history or forecasts.

An interval is usable only inside the same account, compatible window, and
continuous cycle. A confirmed reset, an unexplained balance increase, a quota
adjustment, a failed observation, or missing coverage breaks that interval.
An increase alone does not prove a reset.

Hourly and daily bins include deltas whose observation intervals fall wholly
inside the bin. Cross-boundary deltas are reported as `unattributedPp`, with their
interval, and make coverage incomplete. Do not distribute uncertain consumption
across bins as exact data. Rate estimates are time-weighted over valid observed
intervals and labeled as observed between samples. Peaks use a one-hour average
and retain their interval and coverage.

Forecasts require enough continuous observations to estimate a rate. Without
them, show a useful insufficient-history state. Clamp projections between zero
and capacity. At a reported reset restore the balance to capacity, rather than
adding capacity to an existing balance. Label any future recurrence as inferred.
Keep exhaustion predictions, sustainable pace, and alerts tied to the same
account-window selection and freshness.

Aggregate only compatible windows within one provider. Label contributors,
missing accounts, unit, and capacity basis. A normalized mean is not an assertion
that different plans contain equal capacity. The `All` selection shows provider
summaries separately; it must not silently choose Claude for a shared chart.

## HTTP and panel

Bind only to loopback and validate Host. Serve the embedded shell without quota
data. Protect data and actions with a monitor key or an opaque HttpOnly,
SameSite=Strict session. Validate Origin for browser mutations and do not enable
permissive CORS. Provider secrets are forbidden in the browser; the monitor's own
login and session authenticators are allowed.

Create the monitor key in a private file under the runtime root. Do not put it in
command arguments, logs, the HTML, or permanent browser storage. Sessions may be
discarded on restart. Data responses and email revelation use `no-store`.

| Route | Behavior |
| --- | --- |
| `POST /session` | Verify the monitor key and establish a browser session. |
| `GET /api/v1/snapshot` | Current accounts, window readings, freshness, limiting windows, pace when available, and unknown reset-credit capability. No email fields. |
| `GET /api/v1/snapshot?format=t3-usage` | Current native account per provider in the existing collector contract. Use the oldest contributing observation for shared freshness and preserve provider-level timestamps and error states. |
| `GET /api/v1/view` | One explicit selection supplies summary, history, rate, bins, heatmap, forecast, assumptions, insufficient-history reason, and coverage. |
| `POST /api/v1/refresh` | Coalesce collection or return pending/backoff information without waiting on a provider. |
| `POST /api/v1/accounts/{id}/email` | Reveal only this authenticated account's email on explicit request. |

Adapt all four approved screens. Remove fictional accounts, fixed dates,
synthetic series, simulated reset actions, and the demo label from the served UI.
Keep the approved visual structure, accessible chart inspection, and responsive
layouts. Real history initially has few observations; do not fill it with demo
data. Use America/Sao_Paulo consistently.

Native readers do not expose manual reset redemption or credit counts. Display
unknown capability and disabled reset controls with a reason. Never substitute
the proxy's local cooldown reset for a provider quota reset.

## Implementation and verification order

1. Add deterministic domain, parsing, and analysis fixtures before behavior.
2. Verify stable identity, token rotation, account changes, null percentages,
   errors, rate limiting, Muse dormancy, and matching Claude profile credentials.
3. Verify SQLite reopen, idempotence, source bindings, backoff, and absence of
   fixture secrets in stored data and logs.
4. Verify one blocked provider does not block others, refresh requests coalesce,
   and quota clients do not introduce post-connection timeouts.
5. Connect authenticated HTTP and replace the prototype's data path. Verify
   Host, Origin, email revelation, and the allocation snapshot contract.
6. Run the Go readers against the real local logins without renewing them or
   generating inference. Only add transport specialization if live evidence
   establishes a need.
7. Verify background observation with the panel closed, persistence across a
   restart, and all four screens at desktop and narrow widths.
8. Run gofmt, the relevant tests including race coverage, the complete existing
   suite, and required builds for the original server and monitor commands.

The original build and full suite passed with an isolated Go 1.27.3 toolchain
before implementation. Source Go compatibility remains 1.26.10. Keep V1 runtime,
database, logs, evidence, and sockets on the SSD outside Git. No T3 settings,
native credential stores, or original installation files are modified by V1.
