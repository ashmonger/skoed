// shoot-html.mjs — generic "render HTML file → PNG of .wrap" helper.
// Used by scripts/shoot-all.sh to turn aha-rendered ANSI into a styled
// PNG for the CLI verbs + TUI snapshot.
//
// Usage:
//   HTML=/tmp/page.html OUT=../docs/screenshots/foo.png W=1100 H=1100 \
//     node shoot-html.mjs
import { chromium } from 'playwright'

const html = process.env.HTML
const out  = process.env.OUT
const W    = Number(process.env.W || 1100)
const H    = Number(process.env.H || 900)
if (!html || !out) {
  console.error('HTML and OUT env vars are required')
  process.exit(2)
}
const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: W, height: H } })
await page.goto('file://' + html)
await page.waitForTimeout(400)
const wrap = await page.$('.wrap')
if (!wrap) throw new Error('.wrap not found in ' + html)
await wrap.screenshot({ path: out })
await browser.close()
console.log('saved', out)
