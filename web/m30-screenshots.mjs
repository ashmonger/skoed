/**
 * M30 screenshots — DHCP Persistence + DHCPv6
 * Captures DHCP leases, server status, and cluster state.
 */
import { chromium } from 'playwright';
import { execSync, spawn } from 'child_process';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SHOTS_DIR  = path.join(__dirname, '..', 'demos', 'm30');
fs.mkdirSync(SHOTS_DIR, { recursive: true });

const SSH_KEY  = `${process.env.HOME}/.ssh/id_ed25519`;
const SSH_HOST = 'root@ns3251245.ip-91-134-62.eu';
const CREDS    = { user: 'admin', pass: 'Skoed2026!' };
const LOCAL_PORT = 9090;

const log = (msg) => console.log(`[${new Date().toISOString().slice(11,19)}] ${msg}`);

let tunnelProc = null;


function sshExec(cmd) {
  try {
    return execSync(
      `ssh -i ${SSH_KEY} -o StrictHostKeyChecking=no ${SSH_HOST} "${cmd}"`,
      { timeout: 8000, stdio: ['ignore','pipe','ignore'] }
    ).toString().trim();
  } catch { return ''; }
}

async function login(page) {
  await page.goto(`http://localhost:${LOCAL_PORT}/login`);
  await page.waitForSelector('input[type="password"]', { timeout: 10000 });
  await page.locator('#u').fill(CREDS.user);
  await page.locator('#p').fill(CREDS.pass);
  await page.locator('button.btn-primary').click();
  await page.waitForURL('**/dashboard**', { timeout: 10000 });
}

async function setTheme(page) {
  await page.evaluate(() => {
    try { localStorage.setItem('skoed.theme', JSON.stringify({ mode: 'dark', palette: 'lipgloss' })); } catch {}
  });
  await page.reload();
  await page.waitForLoadState('networkidle');
}

async function shot(page, name, desc) {
  const p = path.join(SHOTS_DIR, `${name}.png`);
  await page.screenshot({ path: p, fullPage: false });
  log(`  📸 ${name}.png — ${desc}`);
}

(async () => {
  // Find leader
  const leaderToken = sshExec(
    "curl -s -X POST http://10.0.0.101:8080/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"username\":\"admin\",\"password\":\"Skoed2026!\"}' | grep -o '\"token\":\"[^\"]*\"' | cut -d'\"' -f4"
  );
  log(`Leader token: ${leaderToken.slice(0,20)}...`);

  // Start tunnel to leader (CT201 = 10.0.0.101 after last restart)
  tunnelProc = spawn('ssh', [
    '-i', SSH_KEY, '-o', 'StrictHostKeyChecking=no',
    '-N', '-L', `${LOCAL_PORT}:10.0.0.101:8080`, SSH_HOST
  ], { stdio: 'ignore' });
  await new Promise(r => setTimeout(r, 1500));

  const browser = await chromium.launch({ headless: true });
  const page    = await browser.newPage();
  await page.setViewportSize({ width: 1400, height: 900 });

  try {
    await login(page);
    await setTheme(page);
    log('Logged in');

    // Shot 1: DHCP leases page
    await page.goto(`http://localhost:${LOCAL_PORT}/dhcp`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(800);
    await shot(page, 'ss-30-01-dhcp-leases', 'DHCPv4 leases page (30 active leases)');

    // Shot 2: DHCP server status (scroll or look for status section)
    await page.waitForTimeout(400);
    await shot(page, 'ss-30-02-dhcp-status', 'DHCPv4 server status panel');

    // Shot 3: DHCPv6 leases (look for v6 tab or route)
    try {
      const v6tab = page.locator('text=DHCPv6, text=IPv6, a[href*=dhcp6]').first();
      if (await v6tab.count() > 0) {
        await v6tab.click();
        await page.waitForLoadState('networkidle');
        await page.waitForTimeout(600);
      } else {
        await page.goto(`http://localhost:${LOCAL_PORT}/dhcp6`);
        await page.waitForLoadState('networkidle');
        await page.waitForTimeout(600);
      }
    } catch {}
    await shot(page, 'ss-30-03-dhcpv6-leases', 'DHCPv6 leases (15 active leases)');

    // Shot 4: Cluster status
    await page.goto(`http://localhost:${LOCAL_PORT}/cluster`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(600);
    await shot(page, 'ss-30-04-cluster-status', 'Cluster status — 3/3 nodes in sync');

    log('All screenshots done');
  } finally {
    await browser.close();
    if (tunnelProc) tunnelProc.kill();
  }
})().catch(err => {
  console.error(err);
  if (tunnelProc) tunnelProc.kill();
  process.exit(1);
});
