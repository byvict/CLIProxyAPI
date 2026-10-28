import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [build, out] = process.argv.slice(2);
if (!build || !out) throw new Error('Usage: quota-auto-refresh-ui-check.mjs <build> <evidence>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
const html = await readFile(`${build}/dist/index.html`, 'utf8');
const usage = JSON.parse(await readFile(`${build}/tests/fixtures/claude-oauth-usage.json`, 'utf8'));
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
await mkdir(out, { recursive: true });
const context = await browser.newContext({ timezoneId: 'America/Sao_Paulo', reducedMotion: 'reduce' });
const page = await context.newPage();
const errors = [];
const reads = [];
const records = [];
let pendingRead;
let holdReads = false;
let failRead = false;
const start = Date.now();
const interval = 5 * 60_000;
const nextPoll = new Map([['fixture-claude', start + interval], ['fixture-meta', start + 30 * 60_000]]);
const files = ['claude', 'meta'].map((type) => ({ name: `${type}-fixture.json`, type, auth_index: `fixture-${type}`, status: 'active' }));
page.on('pageerror', (error) => errors.push(error.message));
await page.clock.install({ time: new Date(start) });
await page.addInitScript(() => {
  localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'en' }, version: 0 }));
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => window.fixtureVisibility || 'visible' });
});
await page.route('**/*', async (route) => {
  const request = route.request();
  const path = new URL(request.url()).pathname;
  const json = (body) => route.fulfill({ json: body, headers: { 'x-cli-proxy-api-version': 'v8.0.0' } });
  if (path === '/management.html') return route.fulfill({ contentType: 'text/html', body: html });
  if (path.endsWith('/credentials')) return json({ files });
  if (path.endsWith('/credentials/download')) return json({ dca_token: 'dca:synthetic-fixture' });
  if (path.endsWith('/quota-history/poll')) {
    const now = await page.evaluate(() => Date.now());
    const { authIndex } = request.postDataJSON();
    const claimed = now >= (nextPoll.get(authIndex) || 0);
    if (claimed) nextPoll.set(authIndex, now + (authIndex === 'fixture-meta' ? 30 * 60_000 : interval));
    return json({ claimed });
  }
  if (path.endsWith('/requests/api-call')) {
    const { url } = request.postDataJSON();
    if (url.includes('/profile')) return json({ status_code: 200, body: { account: { has_claude_max: true } } });
    if (url !== 'https://api.anthropic.com/api/oauth/usage' && !url.includes('muse-code/key')) return json({ status_code: 200, body: {} });
    const provider = url.includes('anthropic.com') ? 'claude' : 'meta';
    reads.push(provider);
    if (holdReads) await new Promise((resolve) => { pendingRead = resolve; });
    if (failRead && provider === 'claude') return json({ status_code: 503, body: 'Fixture unavailable' });
    return json({ status_code: 200, body: provider === 'claude' ? usage : { subs_tier_name: 'Muse fixture', is_subs_active: true, subs_usage: { weekly: { used_percent: 30 } } } });
  }
  if (path.endsWith('/quota-history')) {
    if (request.method() === 'POST') records.push(request.postDataJSON().authIndex);
    return json({ observations: [] });
  }
  if (path.startsWith('/v8/management')) return json({});
  return route.abort();
});
const count = (provider) => reads.filter((value) => value === provider).length;
const waitForReads = async (provider, expected) => {
  const deadline = Date.now() + 10_000;
  while (count(provider) < expected && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(count(provider), expected, `${provider} refresh count`);
};
const waitForRecords = async (provider, expected) => {
  const countRecords = () => records.filter((value) => value === `fixture-${provider}`).length;
  const deadline = Date.now() + 10_000;
  while (countRecords() < expected && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(countRecords(), expected, `${provider} persisted reading count`);
};
const visibility = (value) => page.evaluate((state) => {
  window.fixtureVisibility = state;
  document.dispatchEvent(new Event('visibilitychange'));
}, value);
try {
  await page.goto('http://quota-fixture.invalid/management.html#/login');
  await page.getByPlaceholder('Enter the management key').fill('synthetic-fixture-only');
  await page.getByRole('checkbox', { name: 'Remember password' }).focus();
  await page.keyboard.press('Space');
  assert(await page.getByRole('checkbox', { name: 'Remember password' }).isChecked());
  await page.getByRole('button', { name: 'Login', exact: true }).click();
  await page.locator('a[href="#/quota"]').first().waitFor({ state: 'attached' });
  await page.evaluate(() => { window.location.hash = '/quota'; });
  await waitForReads('claude', 1);
  await waitForRecords('claude', 1);
  await page.getByRole('button', { name: 'Refresh quota', exact: true }).first().waitFor();
  assert.equal(count('meta'), 0, 'Muse retains its persisted 30-minute floor');

  await page.clock.runFor(interval - 1000);
  assert.equal(count('claude'), 1, 'No early refresh');
  await page.clock.runFor(2000);
  await waitForReads('claude', 2);
  await waitForRecords('claude', 2);
  await page.getByRole('button', { name: 'Refresh quota', exact: true }).first().waitFor();
  await visibility('hidden');
  await page.clock.runFor(interval + 1000);
  assert.equal(count('claude'), 2, 'Hidden panels must not poll');
  await visibility('visible');
  await waitForReads('claude', 3);
  await waitForRecords('claude', 3);

  holdReads = true;
  await page.clock.runFor(interval + 1000);
  await waitForReads('claude', 4);
  await page.evaluate(() => window.dispatchEvent(new Event('quota-history-refresh')));
  await page.clock.runFor(interval + 1000);
  assert.equal(count('claude'), 4, 'An unfinished batch must not overlap the next refresh');
  holdReads = false;
  pendingRead();
  await waitForRecords('claude', 4);

  failRead = true;
  await page.clock.runFor(interval + 1000);
  await waitForReads('claude', 5);
  await page.locator('article').first().getByRole('alert').waitFor();
  failRead = false;
  await page.clock.runFor(interval + 1000);
  await waitForReads('claude', 6);
  await waitForRecords('claude', 5);
  await waitForReads('meta', 1);
  await page.getByText('Muse fixture', { exact: true }).waitFor();
  await page.getByRole('button', { name: 'Refresh quota', exact: true }).first().waitFor();
  await page.screenshot({ path: `${out}/automatic-quotas.png`, fullPage: true });

  await page.reload();
  await waitForReads('claude', 7);
  await waitForRecords('claude', 6);
  await page.getByRole('button', { name: 'Refresh quota', exact: true }).first().waitFor();
  assert.equal(count('meta'), 1, 'Reopening must retain the Muse floor');
  await page.getByTitle('Logout', { exact: true }).click();
  await page.getByPlaceholder('Enter the management key').waitFor();
  await page.clock.runFor(interval + 1000);
  assert.equal(count('claude'), 7, 'Logout must stop automatic collection');
  assert.deepEqual(errors, []);
  const receipt = { checks: ['cold start with an existing reservation', 'five-minute refresh', 'hidden pause and visible resume', 'no overlapping batches', 'error recovery', 'Muse thirty-minute floor', 'reload', 'logout cleanup'], reads, errors };
  await writeFile(`${out}/receipt.json`, JSON.stringify(receipt, null, 2) + '\n');
  console.log(JSON.stringify(receipt));
} finally {
  pendingRead?.();
  await context.close();
  await browser.close();
}
