// shoot-all.mjs — single-theme (monokai-solarized dark) screenshots of every
// authenticated route in the skoed Web UI, plus /login. Used to verify
// alignment and visual consistency across all pages in one pass.
//
// Expects a skoed node on $SKOED_BASE_URL (default http://127.0.0.1:18085)
// with admin/demopass123 already configured and seed data populated (see
// /tmp/seed-demo.sh).

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18085'
const USER = process.env.SKOED_USER ?? 'admin'
const PASS = process.env.SKOED_PASS ?? 'demopass123'
const OUTDIR = process.env.SKOED_SCREENSHOT_DIR ?? '../docs/screenshots'

// Every page the SPA exposes. Authenticated routes use the seeded session;
// the login screenshot is captured against a fresh context with no creds.
const AUTHED_PAGES = [
  { path: '/',           name: 'dashboard' },
  { path: '/blocklists', name: 'blocklists' },
  { path: '/allowlist',  name: 'allowlist' },
  { path: '/local-dns',  name: 'local-dns' },
  { path: '/profiles',   name: 'profiles' },
  { path: '/schedules',  name: 'schedules' },
  { path: '/categories', name: 'categories' },
  { path: '/query-log',  name: 'query-log' },
  { path: '/stats',      name: 'stats' },
  { path: '/cluster',    name: 'cluster' },
  { path: '/settings',   name: 'settings' },
  { path: '/account',    name: 'account' },
]

await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })

// ─── Authenticated pages ────────────────────────────────────────────────
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()

  // Land on /login (unauthed route) so we can seed storage before the
  // router's auth guard runs.
  await page.goto(BASE + '/login')
  await page.evaluate(([u, p]) => {
    sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
    localStorage.setItem('skoed.theme', JSON.stringify({
      palette: 'monokai-solarized', mode: 'dark',
    }))
  }, [USER, PASS])

  for (const { path: url, name } of AUTHED_PAGES) {
    await page.goto(BASE + url, { waitUntil: 'networkidle' })
    await page.waitForTimeout(800) // settle async fetches (stats, doh widget)
    const file = `${OUTDIR}/${name}.png`
    await page.screenshot({ path: file, fullPage: true })
    console.log(`saved ${file}`)
  }

  await ctx.close()
}

// ─── Login (no creds, monokai-solarized dark theme) ─────────────────────
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(BASE + '/login')
  await page.evaluate(() => {
    localStorage.setItem('skoed.theme', JSON.stringify({
      palette: 'monokai-solarized', mode: 'dark',
    }))
  })
  await page.goto(BASE + '/login', { waitUntil: 'networkidle' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${OUTDIR}/login.png`, fullPage: true })
  console.log(`saved ${OUTDIR}/login.png`)
  await ctx.close()
}

await browser.close()
console.log('done')
