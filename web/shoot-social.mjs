// Render design/social-preview.html → design/social-preview-1280x640.png.
// Run from web/ (where playwright lives). Drop the PNG into GitHub
// Settings → Social preview.
import { chromium } from 'playwright'
import { resolve } from 'node:path'

const ROOT = resolve(process.cwd(), '..')
const HTML = 'file://' + ROOT + '/design/social-preview.html'
const OUT  = ROOT + '/design/social-preview-1280x640.png'

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 640 } })
await page.goto(HTML)
await page.waitForTimeout(300)
await page.screenshot({ path: OUT, clip: { x: 0, y: 0, width: 1280, height: 640 } })
await browser.close()
console.log('saved', OUT)
