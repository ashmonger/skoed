import { chromium } from 'playwright'
const browser = await chromium.launch({ headless: true })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()
await page.goto('http://127.0.0.1:18080/login')
await page.evaluate(() => {
  sessionStorage.setItem('dblock.creds', JSON.stringify({ user: 'admin', pass: 'demopass123' }))
  localStorage.setItem('dblock.theme', JSON.stringify({ palette: 'lipgloss', mode: 'dark' }))
})
await page.goto('http://127.0.0.1:18080/schedules', { waitUntil: 'networkidle' })
await page.waitForTimeout(600)
const info = await page.evaluate(() => {
  const link = document.querySelector('aside a[class*="bg-accent-subtle"]')
  if (!link) return { error: 'no active link found' }
  const cs = getComputedStyle(link)
  return { bg: cs.backgroundColor, color: cs.color, classes: link.className, html_class: document.documentElement.className, palette: document.documentElement.dataset.palette }
})
console.log(JSON.stringify(info, null, 2))
await page.screenshot({ path: '/tmp/nav-check.png', clip: { x: 0, y: 50, width: 250, height: 300 } })
await browser.close()
