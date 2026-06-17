import { chromium } from 'playwright';
import { mkdirSync } from 'fs';

const OUT = '/tmp/m17-screenshots';
mkdirSync(OUT, { recursive: true });

// Node-1 (leader, CT 200) forwarded on 9170
const BASE = 'http://localhost:9170';
const USER = 'admin';
const PASS = 'Skoed2026!';

async function login(page) {
  await page.goto(`${BASE}/login`);
  await page.waitForSelector('input[type="password"]', { timeout: 10000 });
  await page.locator('#u').fill(USER);
  await page.locator('input[type="password"]').first().fill(PASS);
  await page.locator('input[type="password"]').first().press('Enter');
  await page.waitForURL(`${BASE}/dashboard**`, { timeout: 10000 }).catch(() => {});
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

// ─── 1. Login + dashboard overview ───────────────────────────────────────
console.log('\n1) Login and dashboard');
await login(page);
await shot(page, '01-dashboard', true);

// ─── 2. Schedules page — shows the "school" schedule ─────────────────────
console.log('\n2) Schedules page');
await page.goto(`${BASE}/dashboard/schedules`);
await page.waitForTimeout(2500);
await shot(page, '02-schedules-list', true);

// Try to open the schedule detail for "school"
const schoolCard = page.locator('text=school').first();
const hasSchool = await schoolCard.isVisible({ timeout: 3000 }).catch(() => false);
console.log('  school schedule visible:', hasSchool);
if (hasSchool) {
  await schoolCard.click();
  await page.waitForTimeout(1500);
  await shot(page, '03-schedule-detail-school');
}

// ─── 3. API docs — GET /schedules/{id}/bindings endpoint ─────────────────
console.log('\n3) API docs');
await page.goto(`${BASE}/api/v1/docs`);
await page.waitForTimeout(3000);
await shot(page, '04-api-docs-overview', true);

// Try to find and scroll to the bindings endpoint
const bindingsLink = page.locator('text=/bindings/').first();
const hasBindings = await bindingsLink.isVisible({ timeout: 4000 }).catch(() => false);
console.log('  bindings endpoint visible in docs:', hasBindings);
if (hasBindings) {
  await bindingsLink.scrollIntoViewIfNeeded();
  await page.waitForTimeout(500);
  await shot(page, '05-api-docs-bindings-endpoint');
}

// ─── 4. Config export — shows schedules in exported YAML ─────────────────
console.log('\n4) Settings / config export');
await page.goto(`${BASE}/dashboard/settings`);
await page.waitForTimeout(2500);
await shot(page, '06-settings-overview', true);

// Click export button if available
const exportBtn = page.locator('button').filter({ hasText: /export/i }).first();
const hasExport = await exportBtn.isVisible({ timeout: 3000 }).catch(() => false);
console.log('  export button visible:', hasExport);
if (hasExport) {
  await exportBtn.click();
  await page.waitForTimeout(2000);
  await shot(page, '07-config-export-triggered');
}

// ─── 5. Cluster status — all 3 nodes healthy ─────────────────────────────
console.log('\n5) Cluster status');
await page.goto(`${BASE}/dashboard/cluster`);
await page.waitForTimeout(3000);
await shot(page, '08-cluster-status-3-nodes', true);

await browser.close();
console.log('\nAll screenshots saved to', OUT);
