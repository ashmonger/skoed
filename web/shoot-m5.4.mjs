// shoot-m5.4.mjs — capture the M5.4 automated-blocklist-refresh UI:
//   - /blocklists (table with the new "Auto-refresh" column populated)
//   - /blocklists with the "New blocklist" modal open (auto-refresh field)
//   - / (Dashboard) with a stale-blocklist warning card visible

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18199'
const USER = 'admin', PASS = 'demopass123'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  httpCredentials: { username: USER, password: PASS },
})
const page = await ctx.newPage()

// Seed creds + Lipgloss dark.
await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
  localStorage.setItem('skoed.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
}, [USER, PASS])

// Seed three URL-source blocklists with different refresh shapes:
//   - hagezi: realistic 24h interval, will show as "pending"
//   - tracking: short interval (2s) so the refresh worker fires; refresh shows OK
//   - stale: short interval + bogus URL → stale + error state for the Dashboard alert
async function postBL(body) {
  const resp = await page.request.fetch(BASE + '/api/v1/blocklists', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    data: JSON.stringify(body),
  })
  if (!resp.ok()) console.log('POST blocklist:', resp.status(), await resp.text())
}
await postBL({
  id: 'hagezi-pro', name: 'Hagezi Pro',
  source: { type: 'url', url: 'http://127.0.0.1:18199/static/never-fetched.txt', format: 'hosts' },
  refresh_interval_seconds: 86400, // 24h
})
await postBL({
  id: 'tracking', name: 'Tracking',
  source: { type: 'url', url: 'http://127.0.0.1:18199/api/v1/health', format: 'hosts' },
  refresh_interval_seconds: 5,
})
await postBL({
  id: 'stale-feed', name: 'Stale feed',
  source: { type: 'url', url: 'http://127.0.0.1:18199/api/v1/health', format: 'hosts' },
  refresh_interval_seconds: 2,
})
// Wait long enough for at least one auto-refresh tick + a stale-window pass.
await page.waitForTimeout(8000)

await page.goto(BASE + '/dashboard/blocklists', { waitUntil: 'networkidle' })
await page.waitForTimeout(500)
await page.screenshot({ path: `${OUTDIR}/m5.4-blocklists-table.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.4-blocklists-table.png`)

// Open the create modal so the new "Auto-refresh interval" field is visible.
await page.click('button:has-text("New blocklist")')
await page.waitForTimeout(300)
// Pre-fill so the modal is realistic.
await page.fill('input#bl-name', 'Demo blocklist')
await page.fill('input#bl-url', 'https://example.com/hosts.txt')
await page.fill('input#bl-refresh', '86400')
await page.waitForTimeout(200)
await page.screenshot({ path: `${OUTDIR}/m5.4-create-modal.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.4-create-modal.png`)

// Dashboard — the stale-feed blocklist's interval = 2s, so > 2× window
// already passed; the alert card should be visible at the top.
await page.goto(BASE + '/', { waitUntil: 'networkidle' })
await page.waitForTimeout(800)
await page.screenshot({ path: `${OUTDIR}/m5.4-dashboard-stale-alert.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.4-dashboard-stale-alert.png`)

await browser.close()
