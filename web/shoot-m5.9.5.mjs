// shoot-m5.9.5.mjs — capture the M5.9.5 public landing page WITH a
// successful URL-test result displayed.
//
// Expects:
//   - A dblock daemon listening on $DBLOCK_BASE_URL (default 127.0.0.1:18995),
//     started with DBLOCK_PUBLIC_TESTER_ALLOW_PRIVATE=1 so the SSRF guard
//     accepts the local http server below.
//   - A local hosts-format file at http://127.0.0.1:19595/hosts.txt
//     (the demo setup uses a `python3 -m http.server` inside the data dir).
//
// No auth — the landing page is the one unauthenticated surface.

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.DBLOCK_BASE_URL ?? 'http://127.0.0.1:18995'
const HOSTS_URL = process.env.DBLOCK_TEST_URL ?? 'http://127.0.0.1:19595/hosts.txt'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1280, height: 820 } })
const page = await ctx.newPage()

await page.goto(BASE + '/login')
await page.evaluate(() => {
  localStorage.setItem('dblock.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
})

await page.goto(BASE + '/', { waitUntil: 'networkidle' })

// Fill in the URL tester, submit, wait for the success card.
await page.fill('[data-testid="url-input"]', HOSTS_URL)
await page.selectOption('[data-testid="format-select"]', 'hosts')
await page.click('[data-testid="test-btn"]')
await page.waitForSelector('[data-testid="url-tester-result"]', { timeout: 5000 })
await page.waitForTimeout(300) // settle styles

await page.screenshot({ path: `${OUTDIR}/m5.9.5-landing.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.9.5-landing.png`)

await browser.close()
