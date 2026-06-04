// shoot-m3.5-dashboard.mjs — capture the Dashboard's M3.5 DoH alert.
//
// Expects a node on $DBLOCK_BASE_URL (default http://127.0.0.1:18082) with
// admin/demopass123 + a few DoH probes already emitted (see DEMO_NOTE_M3.5.md).

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.DBLOCK_BASE_URL ?? 'http://127.0.0.1:18082'
const USER = process.env.DBLOCK_USER ?? 'admin'
const PASS = process.env.DBLOCK_PASS ?? 'demopass123'
const OUTDIR = process.env.DBLOCK_SCREENSHOT_DIR ?? '../docs/screenshots'

await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await context.newPage()

await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('dblock.creds', JSON.stringify({ user: u, pass: p }))
}, [USER, PASS])

for (const { palette, mode, slug } of [
  { palette: 'monokai-solarized', mode: 'dark',  slug: 'solarized-dark' },
  { palette: 'monokai-solarized', mode: 'light', slug: 'solarized-light' },
]) {
  await page.evaluate(([pal, m]) => {
    localStorage.setItem('dblock.theme', JSON.stringify({ palette: pal, mode: m }))
  }, [palette, mode])
  await page.goto(BASE + '/', { waitUntil: 'networkidle' })
  await page.waitForTimeout(1200) // give the DoH alert a tick to load
  const file = `${OUTDIR}/m3.5-dashboard-doh-alert-${slug}.png`
  await page.screenshot({ path: file, fullPage: true })
  console.log(`saved ${file}`)
}

await browser.close()
