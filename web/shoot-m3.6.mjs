// shoot-m3.6.mjs — capture the new Clients page + Dashboard (with the
// spoof alert) + Profiles edit modal showing the DHCP-identity section.

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.DBLOCK_BASE_URL ?? 'http://127.0.0.1:18087'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()

await page.goto(BASE + '/login')
await page.evaluate(() => {
  sessionStorage.setItem('dblock.creds', JSON.stringify({ user: 'admin', pass: 'demopass123' }))
  localStorage.setItem('dblock.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
})

for (const { path, name } of [
  { path: '/clients', name: 'clients' },
  { path: '/',        name: 'dashboard' },  // dashboard with spoof alert
]) {
  await page.goto(BASE + path, { waitUntil: 'networkidle' })
  await page.waitForTimeout(800)
  await page.screenshot({ path: `${OUTDIR}/m3.6-${name}.png`, fullPage: true })
  console.log(`saved ${OUTDIR}/m3.6-${name}.png`)
}

// Profiles edit modal with DHCP identity section expanded.
await page.goto(BASE + '/profiles', { waitUntil: 'networkidle' })
await page.waitForTimeout(400)
await page.click('button:has-text("New profile")', { timeout: 5000 })
await page.waitForTimeout(300)
await page.click('summary:has-text("DHCP-stable identity")', { timeout: 5000 })
await page.waitForTimeout(300)
await page.screenshot({ path: `${OUTDIR}/m3.6-profile-identity.png`, fullPage: true })
console.log(`saved ${OUTDIR}/m3.6-profile-identity.png`)

await browser.close()
