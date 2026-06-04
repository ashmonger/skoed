// shoot-m3-ui.mjs — capture M3 Web UI screenshots.
//
// Usage: assumes a dblock node is running on $DBLOCK_BASE_URL (default
// http://127.0.0.1:18080) with HTTP Basic credentials admin/demopass123
// already configured. Writes PNGs to docs/screenshots/.
//
// Seeds the SPA's sessionStorage (creds) + localStorage (theme) by
// scripting them on the /login page so the SPA bootstraps logged-in and
// in the chosen palette/mode.

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.DBLOCK_BASE_URL ?? 'http://127.0.0.1:18080'
const USER = process.env.DBLOCK_USER ?? 'admin'
const PASS = process.env.DBLOCK_PASS ?? 'demopass123'
// Default to the repo-root docs/screenshots/ (this script runs from web/).
const OUTDIR = process.env.DBLOCK_SCREENSHOT_DIR ?? '../docs/screenshots'

const PAGES = [
  { path: '/profiles',   name: 'profiles' },
  { path: '/schedules',  name: 'schedules' },
  { path: '/categories', name: 'categories' },
  { path: '/stats',      name: 'stats-doh' },
]

// Two illustrative theme variants; the existing M2.6/M3 screenshot sets
// already cover all four palettes × light/dark on /cluster.
const PALETTES = [
  { palette: 'monokai-solarized', mode: 'dark',  slug: 'solarized-dark' },
  { palette: 'monokai-solarized', mode: 'light', slug: 'solarized-light' },
]

await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
})
const page = await context.newPage()

// Land on /login first (unauthenticated route) so we can write to
// sessionStorage / localStorage before the auth guard runs.
await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('dblock.creds', JSON.stringify({ user: u, pass: p }))
}, [USER, PASS])

for (const { palette, mode, slug } of PALETTES) {
  await page.evaluate(([pal, m]) => {
    localStorage.setItem('dblock.theme', JSON.stringify({ palette: pal, mode: m }))
  }, [palette, mode])

  for (const { path: url, name } of PAGES) {
    await page.goto(BASE + url, { waitUntil: 'networkidle' })
    await page.waitForTimeout(500)
    const file = `${OUTDIR}/m3ui-${name}-${slug}.png`
    await page.screenshot({ path: file, fullPage: true })
    console.log(`saved ${file}`)
  }
}

await browser.close()
console.log('done')
