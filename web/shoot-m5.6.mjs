// shoot-m5.6.mjs — capture the M5.6 in-place upgrade UI:
//   - / (Dashboard) with the upgrade-available banner at the top

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.DBLOCK_BASE_URL ?? 'http://127.0.0.1:18699'
const USER = 'admin', PASS = 'demopass123'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  httpCredentials: { username: USER, password: PASS },
})
const page = await ctx.newPage()

await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('dblock.creds', JSON.stringify({ user: u, pass: p }))
  localStorage.setItem('dblock.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
}, [USER, PASS])

await page.goto(BASE + '/', { waitUntil: 'networkidle' })
await page.waitForTimeout(800)
await page.screenshot({ path: `${OUTDIR}/m5.6-upgrade-banner.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.6-upgrade-banner.png`)

await browser.close()
