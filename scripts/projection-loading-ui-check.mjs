import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [build, out] = process.argv.slice(2);
if (!build || !out) throw new Error('Usage: projection-loading-ui-check.mjs <build> <evidence>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
const html = await readFile(`${build}/dist/index.html`, 'utf8');
await mkdir(out, { recursive: true });
const now = Date.parse('2026-10-28T18:00:00Z');
const files = ['a', 'b', 'c', 'd', 'unknown', 'empty'].map(id => ({ name: `claude-${id}.json`, type: 'claude', auth_index: id, status: 'active' }));
const samples = ['a', 'b', 'c', 'd', 'unknown', 'deleted'].flatMap(credential => Array.from({ length: 49 }, (_, i) => ({
  credential, provider: 'claude', windowId: 'seven-day', label: 'Weekly', observedAtMs: now - (48 - i) * 300_000,
  remaining: 40 + (48 - i) / 6, resetAtMs: now + 86_400_000, periodHours: 168,
})));
samples.push({ ...samples[0], credential: 'unknown', observedAtMs: now, remaining: null, resetAtMs: null });
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
const context = await browser.newContext({ timezoneId: 'America/Sao_Paulo', reducedMotion: 'reduce', viewport: { width: 1600, height: 1100 } });
const page = await context.newPage();
const errors = [], checks = [], records = [], pending = [];
let failHistory = false, holdProviders = true, holdHistory = false, releaseHistory, emptyLive = false, historyReads = 0, session = 0;
page.on('pageerror', error => errors.push(error.message));
await page.clock.setFixedTime(new Date(now));
await page.addInitScript(() => localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'en' }, version: 0 })));
await page.route('**/*', async route => {
  const request = route.request(), path = new URL(request.url()).pathname;
  const json = body => route.fulfill({ json: body, headers: { 'x-cli-proxy-api-version': 'v8.0.0' } });
  if (path === '/management.html') return route.fulfill({ contentType: 'text/html', body: html });
  if (path.endsWith('/credentials')) return json({ files });
  if (path.endsWith('/quota-history/poll')) return json({ claimed: true });
  if (path.endsWith('/quota-history')) {
    if (request.method() === 'POST') { records.push(request.postDataJSON()); return route.fulfill({ status: 204 }); }
    historyReads++;
    if (failHistory) return route.fulfill({ status: 503, json: { error: "Synthetic history read failure" } });
    const data = session === 0 ? samples : [];
    if (holdHistory) await new Promise(done => { releaseHistory = done; });
    return json({ observations: data });
  }
  if (path.endsWith('/requests/api-call')) {
    if (holdProviders) await new Promise(done => pending.push(done));
    const upstream = request.postDataJSON().url;
    return json({ status_code: 200, body: upstream.endsWith('/usage')
      ? emptyLive ? { seven_day: { utilization: null, resets_at: null } } : { seven_day: { utilization: 12, resets_at: new Date(now + 2 * 86_400_000).toISOString() } }
      : { account: { has_claude_max: true } } });
  }
  if (path.startsWith('/v8/management')) return json({});
  return route.abort();
});
const lanes = () => page.locator('[class*="_laneHead_"]');
const until = async (predicate, message) => {
  const end = Date.now() + 10_000;
  while (!(await predicate()) && Date.now() < end) await new Promise(done => setTimeout(done, 20));
  assert(await predicate(), message);
};
const navigate = async path => { await page.locator(`a[href="#${path}"]`).first().click(); await page.locator('.page-transition--animating').waitFor({ state: 'hidden' }); };
const login = async () => { await page.getByPlaceholder('Enter the management key').fill('synthetic-only'); await page.getByRole('button', { name: 'Login', exact: true }).click(); await navigate('/quota'); };
try {
  await page.goto('http://projection-fixture.invalid/management.html#/login');
  await login();
  await until(() => historyReads > 0 && pending.length > 0, 'history and provider reads started');
  await until(async () => await lanes().count() === 4, 'four recorded lanes must render before any provider response');
  assert.equal(await page.locator('[data-projection-status="projected"]').count(), 4);
  assert.equal(records.length, 0, 'restored readings must not persist synthetic observations');
  assert.equal(await lanes().locator('time').count(), 4);
  assert.equal(await page.locator('[class*="_windowFill_"]').count(), 4);
  await page.screenshot({ path: `${out}/providers-held.png`, fullPage: true });
  await writeFile(`${out}/providers-held.txt`, await page.locator('body').innerText());
  checks.push('Cold production HTML renders four recorded lanes and provisional forecasts while all provider requests remain held; unknown/deleted/empty identities produce no lane and no writes.');
  await navigate('/performance'); holdHistory = true;
  await navigate('/quota');
  await until(() => Boolean(releaseHistory), 'remount refresh pending');
  assert.equal(await lanes().count(), 4, 'remount reuses session history before refresh');
  releaseHistory(); holdHistory = false;
  checks.push('Remount renders cached current-session history while its refresh is held.');
  failHistory = true;
  await page.evaluate(() => window.dispatchEvent(new Event('quota-history-updated')));
  await page.getByRole('alert').filter({ hasText: 'Synthetic history read failure' }).waitFor();
  assert.equal(await lanes().count(), 4, 'history read failure preserves disclosed last-known readings');
  await page.clock.setFixedTime(new Date(now + 46 * 60_000));
  await navigate('/performance'); await navigate('/quota');
  await until(async () => await page.getByText('Estimate paused · stale reading', { exact: true }).count() === 4, 'stale recorded forecasts remain paused');
  assert.equal(await lanes().locator('time').count(), 4);
  await page.clock.setFixedTime(new Date(now));
  failHistory = false;
  await page.evaluate(() => window.dispatchEvent(new Event('quota-history-updated')));
  checks.push('Failed history refresh preserves recorded timestamps and exposes an error; stale recorded readings pause forecasts.');
  holdProviders = false; pending.splice(0).forEach(done => done());
  await until(async () => await page.getByRole('button', { name: 'Refresh quota', exact: true }).count() === 6, 'all live responses completed');
  await until(async () => await lanes().count() === 6 && await lanes().locator('time').count() === 0, 'live values replace recorded lanes');
  assert((await lanes().first().innerText()).includes('88%'));
  emptyLive = true;
  await page.getByRole('button', { name: /Refresh all credentials/ }).click();
  await until(async () => await lanes().count() === 0, 'successful response without windows must not restore history');
  checks.push('Fresh live balance/reset replaces recorded values; explicit successful unknown-window response suppresses all old windows.');
  holdProviders = true; holdHistory = true; releaseHistory = undefined; session++;
  await page.getByTitle('Logout', { exact: true }).click();
  await login();
  await until(() => Boolean(releaseHistory), 'new-session history held');
  assert.equal(await lanes().count(), 0, 'new session cannot flash old history');
  releaseHistory(); holdHistory = false;
  assert.equal(await lanes().count(), 0);
  checks.push('Logout and relogin invalidate history synchronously; empty new-session history cannot reveal old lanes.');
  await page.getByTitle('Logout', { exact: true }).click();
  await page.getByText('Custom Connection URL:', { exact: true }).click();
  await page.getByPlaceholder('Eg: https://example.com:8317').fill('http://other-projection.invalid');
  releaseHistory = undefined; holdHistory = true;
  await login();
  await until(() => Boolean(releaseHistory), 'other connection history held');
  assert.equal(await lanes().count(), 0, 'connection switch cannot reveal previous history');
  releaseHistory(); holdHistory = false;
  checks.push('A different connection with the same credential names cannot reuse the old connection history.');
  assert.deepEqual(errors, []);
  await writeFile(`${out}/receipt.json`, JSON.stringify({ checks, errors, historyReads, persistedLiveReadings: records.length, heldProviders: pending.length }, null, 2));
  console.log(JSON.stringify({ passed: checks.length, errors }));
} catch (error) {
  await page.screenshot({ path: `${out}/failure.png`, fullPage: true });
  await writeFile(`${out}/failure.txt`, await page.locator('body').innerText());
  throw error;
} finally {
  holdProviders = false; pending.splice(0).forEach(done => done()); releaseHistory?.();
  await browser.close();
}
