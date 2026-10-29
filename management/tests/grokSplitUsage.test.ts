import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import i18n from '../src/i18n';
import { QuotaTimeline } from '../src/features/quota/components/QuotaTimeline';
import { buildTimelineLane } from '../src/features/quota/quotaTimelineModel';
import { buildXaiBillingSummary } from '../src/utils/quota';
import type { XaiQuotaState } from '../src/types';

const now = new Date('2026-10-29T15:00:00Z').getTime();
const resetAt = '2026-11-03T12:00:00Z';
const translations = i18n.cloneInstance({ lng: 'en' });

function quota(bot: number | null = 29, total: number | null = 56): XaiQuotaState {
  return {
    status: 'success',
    billing: buildXaiBillingSummary({
      currentPeriod: {
        type: 'USAGE_PERIOD_TYPE_WEEKLY',
        start: '2026-10-27T12:00:00Z',
        end: resetAt,
      },
      creditUsagePercent: total,
      productUsage: [
        { product: 'GrokBot', usagePercent: bot },
        { product: 'GrokBuild', usagePercent: 27 },
      ],
    }),
  };
}

function render(state: XaiQuotaState, offset = 0, at = now) {
  return renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n: translations },
      createElement(QuotaTimeline, {
        entries: [{ file: { name: 'grok-fixture.json', type: 'xai' }, type: 'xai' }],
        quotaFor: () => state,
        displayNameFor: (name: string) => name,
        resolvedTheme: 'dark',
        now: at,
        initialOffset: offset,
      })
    )
  );
}

describe('GrokBot shared quota segment', () => {
  test('hatches 29% of the full bar inside 56% total usage', () => {
    const markup = render(quota());
    expect(markup).toContain('style="width:56%"');
    expect(markup).toContain('style="width:29%" role="img" aria-label="GrokBot usage 29%"');
    expect(markup.match(/aria-label="GrokBot usage/g)).toHaveLength(1);
  });

  test.each([null, 0, -3, NaN, Infinity])(
    'does not draw missing or unusable bot usage %s',
    (bot) => {
      expect(render(quota(bot))).not.toContain('aria-label="GrokBot usage');
    }
  );

  test('does not draw a split when total usage is unavailable', () => {
    expect(render(quota(29, null))).not.toContain('aria-label="GrokBot usage');
  });

  test('caps the bot segment at total usage', () => {
    const lane = buildTimelineLane({
      name: 'grok-fixture.json',
      displayName: 'Grok',
      provider: 'xai',
      quota: quota(75),
    });
    expect(lane.remaining).toBe(44);
    expect(lane.grokBotUsedPercent).toBe(56);
  });

  test('omits the overlay when the provider has no split', () => {
    const state = quota();
    if (!state.billing) throw new Error('Expected billing fixture');
    state.billing.productUsage = [];
    expect(render(state)).toContain('style="width:56%"');
    expect(render(state)).not.toContain('aria-label="GrokBot usage');
  });

  test('does not reuse the split after reset or on past and future windows', () => {
    expect(render(quota(), -2)).not.toContain('aria-label="GrokBot usage');
    expect(render(quota(), 2)).not.toContain('aria-label="GrokBot usage');
    expect(render(quota(), 0, new Date(resetAt).getTime())).not.toContain(
      'aria-label="GrokBot usage'
    );
  });
});
