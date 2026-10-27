import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';

const [origin, fixtureRoot, evidenceRoot] = process.argv.slice(2);
if (!origin || !fixtureRoot || !evidenceRoot) throw new Error('Usage: quota-history-ui-check.mjs <origin> <fixture-root> <evidence-root>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
const key = (await readFile(resolve(fixtureRoot, 'management.key'), 'utf8')).trim();
const fixtures = JSON.parse(await readFile(resolve(fixtureRoot, 'fixtures.json'), 'utf8'));
const referenceScript = await readFile(new URL('../management/v1-source/app.js', import.meta.url), 'utf8');
await mkdir(evidenceRoot, { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1600, height: 1100 }, timezoneId: 'America/Sao_Paulo', reducedMotion: 'reduce' });
const page = await context.newPage();
const errors = [];
page.on('pageerror', (error) => errors.push(error.message));
const results = [];
const cached = new Map();
async function historyResponse(path) {
  const url = new URL(path, origin);
  url.searchParams.sort();
  const route = url.pathname.split('/quota-history/')[1] ?? url.pathname.split('/api/v1/')[1];
  const cacheKey = route + url.search;
  if (!cached.has(cacheKey)) {
    const response = await context.request.get(`${origin}/v8/management/quota-history/${cacheKey}`, { headers: { Authorization: `Bearer ${key}` } });
    assert.equal(response.status(), 200);
    cached.set(cacheKey, await response.json());
  }
  return cached.get(cacheKey);
}
try {
  await page.addInitScript(() => {
    localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'en' }, version: 0 }));
    localStorage.setItem('cli-proxy-theme', JSON.stringify({ state: { theme: 'dark' }, version: 0 }));
  });
  await page.route('**/v8/management/requests/api-call', async (route) => {
    const payload = route.request().postDataJSON();
    const account = fixtures.accounts[payload.authIndex];
    assert(account, 'No live credential may be used in this fixture');
    const remaining = account.remaining - account.rate * (Date.now() - fixtures.base) / 3_600_000;
    const body = payload.url.endsWith('/usage')
      ? {
          seven_day: { utilization: Math.round((100 - remaining) * 100) / 100, resets_at: new Date(account.reset).toISOString() },
          ...(account.band === 'green' ? {
            five_hour: { utilization: 12, resets_at: new Date(Date.now() + 3_600_000).toISOString() },
            seven_day_sonnet: { utilization: 30, resets_at: new Date(account.reset).toISOString() },
          } : {}),
        }
      : { account: { has_claude_max: true } };
    await route.fulfill({ json: { status_code: 200, header: {}, body: JSON.stringify(body) } });
  });
  await page.route('**/v8/management/quota-history/*', async (route) => {
    if (/\/(snapshot|view)(\?|$)/.test(route.request().url())) return route.fulfill({ json: await historyResponse(route.request().url()) });
    return route.continue();
  });
  const persisted = Promise.all(Object.keys(fixtures.accounts).map((authIndex) => page.waitForResponse((response) =>
    response.url().endsWith('/v8/management/quota-history') && response.request().method() === 'POST' && response.request().postDataJSON().authIndex === authIndex
  )));
  await page.goto(`${origin}/management.html#/login`);
  await page.getByPlaceholder('Enter the management key').fill(key);
  await page.getByRole('button', { name: 'Login', exact: true }).click();
  assert((await persisted).every((response) => response.status() === 204));
  const notificationClose = page.getByRole('button', { name: 'Close', exact: true });
  if (await notificationClose.count()) await notificationClose.first().click();
  results.push('All quota readers persist observations through the authenticated backend');
  const links = await page.locator('aside a').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('href')));
  assert.equal(links[links.indexOf('#/quota') + 1], '#/quota-history');
  for (const path of ['#/ai-providers', '#/auth-files', '#/oauth', '#/config', '#/logs']) assert(links.includes(path));
  await page.locator('a[href="#/quota"]').first().click();
  await page.locator('[data-band]').first().waitFor();
  const markers = await page.locator('[data-band]').evaluateAll((nodes) => nodes.map((node) => ({ band: node.dataset.band, coverage: Number(node.dataset.coverage), width: getComputedStyle(node).width, label: node.getAttribute('aria-label') })));
  assert.deepEqual([...new Set(markers.map((marker) => marker.band))].sort(), ['blue', 'green', 'red', 'yellow']);
  assert(markers.every((marker) => marker.width === '3px' && marker.coverage > 0 && marker.coverage < 0.75));
  assert.equal(await page.getByRole('img', { name: 'Current time', exact: true }).count(), 4);
  const lanes = await page.locator('[data-band]').evaluateAll((nodes) => nodes.map((marker) => {
    const track = marker.parentElement;
    const row = track.parentElement;
    const current = track.querySelector('[aria-label="Current time"]');
    const shape = (element) => {
      const style = getComputedStyle(element);
      return Object.fromEntries(['width', 'height', 'top', 'bottom', 'borderRadius', 'transform', 'boxShadow'].map((key) => [key, style[key]]));
    };
    return {
      width: row.getBoundingClientRect().width,
      height: row.getBoundingClientRect().height,
      headerLines: row.firstElementChild.children.length,
      headerText: row.firstElementChild.textContent,
      projected: shape(marker), current: shape(current),
      before: getComputedStyle(marker, '::before').content,
      after: getComputedStyle(marker, '::after').content,
      color: getComputedStyle(marker).backgroundColor,
    };
  }));
  assert(lanes.every((lane) => lane.width === lanes[0].width && lane.height === 64 && lane.headerLines === 1), 'All provider rows must have equal dimensions and only the credential name');
  for (const lane of lanes) {
    assert.deepEqual(lane.projected, lane.current, 'Projected and current-time markers must have identical geometry');
    assert(['none', 'normal'].includes(lane.before) && ['none', 'normal'].includes(lane.after), 'Markers must not have arrows or other ornaments');
    assert.equal(lane.projected.boxShadow, 'none', 'Markers must not glow');
    assert(!/limit|history coverage|insufficient|\b7d\b/i.test(lane.headerText), 'Provider headers must not contain secondary quota labels');
  }
  assert.equal(await page.getByRole('heading', { name: 'Quota windows', exact: true }).locator('xpath=ancestor::section[1]').locator('footer').count(), 0, 'Quota windows must not show a legend');
  await page.waitForFunction(() => !document.querySelector('.page-transition--animating'));
  await page.screenshot({ path: resolve(evidenceRoot, 'quota-management-desktop.png'), fullPage: true });
  results.push('Equal provider row dimensions, no secondary labels or legend, and simple markers matching the white line');
  await page.locator('a[href="#/quota-history"]').first().click();
  await page.getByRole('heading', { name: 'Quota History', exact: true }).waitFor();
  assert.equal(await page.locator('[data-quota-history="v1"]').count(), 1, 'Use the v1 renderer rather than rebuilding its page');
  await page.locator('[data-filter="claude"]').first().click();
  await page.locator('[data-chart="rate"] [data-hit]').waitFor();
  const overlay = page.locator('[data-chart="rate"] [data-hit]');
  await overlay.focus();
  await page.keyboard.press('End');
  assert(Number(await overlay.getAttribute('aria-valuenow')) > 0);
  await page.keyboard.press('ArrowLeft');
  await page.getByRole('combobox').last().selectOption('7d');
  await page.locator('[data-chart="bars"]').waitFor();
  await page.getByRole('combobox').last().selectOption('30d');
  await page.getByRole('combobox').last().selectOption('24h');
  results.push('Original v1 provider tabs, period filters, keyboard chart inspection, bars and heatmap work');

  const reference = await context.newPage();
  reference.on('pageerror', (error) => errors.push(error.message));
  await reference.route('**/app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: referenceScript.replace("screen: 'overview'", "screen: 'history'") }));
  await reference.route('**/api/v1/**', async (route) => route.fulfill({ json: await historyResponse(route.request().url()) }));
  await reference.goto(process.env.V1_REFERENCE_URL || 'http://127.0.0.1:8318');
  await reference.locator('[data-filter="claude"]').first().click();
  await reference.locator('[data-chart="rate"] [data-hit]').waitFor();
  for (const current of [page, reference]) {
    await current.locator('#screen').evaluate((element) => { element.style.width = '1100px'; element.style.padding = '0'; });
    await current.locator('#screen h1').evaluate((element) => { element.textContent = 'Quota History'; });
    await current.locator('#screen .demo').evaluate((element) => { element.textContent = 'Credenciais do proxy · Histórico local'; });
    await current.locator('[data-chart="rate"] [data-hit]').blur();
    await current.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
  }
  const normalize = (html) => html.replace(/<g data-guide[^>]*>[\s\S]*?<\/g>/g, '').replace(/aria-valuenow="\d+"/g, 'aria-valuenow="0"').replace(/ aria-valuetext="[^"]*"/g, '');
  const layout = await page.locator('[data-quota-history="v1"]').evaluate((node) => {
    const rows = [];
    while (node) { const box=node.getBoundingClientRect(); rows.push({tag:node.tagName,classes:node.className,height:box.height,scrollHeight:node.scrollHeight,clientHeight:node.clientHeight,overflow:getComputedStyle(node).overflow});node=node.parentElement; }
    return rows;
  });
  await writeFile(resolve(evidenceRoot, 'layout.json'), JSON.stringify(layout,null,2));
  await page.locator('.heatmap .heat-cell').last().scrollIntoViewIfNeeded();
  const lastCell = await page.locator('.heatmap .heat-cell').last().boundingBox();
  assert(lastCell && lastCell.y >= 0 && lastCell.y + lastCell.height <= 1100, 'The complete history must be reachable by scrolling');
  await page.locator('.content').evaluate((element) => element.scrollTo({top:0,behavior:'instant'}));
  const targetPanels = await page.locator('#screen .panel').evaluateAll((nodes) => nodes.map((node) => node.outerHTML));
  const referencePanels = await reference.locator('#screen .panel').evaluateAll((nodes) => nodes.map((node) => node.outerHTML));
  assert.equal(targetPanels.length, referencePanels.length);
  assert.deepEqual(targetPanels.map(normalize), referencePanels.map(normalize), 'V1 history markup and SVG charts must match the reference');
  await page.locator('.content').evaluate((element) => element.scrollTo({ top: 0, behavior: 'instant' }));
  await page.setViewportSize({width:1600,height:2300});
  await reference.setViewportSize({width:1600,height:2300});
  await page.locator('#screen').screenshot({ path: resolve(evidenceRoot, 'quota-history-v1-replica.png') });
  await reference.locator('#screen').screenshot({ path: resolve(evidenceRoot, 'quota-history-v1-reference.png') });
  results.push('Rendered history panels and SVG charts match the original v1 for identical data and width');
  await reference.close();
  await page.locator('#screen').evaluate((element) => { element.style.removeProperty('width'); });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('.content').evaluate((element) => element.scrollTo({ top: 0, behavior: 'instant' }));
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1), false);
  await page.screenshot({ path: resolve(evidenceRoot, 'quota-history-mobile.png'), fullPage: true });
  results.push('V1 history fits mobile without page overflow');
  assert.deepEqual(errors, []);
  await writeFile(resolve(evidenceRoot, 'receipt.json'), JSON.stringify({ results, markers, lanes, errors }, null, 2));
  process.stdout.write(`${results.length} UI checks passed\n`);
} finally {
  await context.close();
  await browser.close();
}
