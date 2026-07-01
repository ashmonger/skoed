import { chromium } from 'playwright';
import { mkdirSync } from 'fs';

const OUT = '/root/repos/dblock/demos/m355';
mkdirSync(OUT, { recursive: true });

const BASE = 'http://10.0.0.101:8080';
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

// ─── Pre-populate test data via API ──────────────────────────────────────────
const token = await page.evaluate(async ([u, p]) => {
  const r = await fetch('/api/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: u, password: p }),
  });
  const d = await r.json();
  return d.token;
}, [USER, PASS]);

const api = (method, path, body) => page.evaluate(async ([method, path, body, tok]) => {
  const r = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${tok}` },
    body: body ? JSON.stringify(body) : undefined,
  });
  return r.json().catch(() => null);
}, [method, path, body, token]);

// Ensure kids profile exists
const profiles = await api('GET', '/api/v1/profiles');
if (!profiles.find(p => p.id === 'kids')) {
  await api('POST', '/api/v1/profiles', { id: 'kids', name: 'Kids' });
}

// Ensure adults profile exists
if (!profiles.find(p => p.id === 'adults')) {
  await api('POST', '/api/v1/profiles', { id: 'adults', name: 'Adults' });
}

// Create a few demo devices
const existingDevs = await api('GET', '/api/v1/devices');
const existingNames = existingDevs.map(d => d.name);

if (!existingNames.includes('kids-laptop')) {
  await api('POST', '/api/v1/devices', {
    name: 'kids-laptop',
    profile_id: 'kids',
    macs: ['aa:bb:cc:dd:ee:01'],
    ips: ['192.168.1.20'],
    hostnames: ['kids-laptop.lan'],
  });
}
if (!existingNames.includes('smart-tv')) {
  await api('POST', '/api/v1/devices', {
    name: 'smart-tv',
    profile_id: 'adults',
    macs: ['bb:cc:dd:ee:ff:01'],
    ips: ['192.168.1.30'],
  });
}
if (!existingNames.includes('nas-server')) {
  await api('POST', '/api/v1/devices', {
    name: 'nas-server',
    profile_id: 'adults',
    macs: ['cc:dd:ee:ff:00:01', 'cc:dd:ee:ff:00:02'],
    ips: ['192.168.1.50'],
    hostnames: ['nas.lan'],
  });
}

// ─── Screenshots ─────────────────────────────────────────────────────────────

console.log('\n2) Devices list view');
await page.goto(`${BASE}`);
await page.waitForTimeout(1500);
// Click "Devices" in the sidebar nav
await page.getByRole('link', { name: 'Devices' }).click();
await page.waitForTimeout(2000);
await shot(page, 'ss-355-01-devices-list');

console.log('\n3) Register new device side panel');
await page.getByRole('button', { name: 'Register device' }).click();
await page.waitForTimeout(800);
// Fill in the form
await page.locator('input[placeholder="workstation-01"]').fill('workstation-01');
const profileSelect = page.locator('select');
await profileSelect.selectOption({ label: 'Kids' }).catch(() => profileSelect.selectOption({ index: 1 }));
await page.locator('textarea').first().fill('dd:ee:ff:00:11:22');
await page.locator('textarea').nth(1).fill('192.168.1.100');
await page.waitForTimeout(500);
await shot(page, 'ss-355-02-register-device-panel');

// Close the panel via Cancel button
await page.getByRole('button', { name: 'Cancel' }).click();
await page.waitForTimeout(800);

console.log('\n4) Edit existing device');
// Click first edit button in the table
await page.locator('button[title="Edit"]').first().click({ force: true });
await page.waitForTimeout(1000);
await shot(page, 'ss-355-03-edit-device-panel');
// Close via Cancel
await page.getByRole('button', { name: 'Cancel' }).click();
await page.waitForTimeout(500);

console.log('\n5) Devices list with search filter');
await page.goto(`${BASE}`);
await page.waitForTimeout(1000);
await page.getByRole('link', { name: 'Devices' }).click();
await page.waitForTimeout(1500);
await page.locator('input[placeholder*="Filter"]').fill('nas');
await page.waitForTimeout(500);
await shot(page, 'ss-355-04-devices-search');

await browser.close();
console.log('\nDone.');
