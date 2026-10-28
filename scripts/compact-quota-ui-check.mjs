import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
const [origin, build, out] = process.argv.slice(2);
if (!origin || !build || !out) throw new Error('Usage: compact-quota-ui-check.mjs <origin> <management-build> <evidence-root>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
await mkdir(out, { recursive: true });
const now = Date.now();
const future = (days) => new Date(now + days * 86400000).toISOString();
const files = ['claude', 'antigravity', 'codex', 'xai', 'meta'].map((type) => ({ name: `${type}-fixture@example.invalid.json`, type, provider: type, auth_index: `fixture-${type}`, status: 'active', project_id: 'fixture-project', chatgpt_subscription_active_until: future(12) }));
const claude = JSON.parse(await readFile(`${build}/tests/fixtures/claude-oauth-usage.json`, 'utf8'));
claude.five_hour.resets_at = future(0.1);
claude.seven_day.resets_at = future(2);
claude.seven_day_sonnet.resets_at = future(2);
claude.iguana_necktie.resets_at = future(8);
delete claude.additional_credit_balance;
claude.cedar_ember = { eligible: false, grants: [] };
const antigravity = { groups: [{ displayName: 'Gemini Models', description: 'Models within this group: Gemini Flash, Gemini Pro', buckets: [{ bucketId: 'gemini-5h', displayName: '5-hour limit', window: '5h', remainingFraction: 1, resetTime: future(0.2) }, { bucketId: 'gemini-weekly', displayName: 'Weekly limit', window: 'weekly', remainingFraction: 1, resetTime: future(6) }] }] };
const codex = { plan_type: 'pro', credits: { balance: '62500.0000000000', unlimited: false }, rate_limit: { allowed: true, secondary_window: { used_percent: 48, limit_window_seconds: 604800, reset_at: Math.floor((now + 5 * 86400000) / 1000) } } };
const resets = { available_count: 2, credits: [1, 10].map((days, index) => ({ id: `fixture-${index}`, status: 'available', reset_type: 'codex_rate_limits', granted_at: new Date(now - 86400000).toISOString(), expires_at: future(days) })) };
const grok = { currentPeriod: { type: 'USAGE_PERIOD_TYPE_WEEKLY', start: new Date(now - 86400000).toISOString(), end: future(6) }, billingPeriodEnd: future(6), creditUsagePercent: 6, monthlyLimit: { val: 0 }, used: { val: 0 }, onDemandCap: { val: 0 }, productUsage: [{ product: 'GrokBot', usagePercent: 5 }, { product: 'GrokBuild', usagePercent: 1 }] };
const meta = { subs_tier_name: 'Muse Code Max Usage', is_subs_active: true, subs_usage: { window: { used_percent: 16, window_duration_mins: 300, resets_at: Math.floor((now + 2 * 3600000) / 1000) }, weekly: { used_percent: 57, resets_at: Math.floor((now + 5 * 86400000) / 1000) } } };
const history = (provider, windowId, hours, remaining, rate, resetDays) => Array.from({length: hours * 12 + 1}, (_, i) => ({
  credential: `fixture-${provider}`, provider, windowId, label: 'Weekly',
  observedAtMs: now - (hours - i / 12) * 3600000,
  remaining: remaining + (hours - i / 12) * rate,
  resetAtMs: now + resetDays * 86400000, periodHours: 168,
}));
const observations = [
  ...history('codex', 'weekly', 6, 52, 2, 5),
  ...history('claude', 'seven-day', 24, 84, 0.2, 2),
  ...history('xai', 'weekly', 24, 94, 0.1, 6).map(s => ({...s, remaining: s.observedAtMs >= now - 6 * 3600000 ? 94 : s.remaining})),
].sort((a, b) => a.observedAtMs - b.observedAtMs);
const browser = await chromium.launch({headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE});
const receipts = [];
try {
  for (const [version, htmlPath, width] of [
    ['desktop', `${build}/dist/index.html`, 1920],
    ['mobile', `${build}/dist/index.html`, 390],
  ]) {
    const context = await browser.newContext({viewport: {width, height: 1100}, reducedMotion: 'reduce', colorScheme: 'dark', timezoneId: 'America/Sao_Paulo'});
    const page = await context.newPage();
    await page.clock.setFixedTime(new Date(now));
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.addInitScript(() => localStorage.setItem('cli-proxy-language', JSON.stringify({state: {language: 'en'}, version: 0})));
    const html = await readFile(htmlPath, 'utf8');
    await page.route('**/*', async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const path = url.pathname;
      const json = (data) => route.fulfill({json: data, headers: {'x-cli-proxy-api-version': 'v8.0.0'}});
      if (path === '/management.html') return route.fulfill({contentType: 'text/html', body: html});
      if (path.endsWith('/requests/api-call')) {
        const upstream = request.postDataJSON().url;
        let body;
        if (upstream.includes('anthropic.com') && upstream.includes('/usage')) body = claude;
        else if (upstream.includes('anthropic.com') && upstream.includes('/profile')) body = { account: { has_claude_max: true } };
        else if (upstream.includes('retrieveUserQuotaSummary')) body = antigravity;
        else if (upstream.includes('loadCodeAssist')) body = { paidTier: { id: 'g1-ultra-tier', name: 'Ultra' } };
        else if (upstream.includes('wham/usage')) body = codex;
        else if (upstream.includes('rate-limit-reset-credits')) body = resets;
        else if (upstream.includes('grok.com/v1/billing')) body = { config: grok };
        else if (upstream.includes('grok.com/v1/user')) body = { subscriptionTier: 'SuperGrokPro' };
        else if (upstream.includes('grok.com/v1/settings')) body = { subscription_tier_display: 'SuperGrok Heavy' };
        else if (upstream.includes('api.meta.ai/muse-code/key')) body = meta;
        else throw new Error(`Unexpected upstream fixture: ${upstream}`);
        return json({status_code: 200, header: {}, body});
      }
      if (path.endsWith('/credentials')) return json({files});
      if (path.endsWith('/credentials/download')) return json({dca_token: 'dca:synthetic-fixture'});
      if (path.endsWith('/quota-history/poll')) return json({claimed: false});
      if (path.endsWith('/quota-history')) return json({observations});
      if (path.startsWith('/v8/management')) return json({});
      return route.abort();
    });
    await page.goto(`${origin}/management.html#/login`);
    await page.getByPlaceholder('Enter the management key').fill('synthetic-fixture-only');
    await page.getByRole('button', {name: 'Login', exact: true}).click();
    await page.locator('a[href="#/quota"]').first().waitFor({state: 'attached'});
    await page.evaluate(() => { window.location.hash = '/quota'; });
    await page.locator('article').first().waitFor();
    await page.getByRole('button', {name: /Refresh all credentials/}).click();
    await page.getByRole('button', {name: 'Refresh quota', exact: true}).nth(4).waitFor();
    await page.getByText('Ultra', {exact: true}).first().waitFor();
    await page.getByText('SuperGrok Heavy', {exact: true}).first().waitFor();
    await page.getByText('Quota windows', {exact: true}).waitFor();
    await page.getByText('Manual reset expiry (GMT-3)', {exact: true}).waitFor();
    const cards = page.locator('article[class*="_card_"]');
    assert.equal(await cards.count(), 5);
    const cardMetrics = await cards.evaluateAll((nodes) => nodes.map((card) => ({ text: card.innerText, height: Math.round(card.getBoundingClientRect().height), bars: card.querySelectorAll('[class*="quotaBar_"]').length, overflow: card.scrollWidth > card.clientWidth })));
    const timeline = page.locator('section').filter({has: page.getByText('Quota windows', {exact: true})}).last();
    const summary = timeline.locator('[data-projection-status="projected"]');
    await summary.waitFor();
    assert((await summary.innerText()).includes('Provisional'));
    assert.equal(await timeline.locator('[data-projection-status="survives-reset"]').count(), 1);
    assert.equal(await timeline.locator('[data-projection-status="unstable"]').count(), 1);
    assert.equal(await timeline.locator('[data-band]').count(), 1, 'Only exhaustion before reset has a marker');
    assert.equal(await timeline.locator('[data-band][data-provisional]').count(), 1);
    await summary.locator('summary').focus();
    await page.keyboard.press('Enter');
    assert.equal(await summary.getAttribute('open'), '');
    assert((await summary.innerText()).includes('25% coverage'));
    const expanded = await summary.boundingBox();
    const nextLane = await summary.locator('xpath=ancestor::div[contains(@class,"laneHead")]/parent::div/following-sibling::div[1]').boundingBox();
    if (nextLane) assert(expanded.y + expanded.height <= nextLane.y, 'Expanded explanation must not overlap the next provider');
    await summary.locator('summary').click();
    assert((await timeline.locator('[role="img"]').count()) > 0, 'Timeline must retain its quota bars');
    const windowMetrics = [];
    for (const mode of ['Weekly', '5-hour']) {
      await timeline.getByRole('button', {name: mode, exact: true}).click();
      const windows = await timeline.locator('[class*="_window_"]').evaluateAll((nodes) => nodes.map((bar) => ({
        text: bar.innerText.trim(), title: bar.title, width: bar.getBoundingClientRect().width,
        fill: bar.querySelector('[class*="_windowFill_"]')?.style.width ?? null,
      })));
      assert(windows.length > 0, `${mode} must retain its quota windows`);
      assert(windows.every((bar) => bar.text === ''), `${mode} bars must have no visible percentage or reset label`);
      assert(windows.every((bar) => bar.title && bar.width > 0), `${mode} bars must retain tooltips and width`);
      assert(windows.some((bar) => bar.fill !== null), `${mode} must retain the current window fill`);
      windowMetrics.push({mode, windows});
    }
    await timeline.getByRole('button', {name: 'Weekly', exact: true}).click();
    {
      assert(cardMetrics.every((card) => card.bars === 0), 'Summary cards must have no quota bars');
      assert(cardMetrics.every((card) => !card.overflow), 'Cards must fit the viewport');
      assert(!cardMetrics[0].text.includes('Extra Usage'));
      assert(!cardMetrics[1].text.includes('GEMINI MODELS'));
      assert(!cardMetrics[2].text.includes('62500.000'));
      assert(cardMetrics[2].text.includes('62500'));
      const agyPlan = cards.nth(1).getByText('Ultra', {exact: true});
      assert((await agyPlan.getAttribute('class')).includes('codexPlanValue'));
      const codexPlan = cards.nth(2).getByText('Pro 200', {exact: true});
      assert((await codexPlan.getAttribute('class')).includes('codexPlanValue'));
      const grokPlan = cards.nth(3).getByText('SuperGrok Heavy', {exact: true});
      assert((await grokPlan.getAttribute('class')).includes('codexPlanValue'));
      const breakdown = cards.nth(3).getByText('Usage breakdown (shared quota)', {exact: true});
      const titleX = (await breakdown.boundingBox()).x;
      const label = cards.nth(3).getByText('GrokBot usage', {exact: true});
      assert(Math.abs(titleX - (await label.boundingBox()).x) <= 1, 'Breakdown rows must align left with the title');
      const titleWeight = await breakdown.evaluate((el) => getComputedStyle(el).fontWeight);
      const labelWeight = await label.evaluate((el) => getComputedStyle(el).fontWeight);
      assert(Number(titleWeight) > Number(labelWeight), 'Breakdown title must have stronger weight');
      await page.screenshot({path: `${out}/${version}.png`, fullPage: true});
    }
    receipts.push({version, width, cards: cardMetrics, windowMetrics, timelineBars: await timeline.locator('[role="img"]').count(), timelineText: await timeline.innerText(), errors});
    assert.deepEqual(errors, []);
    await context.close();
  }
  await writeFile(`${out}/ui-receipt.json`, JSON.stringify(receipts, null, 2));
  console.log(JSON.stringify(receipts.map(({version, width, cards, timelineBars, errors}) => ({version, width, heights: cards.map((c) => c.height), bars: cards.map((c) => c.bars), timelineBars, errors}))));
} finally {
  await browser.close();
}
