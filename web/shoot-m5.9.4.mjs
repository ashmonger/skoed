// shoot-m5.9.4.mjs — capture the M5.9.4 "Getting Started" Dashboard
// card on a fresh node (0 blocklists, 0 profiles, never dismissed).

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18995'
const USER = 'admin', PASS = 'demopass123'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  httpCredentials: { username: USER, password: PASS },
})
const page = await ctx.newPage()

// Seed creds + Lipgloss dark — same shape as shoot-m5.4 / shoot-m5.6.
await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
  localStorage.setItem('skoed.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
  // Important: ensure the Getting Started dismissal key is NOT set.
  localStorage.removeItem('skoed.gettingStarted.dismissed')
}, [USER, PASS])

// Go to the Dashboard — fresh node, no blocklists, no profiles, so
// the Getting Started card MUST be visible at the top.
await page.goto(BASE + '/dashboard', { waitUntil: 'networkidle' })
await page.waitForTimeout(800)

// Sanity check: card is in the DOM.
const card = await page.locator('text=Getting started').first()
await card.waitFor({ state: 'visible', timeout: 5000 })

await page.screenshot({ path: `${OUTDIR}/m5.9.4-getting-started-card.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.9.4-getting-started-card.png`)

await browser.close()
