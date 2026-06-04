import { chromium } from 'playwright'

const routes = [
  ['/login',      'login'],
  ['/',           'dashboard'],
  ['/blocklists', 'blocklists'],
  ['/allowlist',  'allowlist'],
  ['/local-dns',  'local-dns'],
  ['/query-log',  'querylog'],
  ['/stats',      'stats'],
  ['/cluster',    'cluster'],
  ['/settings',   'settings'],
  ['/account',    'account'],
]

const themes = [
  { mode: 'light', palette: 'monokai-solarized', suffix: 'light' },
  { mode: 'dark',  palette: 'monokai',           suffix: 'dark'  },
]

const baseURL = 'http://localhost:8090'
const outDir = '/home/jcollin/repos/dblock/docs/screenshots'

const browser = await chromium.launch({ headless: true })
try {
  for (const theme of themes) {
    const ctx = await browser.newContext({
      viewport: { width: 1400, height: 900 },
      baseURL,
    })
    // Prime origin: load index, then set storage same-origin, then navigate.
    const page = await ctx.newPage()
    await page.goto('/', { waitUntil: 'domcontentloaded' })
    await page.evaluate(({ mode, palette }) => {
      sessionStorage.setItem('dblock.creds', JSON.stringify({ user: 'admin', pass: 'demo1234' }))
      localStorage.setItem('dblock.theme', JSON.stringify({ mode, palette }))
    }, theme)
    for (const [route, name] of routes) {
      if (route === '/login') {
        // Login route doesn't need creds; clear them so we render the form.
        await page.evaluate(() => sessionStorage.removeItem('dblock.creds'))
      } else {
        await page.evaluate(() => {
          sessionStorage.setItem('dblock.creds', JSON.stringify({ user: 'admin', pass: 'demo1234' }))
        })
      }
      await page.goto(route, { waitUntil: 'networkidle' })
      await page.waitForTimeout(500)
      const file = `${outDir}/${name}-${theme.suffix}.png`
      await page.screenshot({ path: file, fullPage: false })
      console.log(file)
    }
    await ctx.close()
  }
} finally {
  await browser.close()
}
