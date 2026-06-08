// Render design/social-preview.html → design/social-preview-1280x640.png.
// Drop the PNG into GitHub Settings → Social preview when ready.
import { chromium } from 'playwright'

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 640 } })
await page.goto('file://' + process.cwd() + '/design/social-preview.html')
await page.waitForTimeout(300)
await page.screenshot({ path: 'design/social-preview-1280x640.png', clip: { x: 0, y: 0, width: 1280, height: 640 } })
await browser.close()
console.log('saved design/social-preview-1280x640.png')
