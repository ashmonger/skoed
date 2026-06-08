// shoot-cli-tui.mjs — render the static HTML at /tmp/skoed-top-page.html
// to PNG via Playwright. The HTML is produced by `skoed top --snapshot |
// aha` plus a Lipgloss-palette CSS overlay (since aha downgrades 24-bit
// to 16-color and we want the real hexes).

import { chromium } from 'playwright'

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 900, height: 700 } })
await page.goto('file:///tmp/skoed-top-page.html')
await page.waitForTimeout(400)
const wrap = await page.$('.wrap')
if (!wrap) {
  throw new Error('.wrap not found')
}
await wrap.screenshot({ path: '../docs/screenshots/m5.9.1-skoed-top.png' })
await browser.close()
console.log('saved m5.9.1-skoed-top.png')
