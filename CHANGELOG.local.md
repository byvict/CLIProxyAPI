# Local releases

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
