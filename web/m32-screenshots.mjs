import { chromium } from 'playwright';
import { mkdirSync } from 'fs';

const OUT = '/root/repos/dblock/demos/m32';
mkdirSync(OUT, { recursive: true });

const BASE = 'http://10.0.0.101:8080';
const USER = process.env.SKOED_USER || 'admin';
const PASS = process.env.SKOED_PASS || 'Skoed2026!';

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

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

  await login(page);

  // SS1: Settings page — DNS section showing per-domain routes
  await page.goto(`${BASE}/settings`);
  await page.waitForTimeout(2000);
  await shot(page, 'ss-32-01-settings-dns-routes', true);
  console.log('ss-32-01: Settings DNS with per-domain routes');

  // SS2: Cluster page — all 3 nodes in_sync with version
  await page.goto(`${BASE}/cluster`);
  await page.waitForTimeout(2000);
  await shot(page, 'ss-32-02-cluster-nodes-in-sync');
  console.log('ss-32-02: Cluster nodes all in_sync');

  // SS3: Settings — scroll to DNS routes section
  await page.goto(`${BASE}/settings`);
  await page.waitForTimeout(2000);
  // Scroll to DNS section
  await page.evaluate(() => {
    const el = document.querySelector('[id="dns-upstreams"]');
    if (el) el.scrollIntoView({ behavior: 'instant', block: 'center' });
  });
  await page.waitForTimeout(500);
  await shot(page, 'ss-32-03-settings-routes-detail');
  console.log('ss-32-03: Per-domain routes detail');

  await browser.close();
  console.log('Done.');
})();
