import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { basename, dirname, join, isAbsolute } from 'node:path';
import { pathToFileURL } from 'node:url';

const [baseURL, keyFile, evidenceDir] = process.argv.slice(2);
assert(baseURL && keyFile && evidenceDir, 'Usage: node scripts/subscription-monitor-ui-check.mjs <fixture-url> <fixture-key-file> <evidence-dir>');
assert(basename(dirname(keyFile)) === 'fixture', 'Use the isolated fixture server key file.');
assert(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(baseURL).hostname), 'Use a loopback fixture server.');
assert(isAbsolute(evidenceDir), 'Evidence directory must be absolute.');
await mkdir(evidenceDir, { recursive: true });
const moduleName = process.env.SUBMON_PLAYWRIGHT_MODULE || 'playwright';
const { chromium } = await import(isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
const page = await context.newPage();
const failures = [];
page.on('pageerror', error => failures.push(error.message));
await page.route('**/*', route => new URL(route.request().url()).origin === new URL(baseURL).origin ? route.continue() : route.abort());
const checks = [];
async function capture(label) {
  await page.screenshot({ path: join(evidenceDir, label + '.png'), fullPage: true });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false, label + ' has horizontal page overflow');
  assert.deepEqual(failures, [], label + ' has JavaScript errors');
  checks.push(label);
}
try {
  await page.goto(baseURL);
  await page.getByLabel('Chave do monitor', { exact: true }).fill((await readFile(keyFile, 'utf8')).trim());
  await page.getByRole('button', { name: 'Entrar', exact: true }).click();
  await page.getByRole('heading', { name: 'Monitor de assinaturas', exact: true }).waitFor();
  await page.locator('#cp-monitor[aria-busy="false"]').waitFor();
  await page.locator('.provider-card').first().waitFor();
  assert.equal(await page.locator('[data-chart]').count(), 0, 'All silently selected one provider chart');
  await capture('desktop-overview-all');
  await page.locator('.provider-tabs [data-filter="claude"]').click();
 await page.locator('#cp-monitor[aria-busy="false"]').waitFor();
  await page.locator('[data-chart="remaining"] svg').first().waitFor();
  await capture('desktop-remaining');
  const chart = page.locator('[data-hit]').first();
  await chart.focus();
  await chart.press('ArrowRight');
  assert.match(await chart.getAttribute('aria-valuetext'), /Observação|Projeção|Reset|Recorrência/);
  await page.getByRole('tab', { name: 'Ritmo de consumo' }).click();
  await page.locator('[data-chart="rate"] svg').first().waitFor();
  await capture('desktop-rate');
  await page.getByRole('button', { name: 'Recolher gráficos' }).click();
  assert.equal(await page.locator('[data-chart]').count(), 0);
  assert.ok(await page.locator('.chart-summary').count());
  await page.getByRole('button', { name: 'Cotas por conta', exact: true }).click();
  await page.locator('.account-row').first().waitFor();
  await capture('desktop-accounts');
  const reveal = page.getByRole('button', { name: 'Revelar este e-mail' }).first();
  await reveal.click();
  await page.getByRole('button', { name: 'Ocultar e-mail' }).first().waitFor();
  assert.match(await page.locator('#account-rows').innerText(), /example\.test/);
  await page.getByRole('button', { name: 'Ocultar e-mail' }).first().click();
  await page.getByRole('button', { name: 'Histórico', exact: true }).click();
  await page.locator('.heatmap').first().waitFor();
  await page.locator('[data-select="period"]').selectOption('7d');
 await page.locator('#cp-monitor[aria-busy="false"]').waitFor();
  await page.locator('[data-chart="bars"] svg').first().waitFor();
  await capture('desktop-history');
  await page.getByRole('button', { name: 'Ritmo e projeções', exact: true }).click();
  await page.locator('.metric-grid').first().waitFor();
  await capture('desktop-forecast');
  await page.setViewportSize({ width: 390, height: 844 });
  for (const [nav, label] of [['overview', 'overview'], ['accounts', 'accounts'], ['history', 'history'], ['forecast', 'forecast']]) {
    await page.locator('[data-nav="' + nav + '"]').click();
    await page.locator('h1').waitFor();
    await capture('narrow-' + label);
  }
  await writeFile(join(evidenceDir, 'ui-receipt.json'), JSON.stringify({ checks, javascriptErrors: failures, surface: 'actual embedded assets and authenticated handler with test-only SQLite history' }, null, 2));
  console.log(JSON.stringify({ passed: checks.length, evidenceDir }));
} finally {
  await context.close();
  await browser.close();
}
