import { chromium } from 'playwright';

const BASE = 'http://localhost:8091';
const CREDS = { user: 'admin', pass: 'demo1234' };

const shots = [
  { palette: 'monokai-solarized', mode: 'light', out: '../docs/screenshots/cluster-solarized-light.png' },
  { palette: 'monokai-solarized', mode: 'dark',  out: '../docs/screenshots/cluster-solarized-dark.png'  },
  { palette: 'monokai',           mode: 'light', out: '../docs/screenshots/cluster-monokai-light.png'   },
  { palette: 'monokai',           mode: 'dark',  out: '../docs/screenshots/cluster-monokai-dark.png'    },
  { palette: 'monokai-blue',      mode: 'light', out: '../docs/screenshots/cluster-blue-light.png'      },
  { palette: 'monokai-blue',      mode: 'dark',  out: '../docs/screenshots/cluster-blue-dark.png'       },
  { palette: 'monokai-pro',       mode: 'light', out: '../docs/screenshots/cluster-pro-light.png'       },
  { palette: 'monokai-pro',       mode: 'dark',  out: '../docs/screenshots/cluster-pro-dark.png'        },
];

const browser = await chromium.launch({ headless: true });

try {
  for (const { palette, mode, out } of shots) {
    const context = await browser.newContext({ viewport: { width: 1400, height: 900 } });
    const page = await context.newPage();

    const r1 = await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' });
    if (!r1 || !r1.ok()) {
      throw new Error(`goto / failed for ${palette}/${mode}: status=${r1 ? r1.status() : 'null'}`);
    }

    await page.evaluate(({ mode, palette, creds }) => {
      sessionStorage.setItem('skoed.creds', JSON.stringify(creds));
      localStorage.setItem('skoed.theme', JSON.stringify({ mode, palette }));
    }, { mode, palette, creds: CREDS });

    const r2 = await page.goto(`${BASE}/cluster`, { waitUntil: 'networkidle' });
    if (!r2 || !r2.ok()) {
      throw new Error(`goto /cluster failed for ${palette}/${mode}: status=${r2 ? r2.status() : 'null'}`);
    }

    await page.waitForTimeout(800);
    await page.screenshot({ path: out, fullPage: false });
    console.log(`captured ${palette}/${mode} -> ${out}`);

    await context.close();
  }
} finally {
  await browser.close();
}
