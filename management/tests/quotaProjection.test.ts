import { describe, expect, test } from 'bun:test';
import { HOUR_MS } from '../src/utils/time/durations';
import {
  projectionBand,
  quotaProjection,
  type QuotaObservation,
} from '../src/features/quotaHistory/projection';

const now = Date.parse('2026-10-27T15:00:00Z');
const reset = now + 24 * HOUR_MS;

function samples(remaining: number, rate: number): QuotaObservation[] {
  return Array.from({ length: 289 }, (_, index) => {
    const hoursAgo = (288 - index) / 12;
    return {
      credential: 'claude-account',
      provider: 'claude',
      windowId: 'seven-day',
      label: '7-day limit',
      remaining: remaining + hoursAgo * rate,
      observedAtMs: now - hoursAgo * HOUR_MS,
      resetAtMs: reset,
      periodHours: 168,
    };
  });
}

describe('symmetric exhaustion bands around reset', () => {
  for (const sign of [-1, 1]) {
    for (const [hours, band] of [
      [0, 'green'],
      [6, 'green'],
      [6.01, 'blue'],
      [12, 'blue'],
      [12.01, 'yellow'],
      [24, 'yellow'],
      [24.01, 'red'],
    ] as const) {
      test(`${sign * hours} hours is ${band}`, () => {
        expect(projectionBand(sign * hours * HOUR_MS)).toBe(band);
      });
    }
  }
  test('projects after reset without adding renewed quota', () => {
    const projection = quotaProjection(samples(36, 1), now);
    expect(projection.status).toBe('projected');
    if (projection.status !== 'projected') throw new Error('Expected a projection');
    expect(projection.exhaustsAtMs).toBe(now + 36 * HOUR_MS);
    expect(projection.band).toBe('blue');
    expect(projection.lookbackHours).toBe(24);
  });
  test('projects before reset at the corresponding symmetric band', () => {
    const projection = quotaProjection(samples(12, 1), now);
    expect(projection.status).toBe('projected');
    if (projection.status !== 'projected') throw new Error('Expected a projection');
    expect(projection.exhaustsAtMs).toBe(now + 12 * HOUR_MS);
    expect(projection.band).toBe('blue');
  });
  test('uses exactly the preceding X hours, including the boundary overlap', () => {
    const history = samples(24, 1);
    history[0].remaining = 90;
    const projection = quotaProjection(history, now + 2.5 * 60_000);
    expect(projection.status).toBe('projected');
    if (projection.status !== 'projected') throw new Error('Expected a projection');
    expect(projection.lookbackHours).toBeCloseTo(24 - 2.5 / 60);
    expect(projection.ratePpH).toBeCloseTo(1);
  });
  test('shows a projection with explicit coverage when X hours are not yet collected', () => {
    const projection = quotaProjection(samples(24, 1).slice(144), now);
    expect(projection.status).toBe('projected');
    if (projection.status !== 'projected') throw new Error('Expected a projection');
    expect(projection.lookbackHours).toBe(24);
    expect(projection.observedHours).toBeCloseTo(12);
    expect(projection.coverage).toBeCloseTo(0.5);
    expect(projection.exhaustsAtMs).toBe(now + 24 * HOUR_MS);
  });
  test('excludes gaps, resets and adjustments and reports partial coverage', () => {
    const complete = samples(24, 1);
    const gap = [...complete.slice(0, 12), ...complete.slice(18)];
    const projection = quotaProjection(gap, now);
    expect(projection.status).toBe('projected');
    if (projection.status !== 'projected') throw new Error('Expected a projection');
    expect(projection.ratePpH).toBeCloseTo(1);
    expect(projection.coverage).toBeLessThan(1);
    const adjusted = samples(24, 1);
    adjusted.slice(12).forEach((sample) => {
      sample.remaining += 10;
    });
    const afterAdjustment = quotaProjection(adjusted, now);
    expect(afterAdjustment.status).toBe('projected');
    if (afterAdjustment.status !== 'projected') throw new Error('Expected a projection');
    expect(afterAdjustment.ratePpH).toBeCloseTo(1);
  });
  test('has no finite exhaustion marker for a zero consumption rate', () => {
    expect(quotaProjection(samples(24, 0), now).status).toBe('no-consumption');
  });
  test('uses the full lookback across a confirmed reset without counting replenishment', () => {
    const history = samples(24, 1);
    const oldReset = now - 12 * HOUR_MS;
    history.forEach((sample) => {
      if (sample.observedAtMs < oldReset) {
        sample.resetAtMs = oldReset;
        sample.remaining = (oldReset - sample.observedAtMs) / HOUR_MS;
      }
    });
    const projection = quotaProjection(history, now);
    expect(projection.status).toBe('projected');
    if (projection.status !== 'projected') throw new Error('Expected a projection');
    expect(projection.ratePpH).toBeCloseTo(1);
    expect(projection.lookbackHours).toBe(24);
    expect(projection.band).toBe('green');
  });
  test('suspends stale observations and passed resets', () => {
    expect(quotaProjection(samples(24, 1), now + HOUR_MS).status).toBe('stale');
    expect(quotaProjection(samples(24, 1), reset).status).toBe('reset-passed');
  });
});
