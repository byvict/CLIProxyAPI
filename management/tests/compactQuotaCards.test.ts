import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { AntigravityQuotaBody } from '@/features/quota/providers/antigravity/AntigravityQuotaBody';
import { ClaudeQuotaBody } from '@/features/quota/providers/claude/ClaudeQuotaBody';
import { CodexQuotaBody } from '@/features/quota/providers/codex/CodexQuotaBody';
import { MetaQuotaBody } from '@/features/quota/providers/meta/MetaQuotaBody';
import { XaiQuotaBody } from '@/features/quota/providers/xai/XaiQuotaBody';
import { QUOTA_CLASS_KEYS, bindQuotaClasses } from '@/features/quota/types';
import { normalizeClaudeUsageSnapshot } from '@/services/api/claudeUsage';
import { buildXaiBillingSummary } from '@/utils/quota';
import type {
  AntigravityQuotaState,
  ClaudeQuotaState,
  CodexQuotaState,
  MetaQuotaState,
  XaiQuotaState,
} from '@/types';
import claudeUsage from './fixtures/claude-oauth-usage.json';

const classes = bindQuotaClasses(
  Object.fromEntries(QUOTA_CLASS_KEYS.map((key) => [key, key])),
  'compact-card-test'
);
const translations = i18n.cloneInstance({ lng: 'en' });
const render = (body: ReturnType<typeof createElement>) =>
  renderToStaticMarkup(createElement(I18nextProvider, { i18n: translations }, body));

describe('quota card summaries', () => {
  test('Antigravity renders a plain plan without model groups or quota windows', () => {
    const quota: AntigravityQuotaState = {
      status: 'success',
      subscription: { plan: 'ultra', tierId: 'g1-ultra-tier', tierName: 'Ultra' },
      serverTimeOffsetMs: 0,
      groups: [
        {
          id: 'gemini-models',
          label: 'Gemini Models',
          description: 'Models within this group: Gemini Flash, Gemini Pro',
          buckets: [
            {
              id: 'gemini-5h',
              label: '5-hour limit',
              window: '5h',
              remainingFraction: 1,
              resetTime: '2026-10-29T00:00:00Z',
            },
          ],
        },
      ],
    };
    const markup = render(createElement(AntigravityQuotaBody, { quota, classes, summary: true }));
    expect(markup).toContain('<span class="codexPlanValue">Ultra</span>');
    expect(markup).not.toContain('premiumPlanValue');
    expect(markup).not.toContain('Gemini Models');
    expect(markup).not.toContain('5-hour limit');
    expect(markup).not.toContain('quotaBar');
  });

  test('Claude keeps plan and dollar balances without extra usage or quota windows', () => {
    const snapshot = normalizeClaudeUsageSnapshot(claudeUsage)!;
    const quota: ClaudeQuotaState = {
      status: 'success',
      planType: 'plan_max',
      extraUsage: snapshot.extraUsage,
      dollarWindows: snapshot.dollarWindows,
      windows: [{ id: 'five_hour', label: '5-hour limit', usedPercent: 42, resetLabel: null }],
    };
    const markup = render(createElement(ClaudeQuotaBody, { quota, classes, summary: true }));
    expect(markup).toContain('Max');
    expect(markup).toContain('iguana_necktie');
    expect(markup).toContain('$94.96 remaining');
    expect(markup).not.toContain('Extra Usage');
    expect(markup).not.toContain('5-hour limit');
    expect(markup).not.toContain('quotaBar');
  });

  test.each([
    ['62500.0000000000', '62500'],
    ['12.340000', '12.34'],
    ['0.000000001000', '0.000000001'],
    ['0.000', '0'],
    ['42', '42'],
  ])('Codex renders a plain plan and trims insignificant zeros from %s', (balance, expected) => {
    const quota: CodexQuotaState = {
      status: 'success',
      planType: 'pro',
      creditBalance: balance,
      rateLimitResetCreditsAvailableCount: 2,
      windows: [{ id: 'weekly', label: 'Weekly limit', usedPercent: 48, resetLabel: null }],
    };
    const markup = render(createElement(CodexQuotaBody, { quota, classes, summary: true }));
    expect(markup).toContain('<span class="codexPlanValue">Pro 200</span>');
    expect(markup).toContain(`<span class="codexPlanValue">${expected}</span>`);
    expect(markup).toContain('Manual resets');
    expect(markup).not.toContain('elitePlanValue');
    expect(markup).not.toContain('Weekly limit');
    expect(markup).not.toContain('quotaBar');
  });

  test('Grok renders a plain plan and separates the breakdown title, labels and values without quota windows', () => {
    const billing = buildXaiBillingSummary({
      currentPeriod: { type: 'USAGE_PERIOD_TYPE_WEEKLY' },
      creditUsagePercent: 6,
      productUsage: [
        { product: 'GrokBot', usagePercent: 5 },
        { product: 'GrokBuild', usagePercent: 1 },
      ],
    })!;
    billing.planLabel = 'SuperGrok Heavy';
    billing.planTier = 'elite';
    const quota: XaiQuotaState = {
      status: 'success',
      billing,
    };
    const markup = render(createElement(XaiQuotaBody, { quota, classes, summary: true }));
    expect(markup).toContain('<span class="codexPlanValue">SuperGrok Heavy</span>');
    expect(markup).toContain(
      '<div class="codexResetCreditsTitle">Usage breakdown (shared quota)</div>'
    );
    expect(markup).toContain('<span class="codexPlanLabel">GrokBot usage</span>');
    expect(markup).toContain('<span class="codexPlanValue">5%</span>');
    expect(markup).toContain('<span class="codexPlanValue">1%</span>');
    expect(markup).not.toContain('elitePlanValue');
    expect(markup).not.toContain('premiumPlanValue');
    expect(markup).not.toContain('Weekly limit');
    expect(markup).not.toContain('quotaBar');
  });

  test('Meta keeps its plan without quota windows', () => {
    const quota: MetaQuotaState = {
      status: 'success',
      data: {
        planName: 'Muse Code Max Usage',
        isSubscriptionActive: true,
        windows: [{ id: 'weekly', usedPercent: 57 }],
      },
    };
    const markup = render(createElement(MetaQuotaBody, { quota, classes, summary: true }));
    expect(markup).toContain('Muse Code Max Usage');
    expect(markup).not.toContain('quotaRow');
    expect(markup).not.toContain('quotaBar');
  });
});
