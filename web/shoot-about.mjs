// shoot-about.mjs — capture the public /about page (no auth needed).
import { chromium } from 'playwright'

const BASE = process.env.SKOED_BASE_URL ?? 'http://127.0.0.1:18399'
const OUTDIR = '../docs/screenshots'

const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()

await page.goto(BASE + '/login')
await page.evaluate(() => {
  localStorage.setItem('skoed.theme', JSON.stringify({
    palette: 'lipgloss', mode: 'dark',
  }))
})
await page.goto(BASE + '/about', { waitUntil: 'networkidle' })
await page.waitForTimeout(500)
await page.screenshot({ path: `${OUTDIR}/m5.9.6-about.png`, fullPage: true })
console.log('saved m5.9.6-about.png')

await browser.close()
