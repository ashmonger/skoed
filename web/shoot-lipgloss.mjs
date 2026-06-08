// shoot-lipgloss.mjs — capture every page in Lipgloss dark against the
// 3-node Docker cluster. Used to validate contrast + alignment of the new
// palette end-to-end.

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18080'
const USER = process.env.SKOED_USER ?? 'admin'
const PASS = process.env.SKOED_PASS ?? 'demopass123'
const OUTDIR = process.env.SKOED_SCREENSHOT_DIR ?? '../docs/screenshots'

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
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()

await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
  localStorage.setItem('skoed.theme', JSON.stringify({
    palette: 'lipgloss', mode: 'dark',
  }))
}, [USER, PASS])

for (const { path: url, name } of AUTHED_PAGES) {
  await page.goto(BASE + url, { waitUntil: 'networkidle' })
  await page.waitForTimeout(800)
  await page.screenshot({ path: `${OUTDIR}/${name}.png`, fullPage: true })
  console.log(`saved ${OUTDIR}/${name}.png`)
}

// Login (no creds)
{
  const ctx2 = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const p2 = await ctx2.newPage()
  await p2.goto(BASE + '/login')
  await p2.evaluate(() => {
    localStorage.setItem('skoed.theme', JSON.stringify({
      palette: 'lipgloss', mode: 'dark',
    }))
  })
  await p2.goto(BASE + '/login', { waitUntil: 'networkidle' })
  await p2.waitForTimeout(400)
  await p2.screenshot({ path: `${OUTDIR}/login.png`, fullPage: true })
  console.log(`saved ${OUTDIR}/login.png`)
  await ctx2.close()
}

await browser.close()
