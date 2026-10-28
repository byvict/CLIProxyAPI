import { describe, expect, test } from 'bun:test';
import { quotaProjection, type QuotaObservation } from '../src/features/quotaHistory/projection';

const hour = 3_600_000;
const now = Date.parse('2026-10-28T13:00:00Z');
function history(hours: number, remaining: number, rate: number, reset = now + 120 * hour) {
  return Array.from({ length: hours * 12 + 1 }, (_, i): QuotaObservation => ({
    credential: 'account',
    provider: 'codex',
    windowId: 'weekly',
    label: 'Weekly',
    observedAtMs: now - hours * hour + (i * hour) / 12,
    remaining: remaining + (hours - i / 12) * rate,
    resetAtMs: reset,
    periodHours: 168,
  }));
}

describe('cycle-aware exhaustion estimates', () => {
  test('does not hide missing time at the beginning of a new cycle', () => {
    const previous = history(12, 1, 4)
      .slice(0, 72)
      .map((s) => ({ ...s, resetAtMs: now - 6 * hour }));
    const result = quotaProjection([...previous, ...history(2, 87, 2, now + 162 * hour)], now);
    expect(result.status).toBe('projected');
    if (result.status !== 'projected') throw new Error(result.status);
    expect(result.lookbackHours).toBe(6);
    expect(result.coverage).toBeCloseTo(1 / 3);
  });
  test('future observations cannot change a past estimate', () => {
    const data = history(6, 87, 2);
    const future = { ...data[data.length - 1], observedAtMs: now + hour, remaining: 0 };
    expect(quotaProjection([...data, future], now)).toEqual(quotaProjection(data, now));
  });
  test('does not carry an intense previous cycle into the new cycle', () => {
    const previous = history(12, 1, 4)
      .slice(0, 72)
      .map((s) => ({ ...s, resetAtMs: now - 6 * hour }));
    const current = history(6, 87, 2, now + 162 * hour);
    const result = quotaProjection([...previous, ...current], now);
    expect(result.status).toBe('projected');
    if (result.status !== 'projected') throw new Error(result.status);
    expect(result.ratePpH).toBeCloseTo(2);
    expect(result.lookbackHours).toBeCloseTo(6);
    expect(result.coverage).toBeCloseTo(1);
    expect(result.provisional).toBe(true);
    expect(result.exhaustsAtMs).toBe(now + 43.5 * hour);
  });
  test('keeps a 24-hour base even just before reset', () => {
    const result = quotaProjection(history(24, 12, 1, now + 4 * 60_000), now);
    expect(result.status).toBe('survives-reset');
    if (result.status !== 'survives-reset') throw new Error(result.status);
    expect(result.lookbackHours).toBe(24);
    expect(result.remainingAtReset).toBeGreaterThan(11);
  });
  test('does not extrapolate a weekly quota from a ten-minute step', () => {
    expect(quotaProjection(history(1, 87, 6).slice(-3), now).status).toBe('insufficient');
  });
  test('distinguishes a young fully observed cycle from missing history', () => {
    const young = quotaProjection(history(6, 87, 2, now + 162 * hour), now);
    const missing = quotaProjection(history(6, 87, 2), now);
    expect(young.status).toBe('projected');
    expect(missing.status).toBe('projected');
    if (young.status !== 'projected' || missing.status !== 'projected')
      throw new Error('Expected estimates');
    expect(young.coverage).toBe(1);
    expect(missing.coverage).toBe(0.25);
    expect(missing.provisional).toBe(true);
  });
  test('reports unstable recent consumption without a precise marker', () => {
    const data = history(24, 30, 1);
    data.forEach((s) => {
      if (s.observedAtMs >= now - 6 * hour) s.remaining = 30;
    });
    const result = quotaProjection(data, now);
    expect(result.status).toBe('unstable');
  });
  test('keeps observed idle time and does not claim zero use across gaps', () => {
    const data = history(24, 87, 0);
    data.slice(0, 120).forEach((s) => {
      s.remaining = 90;
    });
    const result = quotaProjection([...data.slice(0, 120), ...data.slice(180)], now);
    expect(result.status).toBe('no-consumption');
    if (result.status !== 'no-consumption') throw new Error(result.status);
    expect(result.coverage).toBeLessThan(1);
  });
});
