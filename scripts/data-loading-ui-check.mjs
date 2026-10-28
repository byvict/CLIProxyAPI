import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [build, out] = process.argv.slice(2);
if (!build || !out) throw new Error('Usage: data-loading-ui-check.mjs <build> <evidence>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
const html = await readFile(`${build}/dist/index.html`, 'utf8');
const usage = JSON.parse(await readFile(`${build}/tests/fixtures/claude-oauth-usage.json`, 'utf8'));
await mkdir(out, { recursive: true });
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
const context = await browser.newContext({ timezoneId: 'America/Sao_Paulo', reducedMotion: 'reduce' });
const page = await context.newPage();
const cancellations = [];
const errors = [], checks = [], counts = new Map(), records = [];
const now = Date.parse('2026-10-28T18:00:00Z');
await page.clock.setFixedTime(new Date(now));
const activity = { year: 2026, today: '2026-10-28', generatedAt: new Date(now).toISOString(), timezone: 'America/Sao_Paulo', availableFrom: '2026-10-28', providers: ['claude', 'codex', 'grok', 'meta'].map(id => ({ id, days: [{ date: '2026-10-28', agents: 5, tokens: 500, reportedAgents: 5, partialAgents: 0, longestDurationMs: 3000 }] })) };
let githubLogin = 'old-account', failActivity = false, holdGitHub = false, holdQuota = false, holdProfile = true, holdHistory = false, historyVersion = 0;
let releaseGitHub, releaseQuota, releaseProfile, releaseHistory, releaseTimeline, releaseCodexAux;
let holdTimeline = true, holdCodexAux = true;
const files = ['fast', 'slow'].map(name => ({ name: `claude-${name}.json`, type: 'claude', auth_index: name, status: 'active' }));
const count = name => counts.get(name) || 0;
const bump = name => counts.set(name, count(name) + 1);
files.push({ name: 'codex-fixture.json', type: 'codex', auth_index: 'codex', status: 'active', metadata: { chatgpt_account_id: 'fixture-account' } });
page.on('requestfailed', request => { if (request.url().includes('/quota-history/snapshot')) cancellations.push(request.failure()?.errorText); });
page.on('pageerror', error => errors.push(error.message));
await page.addInitScript(() => localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'en' }, version: 0 })));
await page.route('**/*', async route => {
  const request = route.request(), url = new URL(request.url()), path = url.pathname;
  const json = body => route.fulfill({ json: body, headers: { 'x-cli-proxy-api-version': 'v8.0.0' } });
  if (path === '/management.html') return route.fulfill({ contentType: 'text/html', body: html });
  if (path.endsWith('/performance') || path.endsWith('/delegation')) {
    bump(path.split('/').pop());
    if (failActivity) return route.fulfill({ status: 503, json: { error: 'Synthetic refresh failure' } });
    return json(path.endsWith('/performance') ? { status: 'ready', activity } : { status: 'unavailable' });
  }
  if (path.endsWith('/infra/github')) {
    if (request.method() === 'POST') { githubLogin = 'new-account'; return json({ status: 'ok' }); }
    bump('github');
    const login = githubLogin;
    if (holdGitHub) { holdGitHub = false; await new Promise(done => { releaseGitHub = done; }); }
    return json({ status: 'connected', source: 'configuration', activity: { login, year: 2026, contributions: 1, pullRequests: 1, reviews: 0, commits: 1, days: [] } });
  }
  if (path.endsWith('/credentials')) return json({ files });
  if (path.endsWith('/quota-history/poll')) return json({ claimed: true });
  if (path.endsWith('/requests/api-call')) {
    const { url: upstream, authIndex } = request.postDataJSON();
    if (upstream.endsWith('/usage')) {
      bump(authIndex);
      if (holdQuota && authIndex === 'slow') await new Promise(done => { releaseQuota = done; });
      return json({ status_code: 200, body: authIndex === 'codex' ? { plan_type: 'pro', rate_limit: { secondary_window: { used_percent: 48, limit_window_seconds: 604800, reset_at: (now + 86400000) / 1000 } } } : usage });
    }
    if (upstream.includes('rate-limit-reset-credits') || upstream.includes('/subscription')) {
      bump('codex-aux');
      if (holdCodexAux) await new Promise(done => { const previous = releaseCodexAux; releaseCodexAux = () => { previous?.(); done(); }; });
      return json({ status_code: 200, body: upstream.includes('subscription') ? { active_until: new Date(now + 86400000).toISOString() } : { available_count: 2, credits: [] } });
    }
    if (upstream.endsWith('/profile')) {
      bump('profile');
      if (holdProfile) await new Promise(done => { const previous = releaseProfile; releaseProfile = () => { previous?.(); done(); }; });
      return json({ status_code: 200, body: { account: { has_claude_max: true } } });
    }
    return json({ status_code: 200, body: {} });
  }
  if (path.endsWith('/quota-history/snapshot')) {
    bump('snapshot');
    if (holdHistory) { holdHistory = false; await new Promise(done => { releaseHistory = done; }); }
    return json({ generatedAt: new Date(now + historyVersion * 1000).toISOString(), accounts: [], providers: [{ provider: 'claude', accountCount: 0 }], sources: [] });
  }
  if (path.endsWith('/quota-history/view')) { bump('view'); return json({ generatedAt: new Date(now).toISOString(), groups: [], selectionRequired: '' }); }
  if (path.endsWith('/quota-history')) {
    if (request.method() === 'POST') records.push(request.postDataJSON().authIndex);
    else { bump('timeline'); if (holdTimeline) { holdTimeline = false; await new Promise(done => { releaseTimeline = done; }); } }
    return json({ observations: [] });
  }
  if (path.startsWith('/v8/management')) return json({});
  return route.abort();
});
const until = async (predicate, message) => {
  const end = Date.now() + 10000;
  while (!predicate() && Date.now() < end) await new Promise(done => setTimeout(done, 20));
  assert(predicate(), message);
};
const navigate = async path => { await page.locator(`a[href="#${path}"]`).first().click(); await page.locator('.page-transition--animating').waitFor({ state: 'hidden' }); };
const refresh = () => page.getByRole('button', { name: 'Atualizar', exact: true });
try {
  holdQuota = true;
  await page.goto('http://data-loading.invalid/management.html#/login');
  await page.getByPlaceholder('Enter the management key').fill('synthetic-only');
  await page.getByRole('button', { name: 'Login', exact: true }).click();
  await until(() => count('slow') === 1, 'collector slow account started');
  await navigate('/quota');
  await page.locator('article').filter({ hasText: 'claude-fast' }).getByRole('button', { name: 'Refresh quota', exact: true }).waitFor();
  await page.locator('article').filter({ hasText: 'codex-fixture' }).getByRole('button', { name: 'Refresh quota', exact: true }).waitFor();
  assert(count('codex-aux') >= 2, 'Codex auxiliaries remain pending after balance publication');
  assert.equal(count('profile'), 1, 'Fast balance commits before its profile and sibling');
  await page.getByRole('button', { name: /Refresh all credentials/ }).click();
  await until(() => count('fast') === 2, 'batch read started');
  assert.equal(count('slow'), 1, 'batch joins automatic read');
  releaseQuota(); holdQuota = false;
  await until(() => records.filter(id => id === 'slow').length === 1, 'one persisted observation for joined read');
  await until(() => Boolean(releaseTimeline), 'timeline read held during writes');
  releaseTimeline();
  await until(() => count('timeline') >= 2, 'timeline rereads after writes during its first read');
  holdCodexAux = false; releaseCodexAux?.();
  holdProfile = false; releaseProfile?.();
  await page.getByText('Max', { exact: true }).first().waitFor();
  checks.push('Accounts publish before siblings, Claude profile and Codex auxiliaries; timeline performs a trailing read; automatic/manual overlap shares one usage and one persisted observation.');

  await navigate('/performance');
  await page.getByRole('img', { name: 'Calendário de tokens em 2026' }).waitFor();
  assert.equal(count('github'), 0);
  await navigate('/infra'); await page.getByText('@old-account', { exact: true }).waitFor();
  await navigate('/performance');
  assert.equal(count('performance'), 1, 'navigation reuses fresh data');
  await page.clock.setFixedTime(new Date(now + 61_000));
  await navigate('/infra'); await navigate('/performance');
  await until(() => count('performance') === 2, 'navigation revalidates after 60 seconds');
  failActivity = true; await refresh().click();
  await page.getByRole('alert').filter({ hasText: 'Synthetic refresh failure' }).waitFor();
  await page.getByRole('img', { name: 'Calendário de tokens em 2026' }).waitFor();
  failActivity = false;
  checks.push('Performance is cached for 60 seconds, revalidates on navigation, preserves ready charts after error, and loads no GitHub initially.');

  await navigate('/delegation'); await until(() => count('delegation') === 1, 'delegation loaded');
  await navigate('/performance'); await navigate('/delegation');
  assert.equal(count('delegation'), 1);
  failActivity = true; await refresh().click();
  await page.getByRole('alert').filter({ hasText: 'Synthetic refresh failure' }).waitFor();
  failActivity = false;
  checks.push('Delegation shares navigation caching and retains its last successful response after refresh error.');

  await navigate('/infra');
  holdGitHub = true;
  await page.getByRole('button', { name: 'Refresh', exact: true }).last().click();
  await until(() => Boolean(releaseGitHub), 'old GitHub GET pending');
  await page.getByLabel('Personal access token', { exact: true }).fill('synthetic-token');
  await page.getByRole('button', { name: 'Validar e atualizar', exact: true }).click();
  await page.getByText('@new-account', { exact: true }).waitFor();
  releaseGitHub();
  await navigate('/performance'); await page.getByRole('button', { name: 'GitHub', exact: true }).click();
  await page.getByText(/@new-account/).waitFor();
  assert.equal(await page.getByText(/@old-account/).count(), 0);
  checks.push('A pending GitHub GET cannot overwrite the account returned after saving a token.');

  holdHistory = true; await navigate('/quota-history');
  await until(() => Boolean(releaseHistory), 'initial snapshot pending');
  await page.evaluate(() => { window.dispatchEvent(new Event('quota-history-updated')); window.dispatchEvent(new Event('quota-history-updated')); });
  historyVersion++; releaseHistory();
  await until(() => count('snapshot') >= 2 && count('view') >= 2, 'trailing snapshot/view reread');
  const snapshots = count('snapshot');
  await page.getByRole('button', { name: /Claude 0/ }).click();
  await page.locator('select[data-select="period"]').waitFor();
  await page.locator('#cp-monitor[aria-busy="false"]').waitFor();
  holdHistory = true; releaseHistory = undefined;
  await page.locator('select[data-select="period"]').selectOption('7d');
  await until(() => Boolean(releaseHistory), 'obsolete selection pending');
  await page.locator('select[data-select="period"]').selectOption('30d');
  await until(() => count('snapshot') >= snapshots + 3, 'replacement selection requested');
  releaseHistory();
  await until(() => cancellations.length > 0, 'obsolete selection cancels Axios transport');
  assert.equal(await page.locator('.toast:visible').count(), 0, 'canceled selection is not a user error');
  await navigate('/quota');
  await page.getByTitle('Logout', { exact: true }).click();
  await page.getByPlaceholder('Enter the management key').fill('synthetic-only');
  await page.getByRole('button', { name: 'Login', exact: true }).click();
  await navigate('/performance');
  await until(() => count('performance') >= 4, 'reconnect invalidates cached activity');
  checks.push('History events during a read cause a final read; selection cancellation shows no toast; reconnect invalidates activity.');
  assert.deepEqual(errors, []);
  await page.screenshot({ path: `${out}/final.png`, fullPage: true });
  await writeFile(`${out}/receipt.json`, JSON.stringify({ checks, errors, counts: Object.fromEntries(counts), records, cancellations }, null, 2));
  console.log(JSON.stringify({ passed: checks.length, errors }));
} catch (error) {
  await page.screenshot({ path: `${out}/failure.png`, fullPage: true });
  await writeFile(`${out}/failure.txt`, await page.locator('body').innerText());
  throw error;
} finally { await browser.close(); }
