const HOUR_MS = 3_600_000;

export interface QuotaObservation {
  credential: string;
  provider: string;
  windowId: string;
  label: string;
  observedAtMs: number;
  remaining: number | null;
  resetAtMs: number | null;
  periodHours: number | null;
}

export type ProjectionBand = 'green' | 'blue' | 'yellow' | 'red';

export interface ProjectionEvidence {
  lookbackHours: number;
  observedHours: number;
  coverage: number;
  consumedPp: number;
  ratePpH: number;
  observedAtMs: number;
  provisional: boolean;
}

export type QuotaProjection =
  | { status: 'insufficient' | 'stale' | 'reset-passed' }
  | { status: 'exhausted'; observedAtMs: number }
  | (ProjectionEvidence & { status: 'no-consumption' | 'unstable' })
  | (ProjectionEvidence & {
      status: 'survives-reset';
      remainingAtReset: number;
    })
  | (ProjectionEvidence & {
      status: 'projected';
      exhaustsAtMs: number;
      resetAtMs: number;
      band: ProjectionBand;
    });

export const MAX_SAMPLE_GAP_MS = 45 * 60 * 1000;

export const maxSampleGapMs = (provider: string) =>
  (provider === 'meta' || provider === 'muse') ? 65 * 60_000 : MAX_SAMPLE_GAP_MS;

export function projectionBand(offsetMs: number): ProjectionBand {
  const distance = Math.abs(offsetMs) / HOUR_MS;
  if (distance <= 6) return 'green';
  if (distance <= 12) return 'blue';
  if (distance <= 24) return 'yellow';
  return 'red';
}

export function quotaProjection(
  observations: readonly QuotaObservation[],
  now: number
): QuotaProjection {
  const samples = observations
    .filter((sample) => sample.observedAtMs <= now)
    .sort((a, b) => a.observedAtMs - b.observedAtMs);
  const last = samples[samples.length - 1];
  if (!last || last.resetAtMs === null || last.remaining === null) {
    return { status: 'insufficient' };
  }
  if (last.resetAtMs <= now) return { status: 'reset-passed' };
  if (now - last.observedAtMs > maxSampleGapMs(last.provider)) return { status: 'stale' };
  if (last.remaining === 0) return { status: 'exhausted', observedAtMs: last.observedAtMs };
  if (last.periodHours === null || last.periodHours <= 0) return { status: 'insufficient' };

  const targetHours = Math.min(24, last.periodHours);
  let from = Math.max(now - targetHours * HOUR_MS, last.resetAtMs - last.periodHours * HOUR_MS);
  // A replenishment or capacity adjustment starts a new rate segment. Missing
  // readings leave gaps in that segment; they never count as idle time.
  for (let index = samples.length - 1; index > 0; index -= 1) {
    const previous = samples[index - 1];
    const current = samples[index];
    if (current.credential !== last.credential || current.windowId !== last.windowId) continue;
    if (
      previous.credential !== last.credential ||
      previous.windowId !== last.windowId ||
      previous.periodHours !== last.periodHours ||
      previous.resetAtMs === null ||
      Math.abs(previous.resetAtMs - last.resetAtMs) > 60_000
    )
      break;
    if (
      previous.remaining !== null &&
      current.remaining !== null &&
      current.remaining > previous.remaining
    ) {
      from = Math.max(from, current.observedAtMs);
      break;
    }
  }
  const lookbackMs = now - from;
  let consumed = 0;
  let coveredMs = 0;
  let intervals = 0;
  let recentConsumed = 0;
  let recentCoveredMs = 0;

  for (let index = 1; index < samples.length; index += 1) {
    const previous = samples[index - 1];
    const current = samples[index];
    if (current.observedAtMs <= from) continue;
    const duration = current.observedAtMs - previous.observedAtMs;
    if (
      duration <= 0 ||
      duration > maxSampleGapMs(last.provider) ||
      previous.credential !== last.credential ||
      current.credential !== last.credential ||
      previous.windowId !== last.windowId ||
      current.windowId !== last.windowId ||
      previous.periodHours !== last.periodHours ||
      current.periodHours !== last.periodHours ||
      previous.remaining === null ||
      current.remaining === null ||
      previous.resetAtMs === null ||
      current.resetAtMs === null ||
      Math.abs(current.resetAtMs - last.resetAtMs) > 60_000 ||
      current.resetAtMs <= current.observedAtMs
    ) {
      continue;
    }
    const overlapMs = current.observedAtMs - Math.max(from, previous.observedAtMs);
    if (
      Math.abs(previous.resetAtMs - current.resetAtMs) > 60_000 ||
      current.remaining > previous.remaining
    )
      continue;
    intervals += 1;
    consumed += ((previous.remaining - current.remaining) * overlapMs) / duration;
    coveredMs += overlapMs;
    const recentOverlap = Math.max(
      0,
      current.observedAtMs - Math.max(from, now - 6 * HOUR_MS, previous.observedAtMs)
    );
    recentConsumed += ((previous.remaining - current.remaining) * recentOverlap) / duration;
    recentCoveredMs += recentOverlap;
  }

  if (intervals < 2 || coveredMs < HOUR_MS) return { status: 'insufficient' };
  const ratePpH = consumed / (coveredMs / HOUR_MS);
  const coverage = Math.min(1, coveredMs / lookbackMs);
  const evidence: ProjectionEvidence = {
    lookbackHours: lookbackMs / HOUR_MS,
    observedHours: coveredMs / HOUR_MS,
    coverage,
    consumedPp: consumed,
    observedAtMs: last.observedAtMs,
    ratePpH,
    // Operational disclosure, not a statistical confidence score.
    provisional: lookbackMs < targetHours * HOUR_MS || coverage < 0.8 || consumed < 2,
  };
  if (consumed <= 0) return { ...evidence, status: 'no-consumption' };
  const recentRate = recentConsumed / (recentCoveredMs / HOUR_MS);
  if (
    coveredMs > 6 * HOUR_MS &&
    recentCoveredMs >= 4.8 * HOUR_MS &&
    (recentRate < ratePpH / 2 || recentRate > ratePpH * 2)
  ) {
    return { ...evidence, status: 'unstable' };
  }
  const exhaustsAtMs = last.observedAtMs + (last.remaining / ratePpH) * HOUR_MS;
  if (!Number.isFinite(exhaustsAtMs)) return { status: 'insufficient' };
  if (exhaustsAtMs >= last.resetAtMs) {
    return {
      ...evidence,
      status: 'survives-reset',
      remainingAtReset: Math.max(
        0,
        last.remaining - ratePpH * ((last.resetAtMs - last.observedAtMs) / HOUR_MS)
      ),
    };
  }
  return {
    ...evidence,
    status: 'projected',
    exhaustsAtMs,
    resetAtMs: last.resetAtMs,
    band: projectionBand(exhaustsAtMs - last.resetAtMs),
  };
}
