import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [origin, fixtureRoot, evidenceRoot] = process.argv.slice(2);
if (!origin || !fixtureRoot || !evidenceRoot) throw new Error('Usage: performance-ui-check.mjs <origin> <fixture-root> <evidence-root>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
const key = (await readFile(resolve(fixtureRoot, 'management.key'), 'utf8')).trim();
await mkdir(evidenceRoot, { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1500, height: 1650 }, timezoneId: 'America/Sao_Paulo', reducedMotion: 'reduce' });
const page = await context.newPage();
const errors = [];
page.on('pageerror', (error) => errors.push(error.message));
const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'America/Sao_Paulo' }).format(new Date());
const year = Number(today.slice(0, 4));
const days = [];
for (let at = new Date(`${year}-01-01T12:00:00Z`), index = 0; at.toISOString().slice(0, 10) <= today; at.setUTCDate(at.getUTCDate() + 1), index++) {
  days.push({ date: at.toISOString().slice(0, 10), agents: 5 + index % 37, tokens: 200_000 + ((index * 81931) % 1_300_000), reportedAgents: 5 + index % 37, partialAgents: 0, longestDurationMs: (index % 20 + 1) * 60000 });
}
const activity = { year, today, generatedAt: new Date().toISOString(), timezone: 'America/Sao_Paulo', availableFrom: `${year}-01-01`, providers: ['claude', 'codex', 'grok', 'meta'].map((id, i) => ({ id, days: days.map((day) => ({ ...day, tokens: id === 'grok' ? 0 : day.tokens * (i + 1), reportedAgents: id === 'grok' ? 0 : day.reportedAgents })) })) };
let githubState = { status: 'connected', source: 'configuration', activity: { login: 'fixture-user', year, contributions: 1023, pullRequests: 121, reviews: 77, commits: 825, days: days.map((day, i) => ({ date: day.date, count: i % 31 })) } };
const githubConnected = structuredClone(githubState);
let performanceMode = 'ready';
const report = [];
async function settled() {
  await page.locator('.page-transition--animating').waitFor({ state: 'hidden' });
}
try {
  await page.addInitScript(() => {
    localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'en' }, version: 0 }));
    localStorage.setItem('cli-proxy-theme', JSON.stringify({ state: { theme: 'dark' }, version: 0 }));
  });
  await page.route('**/v8/management/performance', (route) => route.fulfill(performanceMode === 'error' ? { status: 503, json: { error: 'Activity source unavailable' } } : { json: performanceMode === 'unavailable' ? { status: 'unavailable' } : { status: 'ready', activity } }));
  await page.route('**/v8/management/infra/github', async (route) => {
    if (route.request().method() === 'POST') {
      const token = route.request().postDataJSON().token;
      if (token === 'invalid-fixture-token') return route.fulfill({ status: 422, json: { error: 'GitHub rejected the token' } });
      assert.equal(token, 'valid-fixture-token');
      githubState = githubConnected;
      return route.fulfill({ json: { status: 'ok' } });
    }
    return route.fulfill({ json: githubState });
  });
  await page.goto(`${origin}/management.html#/login`);
  await page.getByPlaceholder('Enter the management key').fill(key);
  await page.getByRole('button', { name: 'Login', exact: true }).click();
  await page.locator('a[href="#/performance"]').first().waitFor();
  const close = page.getByRole('button', { name: 'Close', exact: true });
  if (await close.count()) await close.first().click();
  const links = await page.locator('aside a').evaluateAll((items) => items.map((item) => item.getAttribute('href')));
  assert.equal(links[links.indexOf('#/quota') + 1], '#/performance');
  assert.equal(links[links.indexOf('#/ai-providers') + 1], '#/infra');
  assert(links.includes('#/quota-history'));
  report.push('Performance follows Quota Management; Infra follows AI Providers; History remains reachable.');
  await page.locator('a[href="#/performance"]').first().click();
  const content = page.getByRole('main', { name: 'Performance de Todos', exact: true });
  await content.getByRole('img', { name: `Calendário de tokens em ${year}` }).waitFor();
  assert.equal(await content.locator('circle').count(), (year % 4 === 0 ? 366 : 365));
  const allTokens = await content.locator('section[aria-label="Tokens"] p').first().textContent();
  await page.getByRole('button', { name: 'Claude', exact: true }).click();
  const claudeTokens = await page.locator('section[aria-label="Tokens"] p').first().textContent();
  assert.notEqual(allTokens, claudeTokens);
  for (const provider of ['Codex', 'Grok', 'Meta', 'Todos']) {
    await page.getByRole('button', { name: provider, exact: true }).click();
    await page.getByRole('main', { name: `Performance de ${provider}`, exact: true }).waitFor();
    if (provider === 'Grok') await page.getByText('Sem dados de tokens', { exact: true }).waitFor();
  }
  report.push('All provider tabs filter the same real metric contract; missing tokens remain unavailable.');
  await settled();
  await page.screenshot({ path: resolve(evidenceRoot, 'performance-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'GitHub', exact: true }).click();
  await page.getByText('121 pull requests · 77 reviews · 825 commits', { exact: true }).waitFor();
  await settled();
  await page.screenshot({ path: resolve(evidenceRoot, 'performance-github.png'), fullPage: true });
  const chart = page.getByRole('img', { name: 'tokens nos últimos 30 dias. Use as setas para explorar.', exact: true });
  await chart.focus(); await chart.press('ArrowRight');
  await page.locator('[aria-live="polite"]').filter({ hasText: 'tokens' }).first().waitFor();
  report.push('GitHub calendar and contribution counts render; chart values can be explored by keyboard.');
  await page.locator('a[href="#/infra"]').first().click();
  await page.getByText('@fixture-user', { exact: true }).waitFor();
  await page.getByLabel('Personal access token', { exact: true }).fill('invalid-fixture-token');
  await page.getByRole('button', { name: 'Validar e atualizar', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: 'GitHub rejected' }).waitFor();
  await page.getByText('@fixture-user', { exact: true }).waitFor();
  await page.getByLabel('Personal access token', { exact: true }).fill('valid-fixture-token');
  await page.getByRole('button', { name: 'Validar e atualizar', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('input[name="github-token"]')?.value === '');
  const storage = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  assert(!storage.includes('fixture-token'));
  await settled();
  await page.screenshot({ path: resolve(evidenceRoot, 'infra-desktop.png'), fullPage: true });
  report.push('Connection validation errors preserve the existing account; success clears the secret input; no token in browser storage.');
  githubState = { status: 'disconnected' };
  await page.locator('a[href="#/performance"]').first().click();
  await page.getByRole('button', { name: 'GitHub', exact: true }).click();
  await page.getByRole('link', { name: 'Conectar GitHub →', exact: true }).click();
  await page.getByRole('heading', { name: 'Conectar com token', exact: true }).waitFor();
  report.push('Disconnected calendar leads directly to Infra.');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('button.mobile-menu-btn').click();
  await page.locator('a[href="#/performance"]').first().click();
  await page.getByRole('main', { name: 'Performance de Todos', exact: true }).waitFor();
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1));
  await settled();
  await page.screenshot({ path: resolve(evidenceRoot, 'performance-mobile.png'), fullPage: true });
  await page.locator('button.mobile-menu-btn').click();
  await page.locator('a[href="#/infra"]').first().click();
  await page.getByRole('heading', { name: 'Conectar com token', exact: true }).waitFor();
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1));
  await settled();
  await page.screenshot({ path: resolve(evidenceRoot, 'infra-mobile.png'), fullPage: true });
  report.push('Performance and Infra fit a 390px viewport without page overflow.');
  performanceMode = 'unavailable';
  await page.locator('button.mobile-menu-btn').click();
  await page.locator('a[href="#/performance"]').first().click();
  await page.getByText('Nenhum histórico T3code disponível neste servidor.', { exact: false }).waitFor();
  performanceMode = 'error';
  await page.getByRole('button', { name: 'Atualizar', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: 'Activity source unavailable' }).waitFor();
  performanceMode = 'ready';
  await page.getByRole('button', { name: 'Tentar novamente', exact: true }).click();
  await page.getByRole('img', { name: `Calendário de tokens em ${year}` }).waitFor();
  report.push('Missing local history, read failure, and retry recovery remain distinct states.');
  assert.deepEqual(errors, []);
  await writeFile(resolve(evidenceRoot, 'receipt.json'), JSON.stringify({ passed: report, errors, fixture: true }, null, 2));
  console.log(JSON.stringify({ passed: report.length, errors }));
} catch (error) {
  await page.screenshot({ path: resolve(evidenceRoot, "failure.png"), fullPage: true });
  console.error(await page.locator("body").innerText());
  throw error;
} finally { await browser.close(); }
