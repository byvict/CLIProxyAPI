import { afterAll, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n/index';
import { QuotaTimeline } from '../src/features/quota/components/QuotaTimeline';
import { buildTimelineLane } from '../src/features/quota/quotaTimelineModel';
import { historyTimelineLane } from '../src/features/quotaHistory/timelineHistory';
import { quotaObservations } from '../src/features/quotaHistory/observations';
import { buildAntigravityQuotaGroups } from '../src/utils/quota/builders';

const now = new Date('2026-10-29T13:10:00Z').getTime();
const bucket = (id: string, window: string, remainingFraction: number, resetTime: string) => ({
  bucketId: id,
  displayName: window === '5h' ? 'Five Hour Limit Remaining' : 'Weekly Limit Remaining',
  window,
  remainingFraction,
  resetTime,
});
const quota = {
  status: 'success' as const,
  groups: buildAntigravityQuotaGroups({
    groups: [
      {
        displayName: 'Gemini Models',
        buckets: [
          bucket('gemini-5h', '5h', 1, '2026-10-29T18:05:13Z'),
          bucket('gemini-weekly', 'weekly', 0.6143591, '2026-11-04T22:05:13Z'),
        ],
      },
      {
        displayName: 'Claude and GPT Models',
        buckets: [
          bucket('3p-5h', '5h', 0.9585354, '2026-10-29T17:52:39Z'),
          bucket('3p-weekly', 'weekly', 0.97926766, '2026-11-05T12:52:39Z'),
        ],
      },
    ],
  }),
};
const input = {
  name: 'antigravity.json',
  displayName: 'antigravity.json',
  provider: 'antigravity' as const,
  quota,
  maxPeriodHours: 14 * 24,
};
const render = (initialMode: 'weekly' | 'session', quotaFor: () => unknown = () => quota) =>
  renderToStaticMarkup(
    createElement(QuotaTimeline, {
      entries: [{ file: { name: 'antigravity.json', type: 'antigravity' }, type: 'antigravity' }],
      quotaFor: quotaFor as never,
      displayNameFor: (name: string) => name,
      resolvedTheme: 'dark',
      now,
      initialMode,
    })
  );
const laneLabels = (markup: string) =>
  [...markup.matchAll(/>([^<>]+) <span[^>]*>\(([^)]+)\)<\/span>/g)].map(
    ([, label, weekly]) => `${label} ${weekly}`
  );

describe('Antigravity quota pools in the timeline', () => {
  const language = i18n.language;
  beforeAll(() => i18n.changeLanguage('en'));
  afterAll(() => i18n.changeLanguage(language));

  test('draws the Claude & GPT pool as an Other lane below the Gemini lane', () => {
    expect(laneLabels(render('weekly'))).toEqual(['Antigravity 61%', 'Other 98%']);
    expect(laneLabels(render('session'))).toEqual(['Antigravity 61%', 'Other 98%']);
  });

  test('each pool lane reads only its own buckets', () => {
    const gemini = buildTimelineLane({ ...input, pool: 'gemini' });
    const thirdParty = buildTimelineLane({ ...input, pool: 'third-party' });
    expect(gemini.windowId).toBe('gemini-models/gemini-weekly');
    expect(gemini.remaining).toBe(61);
    expect(thirdParty.windowId).toBe('claude-and-gpt-models/3p-weekly');
    expect(thirdParty.remaining).toBe(98);
    expect(thirdParty.limits.map((limit) => limit.remaining)).toEqual([96, 98]);
    expect(buildTimelineLane({ ...input, pool: 'third-party', maxPeriodHours: 5 }).windowId).toBe(
      'claude-and-gpt-models/3p-5h'
    );
  });

  test('recorded readings keep each pool on its own lane before the provider replies', () => {
    const samples = quotaObservations('agy', 'antigravity', quota, now - 60_000);
    const stored = (pool: 'gemini' | 'third-party') =>
      historyTimelineLane({ ...input, quota: undefined, pool }, 'agy', samples, now);
    expect(stored('gemini').windowId).toBe('gemini-models/gemini-weekly');
    expect(stored('third-party').windowId).toBe('claude-and-gpt-models/3p-weekly');
    expect(stored('third-party').remaining).toBeCloseTo(97.93, 2);
  });
});
