import { chromium } from 'playwright';
import { mkdirSync } from 'fs';

const OUT = '/root/repos/dblock/demos/m35';
mkdirSync(OUT, { recursive: true });

const BASE = 'http://10.0.0.101:8080';
const USER = process.env.SKOED_USER || 'admin';
// Never hardcode credentials — require the password from the environment.
const PASS = process.env.SKOED_PASS || '';
if (!PASS) {
  console.error('SKOED_PASS env var is required (admin password); refusing to run without it.');
  process.exit(1);
}

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

// Ensure the demo profile exists with per-client pause active
const token = await page.evaluate(async ([u, p]) => {
  const r = await fetch('/api/v1/auth/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: u, password: p }),
  });
  return (await r.json()).token;
}, [USER, PASS]);

const apiFetch = (method, path, body) => page.evaluate(async ([m, p, b, t]) => {
  const r = await fetch(p, {
    method: m,
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${t}` },
    body: b ? JSON.stringify(b) : undefined,
  });
  return { status: r.status, body: await r.json().catch(() => null) };
}, [method, path, body, token]);

// Ensure demo profile exists
await apiFetch('DELETE', '/api/v1/profiles/m35demo');
await apiFetch('POST', '/api/v1/profiles', {
  id: 'm35demo', name: 'Kids (M35 Demo)',
  client_ips: ['10.0.0.200', '10.0.0.201'],
  blocklist_categories: ['ads', 'malware'],
});
// Set per-client pause
await apiFetch('POST', '/api/v1/profiles/m35demo/pause', {
  duration_seconds: 3600, reason: 'Homework time', client_ips: ['10.0.0.200'],
});
await page.waitForTimeout(500);

// Add a few extra pause/cancel cycles for history
for (let i = 0; i < 3; i++) {
  await apiFetch('POST', '/api/v1/profiles/m35demo/pause', {
    duration_seconds: 2, reason: `History entry ${i + 1}`,
  });
  await page.waitForTimeout(100);
  await apiFetch('DELETE', '/api/v1/profiles/m35demo/pause');
  await page.waitForTimeout(200);
}
// Re-set the per-client pause for the demo
await apiFetch('POST', '/api/v1/profiles/m35demo/pause', {
  duration_seconds: 3600, reason: 'Homework time', client_ips: ['10.0.0.200'],
});
await page.waitForTimeout(500);

console.log('\n2) Profiles list showing m35demo');
await page.goto(`${BASE}/profiles`);
await page.waitForTimeout(2000);
await shot(page, 'ss-35-01-profiles-list');

console.log('\n3) Profile pause state (per-client pause active)');
await page.goto(`${BASE}/profiles`);
await page.waitForTimeout(1500);
// Try to click into the profile
const profileRow = page.locator('text=Kids (M35 Demo)').first();
if (await profileRow.count() > 0) {
  await profileRow.click();
  await page.waitForTimeout(1500);
}
await shot(page, 'ss-35-02-profile-detail-pause-active');

console.log('\n4) Pause endpoint JSON (API demo)');
const pauseState = await apiFetch('GET', '/api/v1/profiles/m35demo/pause');
const history = await apiFetch('GET', '/api/v1/profiles/m35demo/pause/history');
const newDynamic = await apiFetch('GET', '/api/v1/clients/new-dynamic');
console.log('  Pause state:', JSON.stringify(pauseState.body, null, 2));
console.log('  History entries:', history.body?.length);
console.log('  New dynamic clients:', newDynamic.body?.length);

// Show the API response in a plain page for screenshot
await page.setContent(`<!DOCTYPE html>
<html>
<head><style>
body { background: #0f1117; color: #c9d1d9; font-family: monospace; font-size: 14px; padding: 24px; }
h2 { color: #58a6ff; margin: 0 0 8px; }
pre { background: #161b22; border: 1px solid #30363d; border-radius: 6px; padding: 16px; white-space: pre-wrap; margin: 0 0 24px; }
</style></head>
<body>
<h2>GET /api/v1/profiles/m35demo/pause</h2>
<pre>${JSON.stringify(pauseState.body, null, 2)}</pre>
<h2>GET /api/v1/profiles/m35demo/pause/history</h2>
<pre>${JSON.stringify(history.body, null, 2)}</pre>
<h2>GET /api/v1/clients/new-dynamic</h2>
<pre>${JSON.stringify(newDynamic.body, null, 2)}</pre>
</body>
</html>`);
await page.waitForTimeout(500);
await shot(page, 'ss-35-03-api-responses');

console.log('\n5) Cluster status - all nodes in_sync');
await page.goto(`${BASE}/cluster`);
await page.waitForTimeout(2000);
await shot(page, 'ss-35-04-cluster-in-sync');

// Cleanup demo profile
await apiFetch('DELETE', '/api/v1/profiles/m35demo/pause');
await apiFetch('DELETE', '/api/v1/profiles/m35demo');

await browser.close();
console.log('\nDone. Screenshots in', OUT);
