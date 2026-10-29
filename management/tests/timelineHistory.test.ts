import { describe, expect, test } from 'bun:test';
import { historyTimelineLane } from '../src/features/quotaHistory/timelineHistory';
import { quotaObservations } from '../src/features/quotaHistory/observations';
import { buildTimelineLane } from '../src/features/quota/quotaTimelineModel';
import { quotaProjection } from '../src/features/quotaHistory/projection';
import type { QuotaObservation } from '../src/features/quotaHistory/projection';
import type { TimelineLaneInput } from '../src/features/quota/quotaTimelineModel';

const now = 1_800_000_000_000;
const input: TimelineLaneInput = {
  name: 'current.json',
  displayName: 'Current',
  provider: 'codex',
  quota: undefined,
};
const observation: QuotaObservation = {
  credential: 'current',
  provider: 'codex',
  windowId: 'weekly',
  label: 'Weekly',
  observedAtMs: now - 60_000,
  remaining: 42,
  resetAtMs: now + 86_400_000,
  periodHours: 168,
};
const lane = (samples: QuotaObservation[], overrides: Partial<TimelineLaneInput> = {}) =>
  historyTimelineLane({ ...input, ...overrides }, 'current', samples, now);

describe('recorded timeline bootstrap', () => {
  test('installed Claude and Codex window identities match live lanes', () => {
    for (const provider of ['claude', 'codex'] as const) {
      const id = provider === 'claude' ? 'seven-day' : 'weekly';
      const quota = { status: 'success', windows: [{ id, usedPercent: 58, periodHours: 168, resetAtMs: now + 86_400_000 }] };
      const samples = quotaObservations('current', provider, quota, now - 60_000);
      const live = buildTimelineLane({ ...input, provider, quota });
      const stored = lane(samples, { provider });
      expect(stored.windowId).toBe(live.windowId);
      expect(stored.remaining).toBe(live.remaining);
      expect(stored.anchorMs).toBe(live.anchorMs);
    }
  });
  test('uses only latest actual observation for current identity and provider', () => {
    const result = lane([
      { ...observation, remaining: 80, observedAtMs: now - 120_000 },
      observation,
      { ...observation, credential: 'deleted', remaining: 99, observedAtMs: now },
      { ...observation, provider: 'claude', remaining: 99, observedAtMs: now },
      { ...observation, remaining: 99, observedAtMs: now + 1 },
    ]);
    expect(result.remaining).toBe(42);
    expect(result.recordedAtMs).toBe(observation.observedAtMs);
    expect(result.resetCredits).toEqual([]);
    expect(historyTimelineLane(input, null, [observation], now).anchorMs).toBeNull();
    expect(lane([{ ...observation, credential: ' current ' }]).remaining).toBe(42);
  });
  test('latest unknown cannot restore an older balance or reset', () => {
    expect(lane([observation, { ...observation, observedAtMs: now, remaining: null }]).anchorMs).toBeNull();
    expect(
      lane([observation, { ...observation, observedAtMs: now, resetAtMs: null }]).anchorMs
    ).toBeNull();
    expect(
      lane([observation, { ...observation, observedAtMs: now, periodHours: 5 }]).periodHours
    ).toBe(5);
  });
  test('rejects invalid periods and clamps already-normalized remaining without inversion', () => {
    for (const periodHours of [null, 0, -1, Infinity]) expect(lane([{...observation, periodHours}]).anchorMs).toBeNull();
    expect(lane([{...observation, remaining: 140}]).remaining).toBe(100);
    expect(lane([{...observation, remaining: -5}]).remaining).toBe(0);
  });
  test('reset-passed balance is unknown and history stays reset-passed', () => {
    const expired = { ...observation, resetAtMs: now - 1 };
    expect(lane([expired]).remaining).toBeNull();
    expect(lane([expired]).anchorMs).toBe(now - 1);
    expect(quotaProjection([expired], now).status).toBe('reset-passed');
  });
  test('Codex standard weekly and five-hour windows outrank model variants', () => {
    const session = { ...observation, windowId: 'five-hour', periodHours: 5, remaining: 33 };
    const variant = {
      ...observation,
      windowId: 'spark-weekly',
      resetAtMs: now + 1_000,
      remaining: 1,
    };
    expect(lane([variant, session, observation]).windowId).toBe('weekly');
    expect(lane([variant, session, observation], { maxPeriodHours: 5 }).windowId).toBe('five-hour');
  });
  test('unknown standard Codex windows never become model-specific quota', () => {
    for (const [windowId, periodHours] of [['weekly', 168], ['five-hour', 5]] as const) {
      const standard = { ...observation, windowId, periodHours };
      const variant = { ...standard, windowId: `spark-${windowId}`, remaining: 1 };
      for (const unknown of [{ remaining: null }, { resetAtMs: null }]) {
        const latest = { ...standard, ...unknown, observedAtMs: now };
        const result = lane([standard, variant, latest], { maxPeriodHours: periodHours });
        expect(result.remaining).toBeNull();
        expect(result.anchorMs).toBeNull();
      }
    }
  });
  test('successful live or retained response including empty and unknown is authoritative', () => {
    expect(lane([observation], { quota: { status: 'success' } }).anchorMs).toBeNull();
    expect(
      lane([observation], { quota: { status: 'loading', retained: { status: 'success' } } })
        .anchorMs
    ).toBeNull();
    const live = {
      status: 'success',
      windows: [{ id: 'weekly', periodHours: 168, resetAtMs: now + 2_000, usedPercent: 90 }],
    };
    expect(lane([observation], { quota: live }).remaining).toBe(10);
    const unknown = {
      ...live,
      windows: [{ ...live.windows[0], resetAtMs: null, usedPercent: null }],
    };
    expect(lane([observation], { quota: unknown }).anchorMs).toBeNull();
    expect(lane([observation], { quota: { ...live, retained: { status: 'success' } } }).anchorMs).toBeNull();
  });
});

for (const maxPeriodHours of [5, 168, 336]) {
  test(`Claude selects its account quota in the ${maxPeriodHours}-hour view, live and recorded`, () => {
    const windows = [
      { id: 'seven-day-fable', usedPercent: 0, periodHours: 168, resetAtMs: now + 144 * 3_600_000 },
      { id: 'seven-day', usedPercent: 29, periodHours: 168, resetAtMs: now + 144 * 3_600_000 + 490 },
      { id: 'five-hour', usedPercent: 22, periodHours: 5, resetAtMs: now + 3_600_000 },
    ];
    const quota = { status: 'success', windows };
    const provider = 'claude';
    const samples = quotaObservations('current', provider, quota, now - 60_000);
    const expectedId = maxPeriodHours === 5 ? 'five-hour' : 'seven-day';
    for (const result of [
      buildTimelineLane({ ...input, provider, quota, maxPeriodHours }),
      lane(samples, { provider, maxPeriodHours }),
    ]) {
      expect(result.windowId).toBe(expectedId);
      expect(result.remaining).toBe(maxPeriodHours === 5 ? 78 : 71);
    }
  });
}
