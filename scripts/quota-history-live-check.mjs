import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';

const [origin, keyFile, evidenceRoot] = process.argv.slice(2);
if (!origin || !keyFile || !evidenceRoot) throw new Error('Usage: quota-history-live-check.mjs <origin> <management-key-file> <evidence-root>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
const key = (await readFile(keyFile, 'utf8')).trim();
await mkdir(evidenceRoot, { recursive: true, mode: 0o700 });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1600, height: 1100 }, timezoneId: 'America/Sao_Paulo', reducedMotion: 'reduce' });
const page = await context.newPage();
const errors = [];
page.on('pageerror', (error) => errors.push(error.message));
try {
  const response = await context.request.get(`${origin}/v8/management/credentials`, { headers: { Authorization: `Bearer ${key}` } });
  assert.equal(response.status(), 200);
  const credential = (await response.json()).files.find((file) => (file.type === 'claude' || file.provider === 'claude') && !file.disabled);
  assert(credential, 'A registered Claude credential is required for this check');
  const authIndex = String(credential.auth_index ?? credential.authIndex);
  await page.addInitScript(() => {
    localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'en' }, version: 0 }));
    localStorage.setItem('cli-proxy-theme', JSON.stringify({ state: { theme: 'dark' }, version: 0 }));
  });
  // Keep the live check to an explicit quota read for the selected account.
  await page.route('**/quota-history/poll', (route) => route.fulfill({ json: { claimed: false } }));
  await page.route('**/v8/management/requests/api-call', (route) => {
    const request = route.request().postDataJSON();
    assert.equal(String(request.authIndex), authIndex);
    assert.equal(request.method, 'GET', 'Live verification must not perform inference or quota redemption');
    return route.continue();
  });
  await page.goto(`${origin}/management.html#/login`);
  await page.getByPlaceholder('Enter the management key').fill(key);
  await page.getByRole('button', { name: 'Login', exact: true }).click();
  await page.locator('a[href="#/quota"]').first().click();
  await page.getByRole('button', { name: /^Claude/ }).first().click();
  const card = page.locator('article').filter({ hasText: credential.name });
  await card.waitFor();
  const recorded = page.waitForResponse((result) => result.url().endsWith('/v8/management/quota-history') && result.request().method() === 'POST');
  await card.getByRole('button').first().click();
  assert.equal((await recorded).status(), 204, 'A real manual quota read must persist its observation');
  await page.locator('[data-band]').first().waitFor();
  const markers = await page.locator('[data-band]').evaluateAll((nodes) => nodes.map((node) => ({ band: node.dataset.band, coverage: Number(node.dataset.coverage), width: getComputedStyle(node).width, label: node.getAttribute('aria-label') })));
  assert(markers.length > 0 && markers.every((marker) => marker.width === '3px' && marker.coverage > 0));
  assert.equal(await page.getByRole('img', { name: 'Current time', exact: true }).count(), 1);
  await page.waitForFunction(() => !document.querySelector('.page-transition--animating'));
  const close = page.getByRole('button', { name: 'Close', exact: true });
  if (await close.count()) await close.first().click();
  await page.screenshot({ path: resolve(evidenceRoot, 'real-quota-management.png'), fullPage: true });
  await page.locator('a[href="#/quota-history"]').first().click();
  await page.getByRole('heading', { name: 'Quota History', exact: true }).waitFor();
  await page.locator('[data-filter="claude"]').first().click();
  await page.locator('[data-chart="rate"] [data-hit]').waitFor();
  await page.locator('[data-chart="rate"] [data-hit]').focus();
  await page.keyboard.press('End');
  assert(Number(await page.locator('[data-chart="rate"] [data-hit]').getAttribute('aria-valuenow')) > 0);
  await page.locator('.heatmap .heat-cell').last().scrollIntoViewIfNeeded();
  const last = await page.locator('.heatmap .heat-cell').last().boundingBox();
  assert(last && last.y >= 0 && last.y + last.height <= 1100);
  await page.locator('.content').evaluate((element) => element.scrollTo({ top: 0, behavior: 'instant' }));
  await page.setViewportSize({ width: 1600, height: 2400 });
  await page.locator('#screen').screenshot({ path: resolve(evidenceRoot, 'real-quota-history.png') });
  assert.deepEqual(errors, []);
  const receipt = { status: 'passed', manualReadPersisted: true, realMarkers: markers, v1HistoryLoaded: true, errors };
  await writeFile(resolve(evidenceRoot, 'receipt.json'), JSON.stringify(receipt, null, 2), { mode: 0o600 });
  process.stdout.write(JSON.stringify(receipt) + '\n');
} finally {
  await context.close();
  await browser.close();
}
