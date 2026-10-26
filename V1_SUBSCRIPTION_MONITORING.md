# V1 subscription monitoring

Status: requested scope and acceptance criteria, not implemented.

Victor requested subscription limits, consumption by hour and day, historical
periods, pace, peaks, and forecasts. Project attribution and T3 integration belong
to V2. The V1 monitor also supplies data to the existing pstack allocation skill.
The original installation remains the comparison baseline.

## Reference investigation

Checked on October 26, 2026, in America/Sao_Paulo.

No matching public fork was found among the checked repositories owned by
[t3dotgg](https://github.com/t3dotgg),
[pingdotgg](https://github.com/pingdotgg), or
[t3-oss](https://github.com/t3-oss). Direct GitHub API lookups for `CLIProxyAPI`
and `Cli-Proxy-API-Management-Center` under those owners returned 404. This does
not establish that the fork is private. A renamed repository or another owner
remains possible.

The public upstream panel is a separate project from the Go proxy:
[Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center).
The installed baseline uses panel release `v1.26.7` and server release `v8.0.44`.
The following upstream files were inspected at the panel's release tag:

- [QuotaPage.tsx](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/v1.26.7/src/features/quota/QuotaPage.tsx)
  renders the quota controls, account cards, and reset-window timeline.
- [QuotaTimeline.tsx](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/v1.26.7/src/features/quota/components/QuotaTimeline.tsx)
  draws account lanes and window boundaries.
- [quotaTimelineModel.ts](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/v1.26.7/src/features/quota/quotaTimelineModel.ts)
  derives reset windows from reported anchors and periods. Its projection concerns
  window boundaries, not a historical consumption curve.
- [useQuotaStore.ts](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/v1.26.7/src/stores/useQuotaStore.ts)
  holds current quota readings in a Zustand store across route changes. This
  store does not provide the durable observation history required by V1.

The inspected rendering path does not implement the two charts in Victor's
screenshots. Reuse the upstream provider readers and account controls where they
fit. Add the requested history and charts in our panel work. A fork of the Go
server alone does not include the panel's TypeScript source.

## Details visible in the screenshots

The screenshots are visual references, not evidence of the underlying algorithm.
The displayed numbers below are examples from those images, not Victor's usage.

| Area | Visible detail |
| --- | --- |
| Header | `Quota Management`, `12 credentials`, and `10 loaded`. The third image explicitly marks the xAI and Kimi cards as not loaded yet. |
| Provider navigation | `All` and provider tabs with account counts. Claude is selected in the first two images and has seven credentials. The third image underlines `All`, while the pointer hovers over Claude. |
| Account controls | `Show emails`, `Refresh all credentials`, a filename or email search, and a grouped account section. |
| Summary | `458% / 700% remaining`, approximately `2.3% per hour`, and a control to collapse the chart section. |
| Chart selection | Separate `Remaining quota` and `Drain rate` tabs share the summary and account filter. |
| Consumption chart | An orange line with an area fill, an hourly rate axis, a `1-hour average` label, and `Last 24 hours` selected. |
| Peaks and gaps | `Peak 11% per hour` and a caption stating that resets are excluded and gaps mean missing history. |
| Remaining quota chart | Solid observed values, a dashed projected line, an area fill, and a vertical `Now` marker. |
| Forecast horizon | `Next 7 days` is selected. The projection contains upward steps associated with resets. |
| Chart inspection | A hover tooltip shows a date, time, and remaining amount. A caption identifies the next reset and expected replenishment. |
| Additional controls | A collapsed `Forecast settings` section and selectors labeled `Ledger` and `Default`. The third image shows the ledger rows, but the selectors' alternative values remain unknown. |
| Visual style | A dark background, warm orange chart accents, restrained grid lines, rounded controls, and account details below the chart. |
| Collapsed charts | The third image hides the charts while retaining the numerical summary, search, provider cards, and account rows. |
| Provider overview | A horizontal strip shows Claude with seven credentials, Codex with three, and xAI and Kimi with one each. Claude exposes a model-specific seven-day metric and a `Show` control whose behavior is not visible. |
| Segmented balances | Codex shows `204% of 300%` for its weekly limit, three separate bar segments, and a reset countdown with an absolute date. The three segments are consistent with its three credentials, but no tooltip is shown. |
| Unloaded balances | xAI and Kimi each show `-- of 100%`, an unfilled bar, and `Not loaded yet`. An unloaded value is visually distinct from an exhausted quota. |
| Account ledger | Rows are grouped by provider. Each row includes an account label, provider, `Last seen`, and separate columns for the seven-day limit, five-hour limit, and model-specific seven-day limit. Each limit has a percentage and bar. |
| Per-window resets | Rows show a relative reset countdown and an absolute date, or `No reset pending`. This distinction is present even when the displayed balance is 100%. |
| Account availability | One row has a 0% weekly balance alongside a 100% five-hour balance and a 94% model-specific balance. A red `back in` countdown identifies that account's unavailable state. |
| Manual reset controls | Rows show `Resets remaining`, `Reset limits`, and `Refresh quota`. Some reset counts are unknown. The image does not establish reset eligibility, price, or whether the displayed actions are enabled. |

The rate formula, collection interval, persistence mechanism, forecast settings,
and other selector options cannot be determined from the images.

The third image retains the `458% / 700%` summary while `All` is underlined.
The image alone does not explain the summary's scope. V1 must label the provider
and window behind each total rather than copy that ambiguity.

## V1 backlog

Each item remains open until its acceptance criteria pass.

| ID | Item | Acceptance criteria |
| --- | --- | --- |
| V1-01 | Collect and retain quota history. | Save timestamped readings per account and limit window in SQLite. Collection continues with the panel closed. History survives a restart. Retain the last valid reading after a failed refresh and expose its age. |
| V1-02 | Filter by provider, account, window, and period. | The charts and summary describe the same selection. Include the existing provider tabs, account search, and loaded-account counts. Missing readings remain visible as missing. |
| V1-03 | Show remaining quota and recent pace together. | Show remaining amount, scope, unit, and recent rate. Any multi-account total identifies which accounts and windows contribute. Partially loaded groups disclose the missing accounts. |
| V1-04 | Draw the remaining quota history. | Draw observed values as a solid line with an area fill. Support inspection of time and value by pointer and keyboard. Preserve visible gaps when collection is missing. |
| V1-05 | Project remaining quota through resets. | Draw the forecast as a dashed line after a `Now` marker. Support the reference's next-seven-days view, tooltips, and a next-reset caption. Restore each account to its cap at a reset instead of adding a full cap to its existing balance. |
| V1-06 | Draw the consumption rate. | Include the reference's last-24-hours view and a rate averaged over one hour. Identify the averaging interval and valid coverage. Resets and collection gaps do not create consumption spikes or zero-consumption samples. |
| V1-07 | Show peaks and period summaries. | Report the highest observed rate and its interval. Include consumption by hour and day, daily bars, and a weekday-by-hour heatmap, as discussed before the screenshots. Mark summaries incomplete when observation coverage is incomplete. |
| V1-08 | Explain and adjust the forecast. | Provide a collapsible settings section that exposes the rate's observation period and the forecast's assumptions. Show when history is insufficient. Distinguish a reported reset from an inferred future recurrence. The screenshot does not specify the settings to copy. |
| V1-09 | Compare actual and sustainable pace. | Show the recent consumption rate beside remaining quota divided by time until reset. Identify projected exhaustion before reset and unused quota at a completed cycle. Alerts use these readings and disclose stale data. |
| V1-10 | Preserve account controls and privacy. | Refresh the selected account or all accounts without duplicate concurrent probes. Keep emails hidden until revealed. Retain existing search and account details. Include the visible ledger layout without inventing alternative values for its selector or the closed `Default` selector. |
| V1-11 | Match the reference's chart presentation. | Place the summary above the two chart tabs. Use a dark theme, orange line and area styling, readable legends, restrained grid lines, tooltips, and collapsible charts. Collapsing the charts preserves the numerical summary and access to provider cards and account rows. Keep controls usable on a narrow screen. Present times in America/Sao_Paulo. |
| V1-12 | Supply the allocation skill with monitor data. | Expose the latest quota, reset, observation time, and data status in a machine-readable snapshot. Add measured pace and forecasts when available. Preserve the allocation skill's existing policy ownership. |
| V1-13 | Summarize each provider above the account ledger. | Show its account count, labeled quota window, remaining total, and next reported reset. Segment a compatible multi-account balance by account, with account identity and value available on inspection. Display unloaded, stale, and exhausted balances distinctly. |
| V1-14 | Show aligned quota columns for each account. | Group rows by provider. Include the account label, provider, last observation time, and separate bars for every applicable session, weekly, and model-specific window. Show a countdown and absolute reset time, or an explicit absence of a reported reset. Preserve the meaning of each field on narrow screens. |
| V1-15 | Identify the window that limits availability. | An exhausted applicable window makes the relevant availability state unavailable even when another window is full. Name the limiting window and show its reported return time. Unknown quota is not exhaustion. Model-specific availability follows the provider's reported scope rather than an average of its windows. |
| V1-16 | Surface manual reset credits and existing actions. | Show a reported reset-credit count separately from normal scheduled renewal, or mark it unknown. Retain per-account refresh. Expose an existing manual-reset action only when provider support and eligibility are known. Preserve any existing disclosure of a charge or credit use. Monitoring never redeems a reset automatically. New provider redemption implementations are outside this item. |

## Calculation and data rules

- Treat percentage-based consumption as percentage points per hour or day. State
  whether a rate belongs to one account or a sum of account percentages.
- The example `458% / 700%` is consistent with summing percentages across seven
  accounts. It is not 458% available on one account. A sum of normalized
  percentages does not establish equal capacity across different subscription
  plans. Identify this aggregation and retain per-account values.
- Keep session and weekly windows separate. The same activity can affect both
  limits. Adding them would count the activity twice.
- Determine availability from the applicable limiting windows. A full session
  window cannot cancel an exhausted weekly window. Preserve the provider's
  model-specific scope when only some models are affected.
- Aggregate compatible windows only. Keep provider totals separate unless a
  common capacity unit is known. Do not present a cross-provider percentage as
  spendable capacity.
- Detect a reset or quota adjustment before calculating a consumption delta.
  A decrease in remaining quota within a continuous cycle can support a measured
  rate. An increase alone does not establish a reset.
- Quota readings may be rounded or delayed. Describe rates and peaks as observed
  between samples, and keep the sampling interval available for inspection.
- Do not infer zero usage from a failed read, an unloaded account, or a collection
  gap. Do not distribute an uncertain delta across hourly bins as exact usage.
- Label a forecast as an estimate. Keep projected account balances between zero
  and their caps. Show replenishment only for supported reset events, with any
  inferred recurrence identified as an assumption.
- Respect provider backoff and retain cached values after a failed probe. Normal
  quota monitoring must not generate model work or wake a dormant subscription
  session to manufacture a reading.

## Delivery checks

Use fixed observations and a controllable clock to verify the chart calculations.
Cover a steady decline, a reset with unused balance, staggered account resets,
an exhausted account, different windows, an unavailable account, a collection
gap, a quota adjustment, and insufficient forecast history. A reset must not
produce a consumption peak or push the forecast above its capacity.

Include an exhausted weekly limit with a full session limit, an exhausted
session limit with weekly quota remaining, a model-specific limit, and an
unloaded provider card. Verify that each state remains clear in the provider
overview and account ledger. A scheduled renewal must not appear as a manual
reset credit.

Verify that an observation taken while the panel is closed appears after it
reopens. Restart the collector and confirm that the stored history and charts
remain available. Compare the resulting panel with all three reference screenshots.

The V1 work must preserve the original installation at
`/Volumes/Arquivos/cliproxyapi-original`. No project attribution, T3 integration,
automatic model switching, or budget enforcement is part of this backlog.

## Local reference evidence

The supplied images and inspected upstream files are preserved outside Git at
`/Volumes/Arquivos/cliproxyapi-research/20261026-quota-visuals/`.
`sources.json` records their hashes and the research time. The screenshots are
`screenshots/drain-rate.png`, `screenshots/remaining-quota.png`, and
`screenshots/provider-overview-ledger.png`.
Keep these images outside the public repository because they contain another
installation's address and account inventory.
