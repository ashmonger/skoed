import { chromium } from 'playwright';
import { mkdirSync } from 'fs';

const OUT = '/home/jcollin/repos/dblock/demos/m345';
mkdirSync(OUT, { recursive: true });

const BASE = 'http://localhost:8080';
const USER = process.env.SKOED_USER || 'admin';
const PASS = process.env.SKOED_PASS || '';

async function login(page) {
  await page.goto(`${BASE}/login`);
  await page.waitForSelector('input[type="password"]', { timeout: 10000 });
  await page.locator('#u').fill(USER);
  await page.locator('input[type="password"]').first().fill(PASS);
  await page.locator('input[type="password"]').first().press('Enter');
  await page.waitForURL(`${BASE}/dashboard**`, { timeout: 10000 }).catch(() => {});
  await page.waitForTimeout(1500);
  await page.evaluate(() => {
    localStorage.setItem('skoed.theme', JSON.stringify({ mode: 'dark', palette: 'lipgloss' }));
  });
  await page.reload();
  await page.waitForTimeout(1500);
}

async function shot(page, name, fullPage = false) {
  const path = `${OUT}/${name}.png`;
  await page.screenshot({ path, fullPage });
  console.log(`  saved ${path}`);
}

const browser = await chromium.launch({ args: ['--no-sandbox'] });
const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await ctx.newPage();

console.log('\n1) Login');
await login(page);

const token = await page.evaluate(async ([u, p]) => {
  const r = await fetch('/api/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: u, password: p }),
  });
  const d = await r.json();
  return d.token;
}, [USER, PASS]);

// Reset to 8h default before screenshots
await page.evaluate(async (tok) => {
  await fetch('/api/v1/settings', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${tok}` },
    body: JSON.stringify({ auth: { session_timeout_seconds: 28800 } }),
  });
}, token);
await page.waitForTimeout(1000);

console.log('\n2) Settings page - Auth section with 8h default');
await page.goto(`${BASE}/settings`);
await page.waitForTimeout(2000);
await shot(page, 'ss-345-01-settings-default-8h');

console.log('\n3) Open session timeout dropdown');
// Click the session timeout select/dropdown
const selectEl = page.locator('select[name="session_timeout_seconds"], select').filter({ hasText: /hour|minute|day/i }).first();
await selectEl.click({ force: true }).catch(() => {});
await page.waitForTimeout(500);
// Try scrolling into view of the auth section
await page.locator('text=Session Timeout').scrollIntoViewIfNeeded().catch(() => {});
await page.waitForTimeout(500);
await shot(page, 'ss-345-02-settings-dropdown-open');

console.log('\n4) Set to 1 hour and save');
// Select 1 hour (3600 seconds)
await page.evaluate(async (tok) => {
  await fetch('/api/v1/settings', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${tok}` },
    body: JSON.stringify({ auth: { session_timeout_seconds: 3600 } }),
  });
}, token);
await page.reload();
await page.waitForTimeout(2000);
await shot(page, 'ss-345-03-settings-1h-saved');

console.log('\n5) Cluster page - all nodes in sync after config change');
await page.goto(`${BASE}/cluster`);
await page.waitForTimeout(2000);
await shot(page, 'ss-345-04-cluster-synced');

await browser.close();
console.log('\nDone.');
