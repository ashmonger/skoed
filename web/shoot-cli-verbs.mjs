import { chromium } from 'playwright'

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1100, height: 1100 } })
await page.goto('file:///tmp/dblock-cli-page.html')
await page.waitForTimeout(400)
const wrap = await page.$('.wrap')
await wrap.screenshot({ path: '../docs/screenshots/m5.9.1-dblock-cli.png' })
await browser.close()
console.log('saved m5.9.1-dblock-cli.png')
