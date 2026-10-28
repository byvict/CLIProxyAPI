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
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
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
    if (account.provider === 'codex') {
      const credits = [48, 96].map((hours) => ({
        id: `fixture-reset-${hours}`, reset_type: 'codex_rate_limits', status: 'available',
        granted_at: new Date(fixtures.base - 86_400_000).toISOString(),
        expires_at: new Date(fixtures.base + hours * 3_600_000).toISOString(),
      }));
      const body = payload.url.endsWith('/usage')
        ? { plan_type: 'pro', rate_limit: { allowed: true, secondary_window: {
            used_percent: Math.round((100 - remaining) * 100) / 100, limit_window_seconds: 604800,
            reset_at: Math.round(account.reset / 1000),
          } }, rate_limit_reset_credits: { available_count: 2 } }
        : payload.url.endsWith('/rate-limit-reset-credits')
          ? { available_count: 2, applicable_available_count: 2, credits }
          : { active_until: new Date(account.reset).toISOString() };
      return route.fulfill({ json: { status_code: 200, header: {}, body: JSON.stringify(body) } });
    }
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
  assert.equal(links[links.indexOf('#/quota') + 1], '#/performance');
  assert.equal(links[links.indexOf('#/delegation') + 1], '#/quota-history');
  for (const path of ['#/ai-providers', '#/auth-files', '#/oauth', '#/config', '#/logs']) assert(links.includes(path));
  await page.locator('a[href="#/quota"]').first().click();
  await page.getByRole('button', { name: /^Claude/ }).first().click();
  await page.locator('[data-band]').first().waitFor();
  await page.waitForFunction(() => !document.querySelector('.page-transition--animating'));
  const markers = await page.locator('[data-band]').evaluateAll((nodes) => nodes.map((node) => ({ band: node.dataset.band, coverage: Number(node.dataset.coverage), width: getComputedStyle(node).width, label: node.getAttribute('aria-label') })));
  assert.deepEqual([...new Set(markers.map((marker) => marker.band))].sort(), ['blue', 'green', 'red', 'yellow']);
  assert(markers.every((marker) => marker.width === '3px' && marker.coverage > 0 && marker.coverage < 0.75));
  const currentMarker = page.getByRole('img', { name: 'Current time', exact: true });
  assert.equal(await currentMarker.count(), 1, 'Current time must be one continuous marker across all provider rows');
  const currentBounds = await currentMarker.boundingBox();
  const lanes = await page.locator('[data-band]').evaluateAll((nodes) => nodes.map((marker) => {
    const track = marker.parentElement;
    const row = track.parentElement;
    const current = document.querySelector('[aria-label="Current time"]');
    const name = row.querySelector('[class*="laneName"]');
    const icon = row.querySelector('img');
    const shape = (element) => {
      const style = getComputedStyle(element);
      return Object.fromEntries(['width', 'height', 'top', 'bottom', 'clipPath', 'transform', 'boxShadow', 'backgroundColor', 'backgroundImage', 'color', 'opacity'].map((key) => [key, style[key]]));
    };
    return {
      width: row.getBoundingClientRect().width,
      height: row.getBoundingClientRect().height,
      top: row.getBoundingClientRect().top,
      bottom: row.getBoundingClientRect().bottom,
      trackHeight: track.getBoundingClientRect().height,
      headerLines: name.getClientRects().length,
      headerText: name.textContent,
      headerTitle: name.title,
      iconLoaded: icon?.complete && icon.naturalWidth > 0,
      themeTone: getComputedStyle(row).getPropertyValue('--text-secondary').trim(),
      projected: shape(marker), current: shape(current),
      before: getComputedStyle(marker, '::before').content,
      after: getComputedStyle(marker, '::after').content,
      color: getComputedStyle(marker).backgroundColor,
    };
  }));
  assert(lanes.every((lane) => lane.width === lanes[0].width && lane.height >= 64 && lane.headerLines === 1), 'Provider rows retain their minimum height, equal widths, and a single identity line');
  assert(Math.abs(currentBounds.y - lanes[0].top) < 0.6);
  assert(Math.abs(currentBounds.y + currentBounds.height - lanes.at(-1).bottom) < 0.6, 'The dashed line must span all rows and their separators without restarting per provider');
  for (const lane of lanes) {
    assert.equal(lane.projected.width, '3px');
    assert.equal(parseFloat(lane.projected.height), lane.trackHeight, 'Exhaustion projections must remain full-height vertical bars within their provider row');
    assert.equal(lane.projected.clipPath, 'none', 'Exhaustion projections must not become reset-credit triangles');
    assert.equal(lane.current.width, '2px');
    assert.equal(lane.current.opacity, '0.85');
    const tone = lane.themeTone.match(/^#([\da-f]{2})([\da-f]{2})([\da-f]{2})$/i);
    assert(tone, 'The current-time marker must use the app theme tone');
    assert.equal(lane.current.color, `rgb(${tone.slice(1).map((value) => parseInt(value, 16)).join(', ')})`);
    assert.match(lane.current.backgroundImage, /^repeating-linear-gradient\(/, 'The shared current-time marker must be dashed');
    assert(lane.iconLoaded, 'Reuse the provider logo');
    assert.match(lane.headerText, /^Claude \(\d+(?:\.\d+)?%\)$/);
    assert.match(lane.headerTitle, /claude-.*\.json\nWeekly quota remaining: \d+(?:\.\d+)?%/);
    assert(['none', 'normal'].includes(lane.before) && ['none', 'normal'].includes(lane.after), 'Exhaustion projections must not have arrows');
    assert.equal(lane.projected.boxShadow, 'none', 'Markers must not glow');
    assert(!/limit|history coverage|insufficient|\b7d\b/i.test(lane.headerText), 'Provider headers must not contain secondary quota labels');
  }
  assert.equal(await page.getByRole('heading', { name: 'Quota windows', exact: true }).locator('xpath=ancestor::section[1]').locator('footer').count(), 0, 'Quota windows must not show a legend');
  await page.screenshot({ path: resolve(evidenceRoot, 'quota-management-desktop.png'), fullPage: true });
  results.push('One dashed current-time line spans all provider rows; original exhaustion bars, logos and weekly balances remain correct');
  const timeline = page.getByRole('heading', { name: 'Quota windows', exact: true }).locator('xpath=ancestor::section[1]');
  await timeline.screenshot({ path: resolve(evidenceRoot, 'quota-timeline-desktop.png') });
  const weeklyHeader = lanes.find((lane) => /claude-green\.json/.test(lane.headerTitle)).headerText;
  await timeline.getByRole('button', { name: '5-hour', exact: true }).click();
  assert.equal(await timeline.locator('[class*="laneName"]').textContent(), weeklyHeader, 'Session mode must retain the weekly balance instead of showing the 5-hour quota');
  await timeline.getByRole('button', { name: 'Weekly', exact: true }).click();
  await page.getByRole('button', { name: 'Theme', exact: true }).click();
  await page.getByRole('menuitemradio', { name: 'Wool Paper', exact: true }).click();
  const lightMarker = await timeline.getByRole('img', { name: 'Current time', exact: true }).evaluate((node) => ({ color: getComputedStyle(node).color, opacity: getComputedStyle(node).opacity }));
  assert.equal(lightMarker.color, 'rgb(109, 103, 96)');
  assert.equal(lightMarker.opacity, '0.85');
  await timeline.screenshot({ path: resolve(evidenceRoot, 'quota-timeline-light.png') });
  await page.getByRole('button', { name: 'Theme', exact: true }).click();
  await page.getByRole('menuitemradio', { name: 'Dark', exact: true }).click();
  await timeline.getByRole('button', { name: 'Next', exact: true }).click();
  await timeline.getByRole('button', { name: 'Next', exact: true }).click();
  assert.equal(await timeline.locator('[data-outside="before"]').count(), 4);
  assert.equal(await currentMarker.count(), 0, 'Hide the shared current-time marker when viewing a future period');
  await timeline.getByRole('button', { name: 'Today', exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(() => document.querySelector('.sidebar').getBoundingClientRect().right <= 0);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1), false);
  const mobileAlignment = await currentMarker.evaluate((node) => {
    const chart = node.parentElement.parentElement.parentElement;
    const axis = chart.querySelector('[class*="axisCells"]');
    const distance = () => {
      const marker = node.getBoundingClientRect();
      const cells = axis.getBoundingClientRect();
      return marker.left + marker.width / 2 - (cells.left + cells.width * parseFloat(node.style.left) / 100);
    };
    const before = distance();
    chart.scrollLeft = 120;
    const after = distance();
    const scrolled = chart.scrollLeft;
    chart.scrollLeft = 0;
    return { before, after, scrolled, axisWidth: axis.getBoundingClientRect().width, overlayWidth: node.parentElement.getBoundingClientRect().width };
  });
  assert.equal(mobileAlignment.scrolled, 120);
  assert(Math.abs(mobileAlignment.before) < 0.6 && Math.abs(mobileAlignment.after) < 0.6, `The shared marker must follow the timeline axis during mobile horizontal scrolling: ${JSON.stringify(mobileAlignment)}`);
  await timeline.screenshot({ path: resolve(evidenceRoot, 'quota-management-mobile.png') });
  await page.setViewportSize({ width: 1600, height: 1100 });
  results.push('Weekly balance remains correct in session mode, the current-time line follows the light theme and the timeline fits mobile');
  await page.getByRole('button', { name: /^Codex/ }).first().click();
  const resetMarkers = await timeline.getByRole('img', { name: /^Manual reset,/ }).evaluateAll((nodes) => nodes.map((node) => {
    const style = getComputedStyle(node);
    const bar = node.parentElement.querySelector('[class*="windowLive"]');
    return {
      label: node.getAttribute('aria-label'), width: style.width, height: style.height,
      clipPath: style.clipPath,
      alignment: node.getBoundingClientRect().bottom - bar.getBoundingClientRect().top,
    };
  }));
  assert.equal(resetMarkers.length, 2, 'Codex must expose two independent reset-credit expiry markers');
  for (const marker of resetMarkers) {
    assert.equal(marker.width, '10px');
    assert.equal(marker.height, '7px');
    assert.equal(marker.clipPath, 'polygon(0px 0px, 100% 0px, 50% 100%)');
    assert(Math.abs(marker.alignment) < 0.6, 'Only reset-credit triangles must point at the progress bar top edge');
  }
  const codexProjection = await timeline.locator('[data-band]').evaluateAll((nodes) => nodes.map((node) => ({
    width: getComputedStyle(node).width, clipPath: getComputedStyle(node).clipPath,
  })));
  assert.deepEqual(codexProjection, [{ width: '3px', clipPath: 'none' }], 'The Codex exhaustion projection must remain a vertical bar beside its two reset-credit arrows');
  await timeline.screenshot({ path: resolve(evidenceRoot, 'codex-reset-credits-desktop.png') });
  results.push('Two Codex reset-credit expiries are triangles while its exhaustion projection remains a vertical bar');
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
  await page.getByRole('combobox').last().selectOption('all');
  assert.equal(await page.getByRole('combobox').last().inputValue(), 'all');
  await page.getByRole('combobox').last().selectOption('24h');
  await page.locator('#cp-monitor[aria-busy="false"]').waitFor();
  results.push('Original v1 provider tabs, period filters, keyboard chart inspection, bars and heatmap work');

  const reference = await context.newPage();
  reference.on('pageerror', (error) => errors.push(error.message));
  if (!process.env.V1_REFERENCE_URL) {
    const markup = await readFile(new URL('../internal/submon/ui/index.html', import.meta.url), 'utf8');
    const css = await readFile(new URL('../management/v1-source/style.css', import.meta.url), 'utf8');
    await reference.route('http://v1-reference.fixture.invalid/', route => route.fulfill({ contentType: 'text/html', body: markup }));
    await reference.route('**/style.css', route => route.fulfill({ contentType: 'text/css', body: css }));
  }
  await reference.route('**/app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: referenceScript.replace("screen: 'overview'", "screen: 'history'") }));
  await reference.route('**/api/v1/**', async (route) => route.fulfill({ json: await historyResponse(route.request().url()) }));
  await reference.goto(process.env.V1_REFERENCE_URL || 'http://v1-reference.fixture.invalid/');
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
  await writeFile(resolve(evidenceRoot, 'receipt.json'), JSON.stringify({ results, markers, lanes, currentBounds, mobileAlignment, resetMarkers, codexProjection, errors }, null, 2));
  process.stdout.write(`${results.length} UI checks passed\n`);
} finally {
  await context.close();
  await browser.close();
}
