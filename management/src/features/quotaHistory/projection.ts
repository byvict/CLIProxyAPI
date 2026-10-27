import { HOUR_MS } from '@/utils/time/durations';

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

export type QuotaProjection =
  | { status: 'insufficient' | 'stale' | 'reset-passed' | 'no-consumption' }
  | {
      status: 'projected';
      exhaustsAtMs: number;
      resetAtMs: number;
      lookbackHours: number;
      ratePpH: number;
      band: ProjectionBand;
      observedHours: number;
      coverage: number;
    };

export const MAX_SAMPLE_GAP_MS = 15 * 60 * 1000;

export const maxSampleGapMs = (provider: string) =>
  provider === 'meta' ? 65 * 60_000 : MAX_SAMPLE_GAP_MS;

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
  const samples = observations.filter((sample) => sample.observedAtMs <= now);
  const last = samples[samples.length - 1];
  if (!last || last.resetAtMs === null || last.remaining === null) {
    return { status: 'insufficient' };
  }
  if (last.resetAtMs <= now) return { status: 'reset-passed' };
  if (now - last.observedAtMs > maxSampleGapMs(last.provider)) return { status: 'stale' };

  const lookbackMs = last.resetAtMs - now;
  const from = now - lookbackMs;
  let consumed = 0;
  let coveredMs = 0;
  let intervals = 0;

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
  }

  // Use only observed consumption inside the requested X-hour range. Report
  // partial coverage instead of suppressing an otherwise measurable projection.
  if (intervals < 2 || coveredMs < 5 * 60_000) return { status: 'insufficient' };
  if (consumed <= 0 && last.remaining > 0) return { status: 'no-consumption' };

  const ratePpH = consumed / (coveredMs / HOUR_MS);
  const exhaustsAtMs =
    last.remaining === 0
      ? last.observedAtMs
      : last.observedAtMs + (last.remaining / ratePpH) * HOUR_MS;
  if (!Number.isFinite(exhaustsAtMs)) return { status: 'insufficient' };
  return {
    status: 'projected',
    exhaustsAtMs,
    resetAtMs: last.resetAtMs,
    lookbackHours: lookbackMs / HOUR_MS,
    observedHours: coveredMs / HOUR_MS,
    coverage: Math.min(1, coveredMs / lookbackMs),
    ratePpH,
    band: projectionBand(exhaustsAtMs - last.resetAtMs),
  };
}
