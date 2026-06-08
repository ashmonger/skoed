// shoot-m5.9.7.mjs — capture the M5.9.7 "Would this domain be blocked?"
// surfaces:
//
//   docs/screenshots/m5.9.7-landing-domain-card.png
//     The unauthenticated landing page with the domain-tester card
//     showing a blocked verdict for "doubleclick.net".
//
//   docs/screenshots/m5.9.7-test-domain-tool.png
//     The /dashboard/tools/test-domain admin tool with the full
//     reasoning chain (matched profile, matched blocklist, block policy).
//
// Expects:
//   - A skoed daemon listening on $SKOED_BASE_URL (default 127.0.0.1:18499).
//   - The leader has a profile that owns a blocklist matching doubleclick.net.
//     The orchestrator seeds that. The standalone bootstrap below does too.
//   - Credentials at SKOED_AUTH_USER / SKOED_AUTH_PASS (default admin/admin12345).
//
// Run via scripts/shoot-all.sh (orchestrated) or scripts/shoot-m5.9.7.sh
// (standalone, boots a one-shot daemon).

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18499'
// Default matches scripts/shoot-all.sh's seeded admin password.
const USER = process.env.SKOED_AUTH_USER ?? 'admin'
const PASS = process.env.SKOED_AUTH_PASS ?? 'demopass123'
const DOMAIN = process.env.SKOED_TEST_DOMAIN ?? 'doubleclick.net'
const OUTDIR = '../docs/screenshots'
await mkdir(OUTDIR, { recursive: true })

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1280, height: 820 } })
const page = await ctx.newPage()

// Set the same dark "lipgloss" theme the other m5.9-* screenshots use.
await page.goto(BASE + '/login')
await page.evaluate(() => {
  localStorage.setItem('skoed.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
})

// ── 1. Landing page, guest verdict ───────────────────────────────────
await page.goto(BASE + '/', { waitUntil: 'networkidle' })

await page.fill('[data-testid="domain-input"]', DOMAIN)
await page.click('[data-testid="domain-test-btn"]')
await page.waitForSelector('[data-testid="domain-tester-result"]', { timeout: 5000 })
await page.waitForTimeout(300) // settle styles

await page.screenshot({ path: `${OUTDIR}/m5.9.7-landing-domain-card.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.9.7-landing-domain-card.png`)

// ── 2. Admin tool, full reasoning chain ──────────────────────────────
// Authenticate by writing credentials into sessionStorage the way the
// SPA does, then navigate. (The other authed shoot scripts use the
// same trick — avoids submitting the login form.)
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
}, [USER, PASS])

await page.goto(BASE + '/dashboard/tools/test-domain', { waitUntil: 'networkidle' })

await page.fill('[data-testid="test-domain-input"]', DOMAIN)
await page.click('[data-testid="test-domain-submit"]')
await page.waitForSelector('[data-testid="test-domain-result"]', { timeout: 5000 })
await page.waitForTimeout(400) // settle styles + chip transitions

await page.screenshot({ path: `${OUTDIR}/m5.9.7-test-domain-tool.png`, fullPage: false })
console.log(`saved ${OUTDIR}/m5.9.7-test-domain-tool.png`)

await browser.close()
