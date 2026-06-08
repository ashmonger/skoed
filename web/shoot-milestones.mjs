// shoot-milestones.mjs — refresh every SPA page screenshot in
// docs/screenshots/ with the Lipgloss dark theme. Filenames are
// prefixed with the current milestone (m5.9-<page>.png) so it's
// always obvious which iteration of the SPA they capture.
//
// Expects a daemon at $DBLOCK_BASE_URL with admin/demopass123 already
// set up AND seeded with rich data (blocklists, profiles, queries).

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.DBLOCK_BASE_URL ?? 'http://127.0.0.1:18299'
const USER = process.env.DBLOCK_USER ?? 'admin'
const PASS = process.env.DBLOCK_PASS ?? 'demopass123'
const OUTDIR = process.env.DBLOCK_SCREENSHOT_DIR ?? '../docs/screenshots'
const PREFIX = process.env.DBLOCK_MILESTONE_PREFIX ?? 'm5.9'

// M5.9.5 moved the admin shell from / to /dashboard so the new
// public Landing.vue can own /. Admin routes are children of
// /dashboard now.
const AUTHED_PAGES = [
  { path: '/dashboard',                name: 'dashboard' },
  { path: '/dashboard/blocklists',     name: 'blocklists' },
  { path: '/dashboard/allowlist',      name: 'allowlist' },
  { path: '/dashboard/local-dns',      name: 'local-dns' },
  { path: '/dashboard/clients',        name: 'clients' },
  { path: '/dashboard/profiles',       name: 'profiles' },
  { path: '/dashboard/schedules',      name: 'schedules' },
  { path: '/dashboard/categories',     name: 'categories' },
  { path: '/dashboard/query-log',      name: 'query-log' },
  { path: '/dashboard/stats',          name: 'stats' },
  { path: '/dashboard/cluster',        name: 'cluster' },
  { path: '/dashboard/settings',       name: 'settings' },
  { path: '/dashboard/settings/audit', name: 'audit-log' },
  { path: '/dashboard/account',        name: 'account' },
]

await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })

// ─── Authenticated pages ────────────────────────────────────────────────
{
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    httpCredentials: { username: USER, password: PASS },
  })
  const page = await ctx.newPage()

  // Seed Lipgloss dark + Basic Auth creds before the router auth guard runs.
  await page.goto(BASE + '/login')
  await page.evaluate(([u, p]) => {
    sessionStorage.setItem('dblock.creds', JSON.stringify({ user: u, pass: p }))
    localStorage.setItem('dblock.theme', JSON.stringify({
      palette: 'lipgloss', mode: 'dark',
    }))
  }, [USER, PASS])

  for (const { path: url, name } of AUTHED_PAGES) {
    await page.goto(BASE + url, { waitUntil: 'networkidle' })
    await page.waitForTimeout(800)
    const file = `${OUTDIR}/${PREFIX}-${name}.png`
    await page.screenshot({ path: file, fullPage: true })
    console.log(`saved ${file}`)
  }

  await ctx.close()
}

// ─── Public landing page (no auth) ─────────────────────────────────────
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(BASE + '/login')
  await page.evaluate(() => {
    localStorage.setItem('dblock.theme', JSON.stringify({
      palette: 'lipgloss', mode: 'dark',
    }))
  })
  await page.goto(BASE + '/', { waitUntil: 'networkidle' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${OUTDIR}/${PREFIX}-landing-public.png`, fullPage: true })
  console.log(`saved ${OUTDIR}/${PREFIX}-landing-public.png`)
  await ctx.close()
}

// ─── Login page (no creds, Lipgloss dark) ──────────────────────────────
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(BASE + '/login')
  await page.evaluate(() => {
    localStorage.setItem('dblock.theme', JSON.stringify({
      palette: 'lipgloss', mode: 'dark',
    }))
  })
  await page.goto(BASE + '/login', { waitUntil: 'networkidle' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${OUTDIR}/${PREFIX}-login.png`, fullPage: true })
  console.log(`saved ${OUTDIR}/${PREFIX}-login.png`)
  await ctx.close()
}

await browser.close()
console.log('done')
