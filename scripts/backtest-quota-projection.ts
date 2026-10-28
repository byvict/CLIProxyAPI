// Run with Bun. The database is read-only; output contains no account identifiers.
import { Database } from 'bun:sqlite';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import {
  maxSampleGapMs,
  quotaProjection,
  type QuotaObservation,
} from '../management/src/features/quotaHistory/projection';

const [databasePath, baselinePath] = process.argv.slice(2);
if (!databasePath || !baselinePath)
  throw new Error(
    'Usage: bun scripts/backtest-quota-projection.ts <quota-history.sqlite> <legacy-projection.ts>'
  );
const baseline = await import(pathToFileURL(resolve(baselinePath)).href);
const db = new Database(databasePath, { readonly: true });
const rows = db
  .query<QuotaObservation, []>(
    `SELECT credential, provider, window_id AS windowId,
  label, observed_at_ms AS observedAtMs, remaining, reset_at_ms AS resetAtMs,
  period_hours AS periodHours FROM observations WHERE period_hours = 168 ORDER BY observed_at_ms`
  )
  .all();
db.close();
const hour = 3_600_000;
const outcomeGapMs = (provider: string) => ['meta', 'muse'].includes(provider) ? 65 * 60_000 : 45 * 60_000;
const groups = new Map<string, QuotaObservation[]>();
for (const row of rows) {
  const key = JSON.stringify([row.credential, row.windowId, row.periodHours]);
  const group = groups.get(key) ?? [];
  group.push(row);
  groups.set(key, group);
}
function cycleRate(past: QuotaObservation[], last: QuotaObservation) {
  let consumed = 0,
    observed = 0;
  for (let i = past.length - 1; i > 0; i--) {
    const a = past[i - 1],
      b = past[i];
    if (
      a.resetAtMs === null ||
      last.resetAtMs === null ||
      Math.abs(a.resetAtMs - last.resetAtMs) > 60_000 ||
      (a.remaining !== null && b.remaining !== null && b.remaining > a.remaining)
    )
      break;
    const duration = b.observedAtMs - a.observedAtMs;
    if (
      duration <= 0 ||
      duration > maxSampleGapMs(last.provider) ||
      a.remaining === null ||
      b.remaining === null
    )
      continue;
    consumed += a.remaining - b.remaining;
    observed += duration;
  }
  return observed >= hour ? consumed / (observed / hour) : null;
}
const eligibility = { origins: 0, legacy: 0, proposed: 0, newlyEligible: 0, suppressed: 0, originsWithOutcome: 0 };
const buckets = new Map<
  string,
  {
    candidates: number;
    paired: number;
    suppressed: Record<string, number>;
    errors: Record<string, number[]>;
    falseAlarms: Record<string, number>;
    missed: Record<string, number>;
    observedExhaustions: number;
  }
>();
for (const group of groups.values()) {
  let previousAnchor = -Infinity;
  for (let index = 0; index < group.length; index++) {
    const last = group[index],
      now = last.observedAtMs;
    if (
      now - previousAnchor < hour ||
      last.remaining === null ||
      last.resetAtMs === null ||
      last.remaining === 0
    )
      continue;
    previousAnchor = now;
    const resetAtMs = last.resetAtMs;
    const past = group.slice(0, index + 1).filter((s) => s.observedAtMs >= now - 30 * 24 * hour);
    const old = baseline.quotaProjection(past, now);
    const current = quotaProjection(past, now);
    const rates = {
      legacy: 'ratePpH' in old && old.status !== 'unstable' ? old.ratePpH : null,
      cycle: cycleRate(past, last),
      proposed: 'ratePpH' in current && current.status !== 'unstable' ? current.ratePpH : null,
    };
    eligibility.origins++;
    if (rates.legacy !== null) eligibility.legacy++;
    if (rates.proposed !== null) eligibility.proposed++;
    if (rates.legacy === null && rates.proposed !== null) eligibility.newlyEligible++;
    if (rates.legacy !== null && rates.proposed === null) eligibility.suppressed++;
    let hasOutcome = false;
    for (const horizon of [6, 24]) {
      const target = group.slice(index + 1).find((s) => s.observedAtMs >= now + horizon * hour);
      if (
        !target ||
        target.remaining === null ||
        target.resetAtMs === null ||
        target.observedAtMs - now - horizon * hour > outcomeGapMs(last.provider) ||
        target.observedAtMs >= last.resetAtMs ||
        Math.abs(target.resetAtMs - last.resetAtMs) > 60_000
      )
        continue;
      const future = group.slice(index + 1, group.indexOf(target) + 1);
      let prior = last;
      const uninterruptedCycle = future.every((s) => {
        const same =
          s.resetAtMs !== null &&
          Math.abs(s.resetAtMs - resetAtMs) <= 60_000 &&
          (s.remaining === null || prior.remaining === null || s.remaining <= prior.remaining);
        prior = s;
        return same;
      });
      if (!uninterruptedCycle) continue;
      if (!hasOutcome) eligibility.originsWithOutcome++;
      hasOutcome = true;
      const key = `${last.provider}:${horizon}h`;
      const bucket = buckets.get(key) ?? {
        candidates: 0,
        paired: 0,
        suppressed: {},
        errors: { legacy: [], cycle: [], proposed: [] },
        falseAlarms: { legacy: 0, cycle: 0, proposed: 0 },
        missed: { legacy: 0, cycle: 0, proposed: 0 },
        observedExhaustions: 0,
      };
      buckets.set(key, bucket);
      bucket.candidates++;
      if (rates.proposed === null)
        bucket.suppressed[current.status] = (bucket.suppressed[current.status] ?? 0) + 1;
      if (Object.values(rates).some((rate) => rate === null)) continue;
      bucket.paired++;
      if (target.remaining === 0) bucket.observedExhaustions++;
      for (const [method, rate] of Object.entries(rates)) {
        if (rate === null) continue;
        const predicted = Math.max(0, last.remaining - rate * ((target.observedAtMs - now) / hour));
        bucket.errors[method].push(predicted - target.remaining);
        if (predicted === 0 && target.remaining > 0) bucket.falseAlarms[method]++;
        if (predicted > 0 && target.remaining === 0) bucket.missed[method]++;
      }
    }
  }
}
const results = [...buckets].map(([window, bucket]) => ({
  window,
  candidates: bucket.candidates,
  paired: bucket.paired,
  suppressed: bucket.suppressed,
  observedExhaustions: bucket.observedExhaustions,
  methods: Object.entries(bucket.errors).map(([method, errors]) => ({
    method,
    meanAbsoluteErrorPp: errors.length
      ? errors.reduce((sum, e) => sum + Math.abs(e), 0) / errors.length
      : null,
    meanSignedErrorPp: errors.length ? errors.reduce((sum, e) => sum + e, 0) / errors.length : null,
    falseAlarms: bucket.falseAlarms[method],
    missedExhaustions: bucket.missed[method],
  })),
}));
console.log(
  JSON.stringify(
    {
      methodology:
        'Hourly origins; actual observations within a fixed 45-minute tolerance (65 for Muse) of 6h/24h; no future inputs; same cycle without replenishment; identical eligible cases for all methods. Positive signed error overestimates remaining quota.',
      limitations:
        'Overlapping predictions are correlated. Historical collection gaps and rounded balances limit inference. Suppression changes availability. This is descriptive validation, not calibration or a confidence interval.',
      eligibility,
      results,
    },
    null,
    2
  )
);
