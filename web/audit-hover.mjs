// audit-hover.mjs — capture hover states + measure contrast for the
// three places hover styles show up: sidebar nav, table rows, ghost
// buttons. Run against the deployed cluster.

import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18080'
const USER = process.env.SKOED_USER ?? 'admin'
const PASS = process.env.SKOED_PASS ?? 'demopass123'
const OUTDIR = '../docs/audit'

await mkdir(OUTDIR, { recursive: true })

function rgbOf(str) {
  const m = str.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/)
  return m ? [parseInt(m[1]), parseInt(m[2]), parseInt(m[3])] : null
}
function lumComp(c) {
  c = c / 255
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}
function relLum(rgb) { return 0.2126*lumComp(rgb[0]) + 0.7152*lumComp(rgb[1]) + 0.0722*lumComp(rgb[2]) }
function ratio(a, b) {
  const la = relLum(a), lb = relLum(b)
  const [hi, lo] = la > lb ? [la, lb] : [lb, la]
  return (hi + 0.05) / (lo + 0.05)
}

const COMBOS = []
for (const palette of ['monokai-solarized', 'monokai', 'monokai-blue', 'monokai-pro', 'lipgloss']) {
  for (const mode of ['light', 'dark']) {
    COMBOS.push({ palette, mode })
  }
}

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()

await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
}, [USER, PASS])

const report = []
for (const { palette, mode } of COMBOS) {
  await page.evaluate(([pal, m]) => {
    localStorage.setItem('skoed.theme', JSON.stringify({ palette: pal, mode: m }))
  }, [palette, mode])

  // Use blocklists page — table + nav + buttons all present
  await page.goto(BASE + '/blocklists', { waitUntil: 'networkidle' })
  await page.waitForTimeout(500)

  // ── 1. Hover a sidebar nav link (Allowlist, not the active one) ──
  const navTarget = page.locator('aside a:has-text("Allowlist")').first()
  await navTarget.hover()
  await page.waitForTimeout(150)
  const navInfo = await page.evaluate(() => {
    const el = Array.from(document.querySelectorAll('aside a')).find(a => a.textContent.trim().includes('Allowlist'))
    if (!el) return null
    const cs = getComputedStyle(el)
    return { bg: cs.backgroundColor, fg: cs.color }
  })

  // ── 2. Hover a table row ──
  const rowTarget = page.locator('table tbody tr').first()
  let rowInfo = null
  if (await rowTarget.count() > 0) {
    await rowTarget.hover()
    await page.waitForTimeout(150)
    rowInfo = await page.evaluate(() => {
      const row = document.querySelector('table tbody tr')
      if (!row) return null
      const cs = getComputedStyle(row)
      const td = row.querySelector('td')
      const tdCs = td ? getComputedStyle(td) : null
      return { bg: cs.backgroundColor, fg: tdCs?.color ?? cs.color }
    })
  }

  // ── 3. Take a full screenshot with mouse over a non-active nav row ──
  await navTarget.hover()
  await page.waitForTimeout(300)
  await page.screenshot({
    path: `${OUTDIR}/hover-${palette}-${mode}.png`,
    clip: { x: 0, y: 50, width: 300, height: 450 },
  })

  // ── Compute ratios ──
  const navBg = rgbOf(navInfo?.bg)
  const navFg = rgbOf(navInfo?.fg)
  const rowBg = rgbOf(rowInfo?.bg)
  const rowFg = rgbOf(rowInfo?.fg)
  // Compare hover bg to surrounding bg-card (visual distinguishability)
  const cardBg = await page.evaluate(() => {
    const card = document.querySelector('aside')
    return card ? getComputedStyle(card).backgroundColor : null
  })
  const cardRgb = rgbOf(cardBg)

  const row = {
    palette, mode,
    navHover: navBg && navFg ? {
      bg: navBg, fg: navFg,
      textRatio: ratio(navBg, navFg).toFixed(2),
      distinguishability: cardRgb ? ratio(navBg, cardRgb).toFixed(2) : 'n/a',
    } : null,
    rowHover: rowBg && rowFg ? {
      bg: rowBg, fg: rowFg,
      textRatio: ratio(rowBg, rowFg).toFixed(2),
    } : null,
  }
  report.push(row)
}

await browser.close()

console.log('\n=== Hover audit ===')
console.log('  text ratio: contrast of hovered text vs hovered bg (≥4.5 normal)')
console.log('  distinguishability: how much the hover bg differs from sidebar bg (≥1.3 visible)\n')
for (const r of report) {
  console.log(`\n--- ${r.palette} / ${r.mode} ---`)
  if (r.navHover) {
    const t = parseFloat(r.navHover.textRatio)
    const d = parseFloat(r.navHover.distinguishability)
    const tStatus = t >= 4.5 ? '✓' : t >= 3 ? '!' : '✗'
    const dStatus = d >= 1.3 ? '✓' : d >= 1.15 ? '!' : '✗'
    console.log(`  nav hover  text=${tStatus}${r.navHover.textRatio.padStart(5)} distinguish=${dStatus}${r.navHover.distinguishability.padStart(5)}  fg=rgb(${r.navHover.fg.join(',')}) bg=rgb(${r.navHover.bg.join(',')})`)
  }
  if (r.rowHover) {
    const t = parseFloat(r.rowHover.textRatio)
    const tStatus = t >= 4.5 ? '✓' : t >= 3 ? '!' : '✗'
    console.log(`  row hover  text=${tStatus}${r.rowHover.textRatio.padStart(5)}                       fg=rgb(${r.rowHover.fg.join(',')}) bg=rgb(${r.rowHover.bg.join(',')})`)
  }
}
