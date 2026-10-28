import { expect, test } from 'bun:test';
import cases from './fixtures/quota-gaps.json';
import { quotaProjection, type QuotaObservation } from '@/features/quotaHistory/projection';
const now = Date.parse('2026-10-28T12:00:00Z');
for (const boundary of cases) {
  test(`${boundary.provider} gap and freshness at ${boundary.gapMs}ms`, () => {
    const series: QuotaObservation[] = Array.from({ length: 4 }, (_, i) => ({
      credential: 'fixture', provider: boundary.provider, windowId: 'weekly', label: 'Weekly',
      observedAtMs: now - (3 - i) * boundary.gapMs, remaining: 60 - i,
      resetAtMs: now + 86400000, periodHours: 168,
    }));
    const projection = quotaProjection(series, now);
    expect('observedHours' in projection).toBe(boundary.accepted);
    expect(quotaProjection(series, now + boundary.gapMs).status === 'stale').toBe(!boundary.accepted);
  });
}
