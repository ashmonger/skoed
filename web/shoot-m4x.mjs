// shoot-m4x.mjs — capture M4.5 + M4.7 surfaces in Lipgloss dark.
//   - /api/docs/ (Swagger UI rendering the embedded OpenAPI)
//   - /settings (showing the new DNS cache section)
//   - sidebar close-up showing the new "API" entry

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18099'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()

// Seed creds + Lipgloss dark.
await page.goto(BASE + '/login')
await page.evaluate(() => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: 'admin', pass: 'demopass123' }))
  localStorage.setItem('skoed.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
})

// M4.5: Swagger UI page. Not a SPA route — it's a separate static asset.
// Swagger UI takes a beat to fetch + render the spec.
await page.goto(BASE + '/api/docs/', { waitUntil: 'networkidle' })
await page.waitForTimeout(1500)
await page.screenshot({ path: `${OUTDIR}/m4.5-swagger-ui.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m4.5-swagger-ui.png`)

// M4.5: sidebar close-up showing the new "API" entry. Go to Settings so
// the sidebar is in view and Settings is highlighted.
await page.goto(BASE + '/dashboard/settings', { waitUntil: 'networkidle' })
await page.waitForTimeout(600)
await page.screenshot({
  path: `${OUTDIR}/m4.5-sidebar-api-entry.png`,
  clip: { x: 0, y: 0, width: 245, height: 600 },
})
console.log(`saved ${OUTDIR}/m4.5-sidebar-api-entry.png`)

// M4.7: Settings → DNS section with the new cache controls visible.
// The page is the same /settings route; just capture the upper-half so
// the DNS section + cache strip + Clear button are framed.
await page.screenshot({
  path: `${OUTDIR}/m4.7-dns-cache-controls.png`,
  clip: { x: 0, y: 0, width: 1440, height: 700 },
})
console.log(`saved ${OUTDIR}/m4.7-dns-cache-controls.png`)

await browser.close()
