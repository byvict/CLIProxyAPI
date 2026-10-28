import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [origin, fixtureRoot, evidenceRoot] = process.argv.slice(2);
if (!origin || !fixtureRoot || !evidenceRoot) throw new Error('Usage: delegation-ui-check.mjs <origin> <fixture-root> <evidence-root>');
const { chromium } = await import(pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE || 'node_modules/playwright/index.mjs')).href);
const key = (await readFile(resolve(fixtureRoot, 'management.key'), 'utf8')).trim();
await mkdir(evidenceRoot, { recursive: true });
const denial = await fetch(`${origin}/v8/management/delegation`);
assert.equal(denial.status, 401);
const response = await fetch(`${origin}/v8/management/delegation`, { headers: { Authorization: `Bearer ${key}` } });
assert.equal(response.status, 200);
assert.equal(response.headers.get('cache-control'), 'no-store');
const data = await response.json();
assert.equal(data.status, 'ready');
const expected = JSON.parse(await readFile(resolve(fixtureRoot, 'expected-metrics.json'), 'utf8'));
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1500, height: 1100 }, timezoneId: 'America/Sao_Paulo', reducedMotion: 'reduce' });
const page = await context.newPage();
const errors = [], checks = [];
page.on('pageerror', (error) => errors.push(error.message));
const stat = (name) => page.getByText(name, { exact: true }).locator('..').locator('strong');
const settle = () => page.locator('.page-transition--animating').waitFor({ state: 'hidden' });
try {
  await page.addInitScript(() => {
    localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'en' }, version: 0 }));
    localStorage.setItem('cli-proxy-theme', JSON.stringify({ state: { theme: 'dark' }, version: 0 }));
  });
  await page.route('**/v8/management/requests/api-call', (route) => route.fulfill({ status: 503, json: { error: 'Provider calls are disabled in the UI fixture' } }));
  await page.goto(`${origin}/management.html#/login`);
  await page.getByPlaceholder('Enter the management key').fill(key);
  await page.getByRole('button', { name: 'Login', exact: true }).click();
  await page.locator('a[href="#/delegation"]').first().waitFor();
  const close = page.getByRole('button', { name: 'Close', exact: true });
  if (await close.count()) await close.first().click();
  const links = await page.locator('aside a').evaluateAll((items) => items.map((item) => item.getAttribute('href')));
  assert.equal(links[links.indexOf('#/performance') + 1], '#/delegation');
  assert(links.includes('#/quota-history'));
  await page.locator('a[href="#/delegation"]').first().click();
  await page.getByRole('main', { name: 'Acompanhamento dos agentes' }).waitFor();
  await stat('Aproveitados / avaliados').getByText(`${expected.used} / ${expected.evaluated}`, { exact: true }).waitFor();
  assert.equal((await stat('Despachos no recorte').textContent()).trim(), new Intl.NumberFormat('pt-BR').format(expected.work));
  assert.equal((await stat('Aplicação comprovada').textContent()).trim(), `${expected.applied} / ${expected.applicationEvaluated}`);
  assert.equal((await stat('Exclusivos nas três famílias').textContent()).trim(), `${expected.allExclusive} / ${expected.panels}`);
  checks.push('Actual authenticated API and all four UI metrics agree with independently audited golden totals; no transcript mock.');
  await settle();
  await page.screenshot({ path: resolve(evidenceRoot, 'delegation-desktop.png'), fullPage: false });
  await page.getByRole('heading', { name: 'Aproveitamento por cadeira', exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: resolve(evidenceRoot, 'delegation-charts.png'), fullPage: false });
  const before = await page.getByRole('table').first().textContent();
  await page.getByRole('button', { name: 'Despachante imediato', exact: true }).click();
  assert.notEqual(await page.getByRole('table').first().textContent(), before);
  await page.getByRole('button', { name: 'Agente raiz', exact: true }).click();
  await page.getByLabel('Cadeira', { exact: true }).selectOption('review');
  assert.equal((await stat('Aproveitados / avaliados').textContent()).trim(), `${expected.reviewUsed} / ${expected.reviewEvaluated}`);
  checks.push('Immediate dispatcher and root matrices differ; the review filter excludes the verification artifact.');
  await page.getByLabel('Cadeira', { exact: true }).selectOption('');
  await page.getByLabel('Modelo delegado', { exact: true }).selectOption('grok-4.7');
  assert.equal((await stat('Aproveitados / avaliados').textContent()).trim(), '0 / 0');
  await page.getByText('Sem avaliação', { exact: true }).first().waitFor();
  assert.equal((await stat('Exclusivos nas três famílias').textContent()).trim(), '0 / 0');
  checks.push('An uninspected family shows no evaluable denominator; partial rosters never become complete panels.');
  await page.getByLabel('Modelo delegado', { exact: true }).selectOption('');
  await page.getByLabel('Projeto', { exact: true }).selectOption('2291c2c1-6211-4fbb-85b3-d255fb03310d');
  assert.equal((await stat('Aproveitados / avaliados').textContent()).trim(), '0 / 0');
  await page.getByLabel('Projeto', { exact: true }).selectOption('');
  await page.getByLabel('De', { exact: true }).fill('2026-10-28');
  await page.getByLabel('Até', { exact: true }).fill('2026-10-27');
  await page.getByRole('alert').filter({ hasText: 'data inicial' }).waitFor();
  await page.getByLabel('De', { exact: true }).fill('');
  await page.getByLabel('Até', { exact: true }).fill('');
  const first = page.locator('details').first();
  await first.locator('summary').click();
  await first.getByText('Aplicação comprovada', { exact: false }).waitFor();
  await first.locator('code').filter({ hasText: 'Head:' }).waitFor();
  await first.locator('summary').click();
  checks.push('Project/date filters operate and a real accepted contribution exposes exact decision/artifact references.');
  await page.setViewportSize({ width: 390, height: 844 });
  if (await page.locator('aside.sidebar').evaluate((node) => node.classList.contains('open'))) await page.locator('.mobile-menu-btn').click();
  await page.waitForFunction(() => document.querySelector('aside.sidebar').getBoundingClientRect().right <= 0);
  await settle();
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 2));
  await page.getByRole('heading', { name: 'Agentes', exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: resolve(evidenceRoot, 'delegation-mobile.png'), fullPage: false });
  checks.push('Mobile controls and page stay within viewport; wide comparison tables scroll inside their containers.');
  await page.setViewportSize({ width: 1500, height: 1100 });
  for (const [name, value] of [
    ['unavailable', { status: 'unavailable' }],
    ['empty', { status: 'ready', report: { ...data.report, dispatches: [], contributions: [], panels: [], attribution_coverage: [], evidence: [] } }],
    ['error', null],
  ]) {
    await page.route('**/v8/management/delegation', (route) => route.fulfill(value ? { json: value } : { status: 503, json: { error: 'Fixture publication is invalid' } }));
    await page.getByRole('button', { name: 'Atualizar', exact: true }).click();
    if (name === 'unavailable') await page.getByText('A coleta ainda não está disponível', { exact: true }).waitFor();
    if (name === 'empty') await page.getByText('Nenhum despacho no período e filtros selecionados.', { exact: true }).waitFor();
    if (name === 'error') await page.getByRole('alert').waitFor();
    await page.unroute('**/v8/management/delegation');
  }
  await page.getByRole('button', { name: 'Atualizar', exact: true }).click();
  await page.getByRole('main', { name: 'Acompanhamento dos agentes' }).waitFor();
  checks.push('Unavailable, empty and invalid-publication states are distinct and recover by refresh.');
  for (const route of ['quota', 'quota-history', 'performance', 'delegation']) {
    await page.locator(`a[href="#/${route}"]`).first().click();
    await settle();
    assert(page.url().endsWith(`#/${route}`));
  }
  assert.deepEqual(errors, []);
  checks.push('Existing quota, history and Performance navigation survives; no browser runtime errors.');
  await writeFile(resolve(evidenceRoot, 'receipt.json'), JSON.stringify({ status: 'PASS', sourceGeneration: data.report.generation_id, expected, checks, pageErrors: errors }, null, 2));
  console.log(JSON.stringify({ status: 'PASS', checks: checks.length, generation: data.report.generation_id }));
} finally { await browser.close(); }
