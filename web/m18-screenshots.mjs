import { chromium } from 'playwright';
import { mkdirSync } from 'fs';

const OUT = '/tmp/m18-screenshots';
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

// 1. Dashboard overview
console.log('\n1) Dashboard');
await login(page);
await shot(page, '01-dashboard', true);

// 2. Cluster status — all 3 nodes healthy after rolling upgrade
console.log('\n2) Cluster status (post-upgrade)');
await page.goto(`${BASE}/dashboard/cluster`);
await page.waitForTimeout(3000);
await shot(page, '02-cluster-status-post-upgrade', true);

// 3. Rolling upgrade UI — status endpoint response
console.log('\n3) Query log showing blocked/allowed after upgrade');
await page.goto(`${BASE}/dashboard/query-log`);
await page.waitForTimeout(3000);
await shot(page, '03-query-log-post-upgrade', true);

// 4. Profiles — showing kids/adults/iot with client IPs
console.log('\n4) Profiles overview');
await page.goto(`${BASE}/dashboard/profiles`);
await page.waitForTimeout(2500);
await shot(page, '04-profiles-overview', true);

// 5. Blocklists — shows oisd-small, cat:adult, cat:ads with domain counts
console.log('\n5) Blocklists');
await page.goto(`${BASE}/dashboard/blocklists`);
await page.waitForTimeout(2500);
await shot(page, '05-blocklists-with-counts', true);

// 6. API docs — show rolling upgrade endpoints
console.log('\n6) API docs — upgrade endpoints');
await page.goto(`${BASE}/api/docs`);
await page.waitForTimeout(3000);
await shot(page, '06-api-docs-overview', true);

// Try scrolling to rolling upgrade section
const upgradeLink = page.locator('a, button').filter({ hasText: /upgrade/i }).first();
const hasUpgrade = await upgradeLink.isVisible({ timeout: 4000 }).catch(() => false);
console.log('  upgrade section visible:', hasUpgrade);
if (hasUpgrade) {
  await upgradeLink.scrollIntoViewIfNeeded();
  await page.waitForTimeout(500);
  await shot(page, '07-api-docs-upgrade-section');
}

// 7. Cluster status on CT 201 (follower) via port 9171
console.log('\n7) Follower node cluster status');
const ctx2 = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page2 = await ctx2.newPage();
await page2.goto('http://localhost:9171/login');
await page2.waitForSelector('input[type="password"]', { timeout: 10000 }).catch(() => {});
await page2.locator('#u').fill(USER).catch(() => {});
await page2.locator('input[type="password"]').first().fill(PASS).catch(() => {});
await page2.locator('input[type="password"]').first().press('Enter').catch(() => {});
await page2.waitForTimeout(3000);
await page2.goto('http://localhost:9171/dashboard/cluster');
await page2.waitForTimeout(3000);
await shot(page2, '08-follower-cluster-status', true);
await ctx2.close();

await browser.close();
console.log('\nAll screenshots saved to', OUT);
