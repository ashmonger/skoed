import { chromium } from 'playwright';
import { existsSync, mkdirSync } from 'fs';
import { join } from 'path';

const BASE = process.env.SKOED_BASE ?? 'http://localhost:9180';
const OUT_DIR = process.env.OUT_DIR ?? '/tmp/batch-screenshots';
const CREDENTIALS = { user: 'admin', pass: 'Skoed2026!' };

mkdirSync(OUT_DIR, { recursive: true });

const routes = [
  { name: '01-dashboard.png',          path: '/dashboard',             fullPage: false },
  { name: '02-blocklists.png',         path: '/dashboard/blocklists',  fullPage: true  },
  { name: '03-allowlist.png',          path: '/dashboard/allowlist',   fullPage: true  },
  { name: '04-local-dns.png',          path: '/dashboard/local-dns',   fullPage: true  },
  { name: '05-clients.png',            path: '/dashboard/clients',     fullPage: true  },
  { name: '06-profiles.png',           path: '/dashboard/profiles',    fullPage: true  },
  { name: '07-schedules.png',          path: '/dashboard/schedules',   fullPage: true  },
  { name: '08-categories.png',         path: '/dashboard/categories',  fullPage: true  },
  { name: '09-query-log.png',          path: '/dashboard/query-log',   fullPage: true  },
  { name: '10-stats.png',              path: '/dashboard/stats',       fullPage: false },
  { name: '11-test-domain.png',        path: '/dashboard/test-domain', fullPage: false },
  { name: '12-cluster.png',            path: '/dashboard/cluster',     fullPage: false },
  { name: '13-settings.png',           path: '/dashboard/settings',    fullPage: true  },
  { name: '14-api-docs.png',           path: '/api/docs',              fullPage: false },
  { name: '15-api-docs-schedules.png', path: '/api/docs',              fullPage: false, scrollY: 900 },
  { name: '16-firewall-rules.png',     path: '/dashboard/firewall-rules', fullPage: true, optional: true },
  { name: '17-getting-started.png',    path: '/getting-started',       fullPage: false, optional: true,
    fallbackPath: '/docs' },
];

async function login(page) {
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle', timeout: 30000 });
  // Try filling in credentials — handle different form structures
  try {
    await page.fill('input[type="text"], input[name="username"], input[id="username"], input[placeholder*="user" i], input[placeholder*="login" i]', CREDENTIALS.user);
    await page.fill('input[type="password"]', CREDENTIALS.pass);
    await page.click('button[type="submit"], input[type="submit"], button:has-text("Login"), button:has-text("Sign in")');
    await page.waitForURL(url => !url.href.includes('/login'), { timeout: 15000 });
    console.log('Logged in successfully');
  } catch (e) {
    // Maybe already on dashboard or no login page
    const url = page.url();
    console.log(`Login step result, current url: ${url} (${e.message})`);
  }
}

async function isDashboard(page) {
  const url = page.url();
  return url.includes('/dashboard') && !url.includes('?') ;
}

async function takeScreenshot(page, route) {
  const targetUrl = `${BASE}${route.path}`;
  try {
    await page.goto(targetUrl, { waitUntil: 'networkidle', timeout: 20000 });
  } catch (e) {
    console.log(`  WARN: networkidle timeout for ${route.path}, continuing anyway`);
  }

  const finalUrl = page.url();

  // Detect redirect back to login or dashboard when we requested a specific sub-route
  if (route.optional) {
    // For optional routes: if we ended up on dashboard or login when we didn't request those, skip
    if (!finalUrl.includes(route.path) && !finalUrl.startsWith(`${BASE}${route.path}`)) {
      // Try fallback path if given
      if (route.fallbackPath) {
        const fallbackUrl = `${BASE}${route.fallbackPath}`;
        try {
          await page.goto(fallbackUrl, { waitUntil: 'networkidle', timeout: 20000 });
        } catch (e) { /* ignore */ }
        const afterFallback = page.url();
        if (!afterFallback.includes(route.fallbackPath)) {
          console.log(`  SKIP (optional, not found): ${route.name}`);
          return null;
        }
      } else {
        console.log(`  SKIP (optional, not found): ${route.name}`);
        return null;
      }
    }
  }

  // If we requested a non-dashboard sub-route but got redirected to /dashboard, skip if optional
  const requestedPath = route.fallbackPath
    ? [route.path, route.fallbackPath]
    : [route.path];
  const landed = requestedPath.some(p => finalUrl.includes(p));

  if (!landed) {
    if (route.optional) {
      console.log(`  SKIP (redirected away): ${route.name} -> ${finalUrl}`);
      return null;
    }
    console.log(`  WARN: expected ${route.path} but landed on ${finalUrl}`);
  }

  // Wait a bit for JS rendering
  await page.waitForTimeout(1500);

  if (route.scrollY) {
    await page.evaluate((y) => window.scrollTo(0, y), route.scrollY);
    await page.waitForTimeout(500);
  }

  const outPath = join(OUT_DIR, route.name);
  await page.screenshot({ path: outPath, fullPage: route.fullPage });
  console.log(`  OK: ${route.name} (${finalUrl})`);
  return outPath;
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
  });
  const page = await context.newPage();

  // Login
  await login(page);

  const results = [];

  for (const route of routes) {
    console.log(`Taking: ${route.name} -> ${route.path}`);
    try {
      const saved = await takeScreenshot(page, route);
      results.push({ name: route.name, saved: saved !== null, path: saved });
    } catch (e) {
      console.error(`  ERROR: ${route.name}: ${e.message}`);
      results.push({ name: route.name, saved: false, error: e.message });
    }
  }

  await browser.close();

  console.log('\n=== SUMMARY ===');
  for (const r of results) {
    console.log(`${r.saved ? 'OK' : 'SKIP'} ${r.name}${r.error ? ' - ' + r.error : ''}`);
  }

  // Output JSON for caller
  console.log('\n=== JSON ===');
  console.log(JSON.stringify(results, null, 2));
})();
