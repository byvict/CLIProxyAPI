import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { basename, dirname, join, isAbsolute } from 'node:path';
import { pathToFileURL } from 'node:url';

const [baseURL, keyFile, evidenceDir] = process.argv.slice(2);
assert(baseURL && keyFile && isAbsolute(evidenceDir), 'Provide fixture URL, key file and absolute evidence directory.');
assert.equal(basename(dirname(keyFile)), 'fixture');
assert(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(baseURL).hostname));
const moduleName = process.env.SUBMON_PLAYWRIGHT_MODULE || 'playwright';
const { chromium } = await import(isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
await mkdir(evidenceDir, { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
const page = await context.newPage();
const errors = [];
const checks = [];
let snapshotRequests = 0;
page.on('pageerror', error => errors.push(error.message));
page.on('request', request => { if (new URL(request.url()).pathname === '/api/v1/snapshot') snapshotRequests++; });
await page.route('**/*', route => new URL(route.request().url()).origin === new URL(baseURL).origin ? route.continue() : route.abort());
await page.clock.install();
const wait = () => page.locator('#cp-monitor[aria-busy="false"]').waitFor();
const outsideFocus = () => page.locator('[data-nav="overview"]').focus();
async function login() {
  await page.getByLabel('Chave do monitor', { exact: true }).fill((await readFile(keyFile, 'utf8')).trim());
  await page.getByRole('button', { name: 'Entrar', exact: true }).click();
  await page.locator('.provider-card').first().waitFor();
  await wait();
}
let heldView = null;
function holdView(fail = false) {
  let arrived, release;
  const ready = new Promise(resolve => { arrived = resolve; });
  const released = new Promise(resolve => { release = resolve; });
  heldView = { arrived, released, fail };
  return { ready, release };
}
await page.route('**/api/v1/view?*', async route => {
  const held = heldView;
  if (!held) return route.continue();
  heldView = null;
  held.arrived();
  await held.released;
  if (held.fail) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ message: 'Resposta atrasada indisponível.' }) });
  return route.continue();
});
try {
  await page.goto(baseURL);
  await login();
  await page.locator('.provider-tabs [data-filter="claude"]').click();
  await wait();

  const delayed = holdView();
  await outsideFocus();
  await page.clock.runFor(30001);
  await delayed.ready;
  const chart = await page.locator('[data-hit]').first().elementHandle();
  await chart.focus();
  delayed.release();
  await wait();
  assert.equal(await chart.evaluate(element => element.isConnected && document.activeElement === element), true, 'pending automatic success destroyed new focus');
  assert.equal(await page.locator('#refresh-status').isVisible(), true);
  checks.push('focus-acquired-during-pending-success');

  const before = snapshotRequests;
  for (let i = 0; i < 5; i++) { await page.clock.runFor(30001); await wait(); }
  assert.ok(snapshotRequests >= before + 5, 'interaction stopped fetching fresh state');
  assert.equal(await chart.evaluate(element => element.isConnected), true);
  checks.push('focused-screen-continues-fetching-with-visible-pause');

  await outsideFocus();
  await page.clock.runFor(1);
  assert.equal(await page.locator('#refresh-status').isVisible(), false, 'deferred data was not applied after interaction');
  const failure = holdView(true);
  await page.clock.runFor(30001);
  await failure.ready;
  const errorChart = await page.locator('[data-hit]').first().elementHandle();
  await errorChart.focus();
  failure.release();
  await wait();
  assert.equal(await errorChart.evaluate(element => element.isConnected && document.activeElement === element), true, 'pending automatic failure destroyed new focus');
  assert.match(await page.locator('#refresh-status').innerText(), /indisponível/);
  checks.push('focus-acquired-during-pending-failure');

  await outsideFocus();
  await page.clock.runFor(1);
  const settings = holdView();
  await page.clock.runFor(30001);
  await settings.ready;
  await page.locator('.forecast-settings summary').first().click();
  settings.release();
  await wait();
  assert.equal(await page.locator('.forecast-settings[open]').count(), 1);
  assert.equal(await page.locator('#refresh-status').isVisible(), true);
  await page.getByRole('button', { name: 'Aplicar dados', exact: true }).click();
  assert.equal(await page.locator('#refresh-status').isVisible(), false);
  checks.push('settings-opened-during-pending-response');

  let oldUnauthorized = true, arrived, release;
  const ready = new Promise(resolve => { arrived = resolve; });
  const released = new Promise(resolve => { release = resolve; });
  await page.route('**/api/v1/snapshot', async route => {
    if (!oldUnauthorized) return route.continue();
    oldUnauthorized = false;
    const response = await route.fetch();
    assert.equal(response.status(), 401);
    arrived();
    await released;
    return route.fulfill({ response });
  });
  await context.clearCookies();
  await outsideFocus();
  await page.clock.runFor(30001);
  await ready;
  await page.clock.runFor(30001);
  await page.getByLabel('Chave do monitor', { exact: true }).waitFor();
  await login();
  const oldResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/snapshot' && response.status() === 401);
  release();
  await oldResponse;
  await page.clock.runFor(100);
  assert.equal(await page.getByLabel('Chave do monitor', { exact: true }).count(), 0, 'old 401 replaced a renewed authenticated session');
  assert.equal(await page.evaluate(async () => (await fetch('/api/v1/snapshot')).status), 200);
  checks.push('superseded-401-after-reauthentication');
  assert.deepEqual(errors, []);
  await page.screenshot({ path: join(evidenceDir, 'delayed-response-final.png'), fullPage: true });
  await writeFile(join(evidenceDir, 'delayed-ui-receipt.json'), JSON.stringify({ checks, snapshotRequests, javascriptErrors: errors }, null, 2));
  console.log(JSON.stringify({ passed: checks.length, evidenceDir }));
} finally {
  await context.close();
  await browser.close();
}
