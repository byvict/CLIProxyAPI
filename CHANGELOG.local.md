# Local releases

## 8.0.44-local.24

- Start quota history write transactions in SQLite immediate mode so concurrent account collection does not fail while upgrading a read transaction to a writer.

## 8.0.44-local.23

- Keep quota history attached to the provider and relative credential path when the installation moves. Preserve current management identifiers in API responses.
- Add a checked migration for observations split across former runtime directories, with a database backup and conflict rejection.
- Collect quota in a scheduled macOS job when the panel is closed or hidden, using the existing provider readers and shared polling reservations. A visible panel keeps priority.
- Preserve the weekly projection criteria and mark incomplete coverage as provisional.

## 8.0.44-local.22

- Mark GrokBot consumption with subtle diagonal hatching inside the shared quota timeline bar. Keep GrokBuild consumption plain and omit the split when product usage is unavailable or the quota window has reset.

## 8.0.44-local.21

- Restore exhaustion markers for every estimated quota window, including dates after the scheduled reset. Keep estimates provisional when the recent pace changes instead of hiding their markers.
- Anchor Claude's live and recorded timeline to the account-wide weekly quota instead of a model-specific window with an earlier reset.
- Preserve Antigravity consumption evidence across balance increases within the same reset cycle, independently for Gemini and third-party pools.

## 8.0.44-local.20

- Keep the definitive checkout in `~/Dev/CLIProxyAPI/codigo` and the delegation observatory's publication, data, control and work directories in `~/Dev/CLIProxyAPI/app`. The runtime's `delegation-observatory` is a directory instead of a link to the SSD, so the delegation page no longer needs the SSD mounted.
- Stop copying `Iniciar.command` and `Parar.command` to `.before-launchd` files on deploy.

## 8.0.44-local.19

- Export a flat `tier0` record per delegation dispatch with status, interruption, failure, mechanical replacement and retry links, latency, own tokens, provider, model, family, effort and dispatcher and root families, plus a per-seat summary. Existing export fields are unchanged, and generations exported before this release still validate and publish.
- Add `scripts/delegation-observatory/inventory.py`, which writes a heuristic multi-family round inventory, a comparison of the pstack model sheet with historical seats and an estimate of quota points per window from a validated export.

## 8.0.44-local.18

- Serve the Performance page from a report kept in memory. The proxy checks its sources every minute and recomputes only when T3's database, the imported history or the São Paulo date changed, so page opens no longer wait 2 to 5 seconds and aborted requests no longer discard the work. The first open after a restart waits for one computation, and a failed computation is retried a minute later.

## 8.0.44-local.17

- Run the performance history importer from the active release at load and every 15 minutes as the LaunchAgent `com.byvict.cliproxyapi.performance-import`, with its receipt and error log in `~/Library/Logs/CLIProxyAPI/`. `stop`, `uninstall` and `status` cover both jobs.
- Keep the history in SQLite WAL mode so page reads continue during an import, and replace the per-run backup with one dated backup per São Paulo day, keeping the newest three.
- Read imported executions and token events in one read transaction.
- Ship the importer as a checksummed release artifact, keep older releases valid for rollback, and run its tests before each deploy.
- Skip a T3 database that cannot be read or parsed, list it in the receipt's `skippedSources`, and still import the other sources. An error writing the history still fails the run without changes.
- Wait up to 5 seconds for a T3 or history database that another process holds locked instead of failing the page read.

## 8.0.44-local.16

- Run the service from `~/Dev/CLIProxyAPI/app` and build releases in `~/Dev/CLIProxyAPI/build`.
- Run the deploy's `go test ./...` directly again. The launchd test job is removed.

## 8.0.44-local.15

- Build releases from `~/Library/Caches/CLIProxyAPI-build` on the internal disk and remove each panel build directory after its artifact is copied.
- Run the deploy's `go test ./...` as a one-off launchd job, outside T3 Code's process tree, so its loopback port probes cannot fail test servers.

## 8.0.44-local.14

- Run the service from `~/Library/Application Support/CLIProxyAPI` on the internal disk. The delegation observatory keeps its data on the SSD and the runtime links to its publication.

## 8.0.44-local.13

- Draw the Antigravity Claude & GPT quota pool as an `Other` timeline lane directly below the Antigravity (Gemini) lane, with its own weekly balance, window, recorded readings and forecast.
- Ignore root-path requests from local loopback port scanners in the OAuth session proxy tests, which failed the deploy gate as false direct-fallback hits.

## 8.0.44-local.12

- Standardize the Antigravity quota card summary to show its plan as plain text without model-group quota bars.
- Render the xAI subscription plan (`SuperGrok Heavy`) as plain text instead of a badge on quota summary cards.

## 8.0.44-local.11

- Show quota timeline bars and eligible forecasts from recorded observations before provider replies, preserving timestamps and unknown states.
- Reuse current-session history while navigating and preserve standard Codex window identity when its reading is unknown.
- Avoid repeated bcrypt checks for a previously verified management key while preserving bans, remote-access policy, and configuration rotation.

## 8.0.44-local.10

- Load activity without ordering token events, reuse recent results while navigating, and request GitHub activity on demand.
- Display quota balances per account before optional metadata, share concurrent reads, and preserve balances during refresh.
- Preserve history selections, cancel obsolete reads, and recover collection after failed resets.
- Bound history reads by period and align sample validity with the 30-minute collector using 45-minute and 65-minute limits.

## 8.0.44-local.9

- Separate inspected dispatches, evaluable utilization and unread work in the delegation dashboard without changing rate denominators.

## 8.0.44-local.8

- Accept GPT-6.1 Terra audit imports and preserve the actual auditor model in stored and exported provenance.

## 8.0.44-local.7

- Show only aggregate family contribution dashboards, without individual panels, findings or evidence accordions.
- Distinguish identified dispatches from analyzed outcomes and preserve unknown utilization.

## 8.0.44-local.6

- Remove percentages and reset timestamps inside weekly and 5-hour quota timeline bars, preserving fills and hover details.

## 8.0.44-local.5

- Show the quota windows table above provider summary cards on desktop and mobile.

## 8.0.44-local.4

- Add the Agentes dashboard for real T3 dispatches, inspected contributions, root/delegate family comparisons and complete-panel exclusivity.
- Publish validated, sanitized audit generations through the existing authenticated management API, with explicit unknown outcomes and evidence references.
- Add a read-only collector, revisioned judgment store and atomic publisher for the daily T3 audit.

## 8.0.44-local.3

- Refresh every provider's quota every 30 minutes while the panel is visible.
- Use the same 30-minute reservation interval on the server across tabs and panel restarts.
- Keep automatic loading on open and Muse's minimum interval.

## 8.0.44-local.2

- Load native quota cards when reopening the panel, even after a recent poll.
- Refresh every five minutes while visible, resume on return, and wait for each batch to finish.
- Show quota read errors and retry automatically. Preserve Muse's 30-minute minimum interval.

## 8.0.44-local.1

- Start the main proxy on port 8317 at macOS login and restart it after a process exits.
- Deploy the tested binary and the verified management panel from clean, pushed `main` with build identity checks and rollback.
- Preserve the existing configuration, credentials, and history databases during updates.
- Keep the runtime on the SSD and authorize removable-volume access through a stable macOS launcher.
- Leave the separate V1 monitor on port 8318 outside service and deployment operations.
