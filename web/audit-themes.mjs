// audit-themes.mjs — visual + programmatic contrast audit across every
// palette × mode combo. Captures Dashboard screenshots for each combo and
// computes WCAG contrast ratios for the canonical text/bg token pairs.

import { chromium } from 'playwright'
import { mkdir, writeFile } from 'node:fs/promises'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18080'
const USER = process.env.SKOED_USER ?? 'admin'
const PASS = process.env.SKOED_PASS ?? 'demopass123'
const OUTDIR = '../docs/audit'

await mkdir(OUTDIR, { recursive: true })

// All palette × mode combos. monokai-solarized has no dedicated light variant
// (it inherits the Tailwind compile-time default), but we include it anyway
// so we can see how it renders.
const COMBOS = []
for (const palette of ['monokai-solarized', 'monokai', 'monokai-blue', 'monokai-pro', 'lipgloss']) {
  for (const mode of ['light', 'dark']) {
    COMBOS.push({ palette, mode, slug: `${palette}-${mode}` })
  }
}

// Canonical token pairs we want to verify. The pair "text-X on bg-Y" must
// render with sufficient contrast or it's an accessibility bug.
const TOKEN_PAIRS = [
  // Body text on canvas
  { fg: 'text-fg',         bg: 'bg-bg-canvas', label: 'body text' },
  { fg: 'text-fg-strong',  bg: 'bg-bg-canvas', label: 'headings' },
  { fg: 'text-fg-muted',   bg: 'bg-bg-canvas', label: 'muted text' },
  { fg: 'text-fg-subtle',  bg: 'bg-bg-canvas', label: 'subtle text' },
  // Text on cards
  { fg: 'text-fg',         bg: 'bg-bg-card',   label: 'body in card' },
  { fg: 'text-fg-strong',  bg: 'bg-bg-card',   label: 'heading in card' },
  { fg: 'text-fg-muted',   bg: 'bg-bg-card',   label: 'muted in card' },
  // Accent + status colors on canvas/card
  { fg: 'text-accent',     bg: 'bg-bg-card',   label: 'accent link' },
  { fg: 'text-success',    bg: 'bg-bg-card',   label: 'success text' },
  { fg: 'text-warning',    bg: 'bg-bg-card',   label: 'warning text' },
  { fg: 'text-danger',     bg: 'bg-bg-card',   label: 'danger text' },
  // Active nav (the issue the user spotted): text-accent on bg-accent-subtle
  { fg: 'text-accent',     bg: 'bg-accent-subtle', label: 'active nav' },
  // Filled badges/buttons
  { fg: null,              bg: 'bg-accent',    label: 'primary btn (auto fg)' },
  { fg: null,              bg: 'bg-success',   label: 'success badge (auto fg)' },
  { fg: null,              bg: 'bg-danger',    label: 'danger badge (auto fg)' },
]

function srgbToLinear(c) {
  c = c / 255
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}
function relativeLuminance(rgb) {
  return 0.2126 * srgbToLinear(rgb[0]) + 0.7152 * srgbToLinear(rgb[1]) + 0.0722 * srgbToLinear(rgb[2])
}
function contrastRatio(a, b) {
  const la = relativeLuminance(a), lb = relativeLuminance(b)
  const [hi, lo] = la > lb ? [la, lb] : [lb, la]
  return (hi + 0.05) / (lo + 0.05)
}

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()

// One-time login
await page.goto(BASE + '/login')
await page.evaluate(([u, p]) => {
  sessionStorage.setItem('skoed.creds', JSON.stringify({ user: u, pass: p }))
}, [USER, PASS])

const report = []

for (const { palette, mode, slug } of COMBOS) {
  await page.evaluate(([pal, m]) => {
    localStorage.setItem('skoed.theme', JSON.stringify({ palette: pal, mode: m }))
  }, [palette, mode])

  await page.goto(BASE + '/', { waitUntil: 'networkidle' })
  await page.waitForTimeout(500)
  await page.screenshot({ path: `${OUTDIR}/dashboard-${slug}.png`, fullPage: false })

  // Compute color pairs by injecting probe elements and reading their styles.
  const ratios = await page.evaluate((pairs) => {
    function rgbOf(str) {
      const m = str.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/)
      if (!m) return null
      return [parseInt(m[1]), parseInt(m[2]), parseInt(m[3])]
    }
    const root = document.body
    const probe = document.createElement('div')
    probe.style.position = 'fixed'; probe.style.visibility = 'hidden'
    probe.style.left = '-9999px'
    root.appendChild(probe)
    const out = []
    for (const p of pairs) {
      const bgEl = document.createElement('div')
      bgEl.className = p.bg
      probe.appendChild(bgEl)
      const bgRGB = rgbOf(getComputedStyle(bgEl).backgroundColor)
      let fgRGB = null
      if (p.fg) {
        const fgEl = document.createElement('span')
        fgEl.className = p.fg
        bgEl.appendChild(fgEl)
        fgRGB = rgbOf(getComputedStyle(fgEl).color)
      } else {
        // For bg-accent/bg-success/bg-danger, the rule itself sets color
        fgRGB = rgbOf(getComputedStyle(bgEl).color)
      }
      out.push({ label: p.label, bgRGB, fgRGB })
      probe.removeChild(bgEl)
    }
    root.removeChild(probe)
    return out
  }, TOKEN_PAIRS)

  const comboReport = { palette, mode, pairs: [] }
  for (const r of ratios) {
    if (!r.bgRGB || !r.fgRGB) {
      comboReport.pairs.push({ ...r, ratio: null, status: 'unknown' })
      continue
    }
    const ratio = contrastRatio(r.bgRGB, r.fgRGB)
    let status = 'pass'
    if (ratio < 3.0) status = 'FAIL'
    else if (ratio < 4.5) status = 'low'
    comboReport.pairs.push({ ...r, ratio: ratio.toFixed(2), status })
  }
  report.push(comboReport)
}

await browser.close()

// Print a flat table.
console.log('\n=== Contrast audit ===')
console.log('  WCAG: ≥4.5 normal text, ≥3.0 large text/UI components, ≥7 enhanced')
console.log('  status: pass (≥4.5)  low (3.0–4.5)  FAIL (<3.0)\n')
for (const c of report) {
  console.log(`\n--- ${c.palette} / ${c.mode} ---`)
  for (const p of c.pairs) {
    const tag = p.status === 'FAIL' ? '✗ FAIL' : p.status === 'low' ? '! low ' : '✓ pass'
    const bg = p.bgRGB ? `rgb(${p.bgRGB.join(',')})` : '???'
    const fg = p.fgRGB ? `rgb(${p.fgRGB.join(',')})` : '???'
    console.log(`  ${tag}  ${p.ratio ?? ' n/a'}  ${p.label.padEnd(28)} fg ${fg.padEnd(20)} bg ${bg}`)
  }
}

await writeFile(`${OUTDIR}/contrast-report.json`, JSON.stringify(report, null, 2))
console.log(`\nJSON: ${OUTDIR}/contrast-report.json`)
