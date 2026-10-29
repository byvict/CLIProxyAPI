import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import '../src/i18n/index';
import { ProjectionSummary } from '../src/features/quotaHistory/ProjectionSummary';
import { ExhaustionMarker } from '../src/features/quotaHistory/ExhaustionMarker';
import { quotaObservations } from '../src/features/quotaHistory/observations';
import { buildTimelineLane } from '../src/features/quota/quotaTimelineModel';

const now = Date.parse('2026-10-27T15:00:00Z');
const hour = 3_600_000;

describe('quota history', () => {
  test('shows projected exhaustion after reset and exposes the basis', () => {
    const projection = {
      status: 'survives-reset' as const,
      remainingAtReset: 30,
      exhaustsAtMs: now + 16 * hour,
      resetAtMs: now + hour,
      band: 'yellow' as const,
      lookbackHours: 6,
      observedHours: 6,
      coverage: 1,
      consumedPp: 13,
      ratePpH: 2,
      observedAtMs: now,
      provisional: true,
    };
    expect(
      renderToStaticMarkup(
        createElement(ExhaustionMarker, {
          projection,
          startMs: now,
          endMs: now + 32 * hour,
        })
      )
    ).toContain('left:50%');
    const summary = renderToStaticMarkup(createElement(ProjectionSummary, { projection, now }));
    expect(summary).toContain('Expected to last until reset');
    expect(summary).toContain('Provisional');
    expect(summary).toContain('100% coverage');
    expect(summary).toContain('<details');
  });
  test('keeps account and model-scoped percentage windows independent', () => {
    const samples = quotaObservations(
      'a',
      'claude',
      {
        status: 'success',
        windows: [
          {
            id: 'seven-day',
            label: '7-day',
            usedPercent: 40,
            resetAtMs: now + hour,
            periodHours: 168,
          },
          {
            id: 'seven-day-fable',
            label: 'Fable',
            usedPercent: 70,
            resetAtMs: now + hour,
            periodHours: 168,
          },
        ],
      },
      now
    );
    expect(samples.map((sample) => [sample.windowId, sample.remaining])).toEqual([
      ['seven-day', 60],
      ['seven-day-fable', 30],
    ]);
    const unknown = quotaObservations(
      'b',
      'codex',
      { status: 'success', windows: [{ id: 'weekly', usedPercent: null }] },
      now
    );
    expect(unknown[0].remaining).toBeNull();
  });
  test('uses the same normalized window identity as the original timeline', () => {
    for (const [provider, quota] of [
      [
        'meta',
        {
          status: 'success',
          data: {
            windows: [{ id: 'weekly', usedPercent: 40, resetAt: (now + hour) / 1000 }],
          },
        },
      ],
      [
        'antigravity',
        {
          status: 'success',
          groups: [
            {
              id: 'models',
              buckets: [
                {
                  id: 'a',
                  remainingFraction: 0.6,
                  resetAtMs: now + hour,
                  periodHours: 168,
                },
              ],
            },
          ],
        },
      ],
      [
        'kimi',
        {
          status: 'success',
          rows: [
            {
              id: 'weekly',
              label: 'Weekly',
              used: 40,
              limit: 100,
              resetAtMs: now + hour,
              periodHours: 168,
            },
          ],
        },
      ],
    ] as const) {
      const observations = quotaObservations('a', provider, quota, now);
      const lane = buildTimelineLane({
        name: 'a',
        displayName: 'a',
        provider,
        quota,
      });
      expect(observations[0].windowId).toBe(lane.windowId);
      expect(observations[0].resetAtMs).toBe(lane.anchorMs);
      expect(observations[0].remaining).toBe(60);
    }
  });
  test('keeps a before-reset projection beyond the visible chart discoverable', () => {
    const markup = renderToStaticMarkup(
      createElement(ExhaustionMarker, {
        projection: {
          status: 'projected',
          exhaustsAtMs: now + 90 * hour,
          resetAtMs: now + 120 * hour,
          lookbackHours: 24,
          ratePpH: 1,
          band: 'red',
          observedHours: 12,
          coverage: 0.5,
          consumedPp: 12,
          observedAtMs: now,
          provisional: true,
        },
        startMs: now - hour,
        endMs: now + 48 * hour,
      })
    );
    expect(markup).toContain('data-band="red"');
    expect(markup).toContain('data-outside="after"');
    expect(markup).toContain('tabindex="0"');
    expect(markup).toContain('Provisional');
  });
});

test('timeline preserves the successful windows during refresh and error', () => {
  const success = { status: 'success', windows: [{ id: 'weekly', usedPercent: 50, periodHours: 168, resetAtMs: now + hour }] };
  const base = { provider: 'codex', name: 'fixture', displayName: 'Fixture', maxPeriodHours: 14 * 24 };
  const expected = buildTimelineLane({ ...base, quota: success });
  for (const status of ['loading', 'error']) {
    expect(buildTimelineLane({ ...base, quota: { status, retained: success } })).toEqual(expected);
  }
});
