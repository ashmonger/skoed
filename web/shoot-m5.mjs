// shoot-m5.mjs — capture M5.x surfaces in Lipgloss dark.
//   - /settings/audit (M5.2 audit log page, populated + filter bar visible)
//   - /settings (M5.2 audit-log link card)

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18099'
const USER = process.env.SKOED_USER ?? 'admin'
const PASS = process.env.SKOED_PASS ?? 'demopass123'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  httpCredentials: { username: USER, password: PASS },
})
const page = await ctx.newPage()

// Seed the SPA's session storage with credentials + dark Lipgloss palette.
await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
  localStorage.setItem('skoed.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
}, [USER, PASS])

// Make sure the audit log is non-empty for the screenshot. Issue a few
// state-changing API calls; auth via httpCredentials picks up Basic.
async function seedAudit() {
  const variants = [
    { path: '/api/v1/blocklists', method: 'POST', body: { id: 'screenshot-bl-1', name: 'Screenshot blocklist 1', source: { type: 'manual' } } },
    { path: '/api/v1/blocklists', method: 'POST', body: { id: 'screenshot-bl-2', name: 'Screenshot blocklist 2', source: { type: 'manual' } } },
    { path: '/api/v1/allowlist',  method: 'POST', body: { domain: 'example.com' } },
    { path: '/api/v1/local-dns',  method: 'POST', body: { hostname: 'nas.lab', type: 'A', value: '10.42.10.20', ttl: 300 } },
    { path: '/api/v1/local-dns',  method: 'POST', body: { hostname: 'printer.lab', type: 'A', value: '10.42.10.21', ttl: 300 } },
    { path: '/api/v1/blocklists', method: 'POST', body: 'not json — provokes 400' },
  ]
  for (const v of variants) {
    try {
      await page.request.fetch(BASE + v.path, {
        method: v.method,
        headers: { 'content-type': 'application/json' },
        data: typeof v.body === 'string' ? v.body : JSON.stringify(v.body),
      })
    } catch {}
  }
}
await seedAudit()

// M5.2: Audit log page, full screenshot.
await page.goto(BASE + '/settings/audit', { waitUntil: 'networkidle' })
await page.waitForTimeout(700)
await page.screenshot({ path: `${OUTDIR}/m5.2-audit-log.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.2-audit-log.png`)

// M5.2: an entry expanded — click the first row to show the diff/seq pane.
const firstRow = page.locator('table tbody tr').first()
await firstRow.click()
await page.waitForTimeout(300)
await page.screenshot({ path: `${OUTDIR}/m5.2-audit-log-expanded.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.2-audit-log-expanded.png`)

// M5.2: Settings page top, showing the audit-log link card.
await page.goto(BASE + '/settings', { waitUntil: 'networkidle' })
await page.waitForTimeout(500)
// Scroll to bottom so the new audit card is in frame.
await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight))
await page.waitForTimeout(200)
await page.screenshot({
  path: `${OUTDIR}/m5.2-settings-audit-card.png`,
  clip: { x: 0, y: 300, width: 1440, height: 600 },
})
console.log(`saved ${OUTDIR}/m5.2-settings-audit-card.png`)

await browser.close()
