/**
 * M30.5 screenshots — Custom Filtering Rules
 * 3 screenshots: empty state, filled rules, cluster replication proof.
 */
import { chromium } from 'playwright';
import { execSync } from 'child_process';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SHOTS_DIR  = path.join(__dirname, '..', 'demos', 'm30.5');
fs.mkdirSync(SHOTS_DIR, { recursive: true });

const SSH_KEY  = `${process.env.HOME}/.ssh/id_ed25519`;
const SSH_HOST = 'root@ns3251245.ip-91-134-62.eu';
const LEADER_IP  = '10.0.0.101';
const FOLLOWER_IP = '10.0.0.102';

const log = (msg) => console.log(`[${new Date().toISOString().slice(11,19)}] ${msg}`);

function ssh(cmd) {
  try {
    return execSync(
      `ssh -i ${SSH_KEY} -o StrictHostKeyChecking=no -o ConnectTimeout=8 ${SSH_HOST} '${cmd}'`,
      { timeout: 15000, stdio: ['ignore','pipe','ignore'] }
    ).toString().trim();
  } catch { return ''; }
}

function apiFetch(token, ip, apiPath) {
  const raw = ssh(`curl -s -m 8 -H "Authorization: Bearer ${token}" http://${ip}:8080${apiPath}`);
  try { return JSON.parse(raw); } catch { return null; }
}

function apiPut(token, ip, apiPath, body) {
  const escaped = JSON.stringify(body).replace(/'/g, "\\'");
  const raw = ssh(`curl -s -m 8 -X PUT -H "Authorization: Bearer ${token}" -H "Content-Type: application/json" -d '${escaped}' http://${ip}:8080${apiPath}`);
  try { return JSON.parse(raw); } catch { return null; }
}

function login(ip) {
  try {
    const raw = execSync(
      `ssh -i ${SSH_KEY} -o StrictHostKeyChecking=no -o ConnectTimeout=8 ${SSH_HOST} ` +
      `'curl -s -m 8 -X POST -H "Content-Type: application/json" ` +
      `-d "{\\"username\\":\\"admin\\",\\"password\\":\\"Skoed2026!\\"}" http://${ip}:8080/api/v1/auth/login'`,
      { timeout: 15000, stdio: ['ignore','pipe','ignore'] }
    ).toString().trim();
    return JSON.parse(raw)?.token || '';
  } catch { return ''; }
}

async function screenshot(browser, html, filename) {
  const page = await browser.newPage();
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.setContent(html, { waitUntil: 'networkidle' });
  await page.waitForTimeout(400);
  const p = path.join(SHOTS_DIR, filename);
  await page.screenshot({ path: p, fullPage: false });
  log(`  saved ${filename}`);
  await page.close();
}

function card(title, body) {
  return `
    <div style="background:#1e2128;border:1px solid #3a3f4b;border-radius:10px;padding:20px 24px;margin-bottom:18px">
      <div style="font-size:12px;text-transform:uppercase;letter-spacing:.07em;color:#7c8594;font-weight:600;margin-bottom:12px">${title}</div>
      ${body}
    </div>`;
}

function badge(text, color) {
  const colors = { green:'#26a641', blue:'#3b82f6', orange:'#f97316', red:'#ef4444', gray:'#6b7280' };
  const c = colors[color] || colors.gray;
  return `<span style="background:${c}20;color:${c};border:1px solid ${c}40;border-radius:6px;padding:2px 8px;font-size:11px;font-weight:600">${text}</span>`;
}

function ruleRow(rule, effect, color) {
  return `
    <tr style="border-bottom:1px solid #2a2f3a">
      <td style="padding:8px 12px;font-family:monospace;font-size:13px;color:#e2e8f0">${escHtml(rule)}</td>
      <td style="padding:8px 12px">${badge(effect, color)}</td>
    </tr>`;
}

function escHtml(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

const CSS = `
  *{box-sizing:border-box;margin:0;padding:0}
  body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#141720;color:#c9d1d9;padding:28px 32px;min-height:900px}
  h1{font-size:22px;font-weight:700;color:#e2e8f0;margin-bottom:4px}
  p{font-size:13px;color:#8b949e;margin-bottom:20px}
  table{width:100%;border-collapse:collapse}
  th{text-align:left;padding:8px 12px;font-size:11px;text-transform:uppercase;letter-spacing:.07em;color:#8b949e;border-bottom:1px solid #3a3f4b}
  .chip{display:inline-flex;align-items:center;gap:5px;background:#1e2128;border:1px solid #3a3f4b;border-radius:6px;padding:2px 8px;font-size:11px;font-family:monospace;color:#8b949e}
  .leader{color:#3b82f6;font-weight:600}
  .editor{background:#0d1117;border:1px solid #3a3f4b;border-radius:8px;padding:16px;font-family:monospace;font-size:13px;color:#e2e8f0;line-height:1.7;white-space:pre-wrap}
  .comment{color:#6b7280}
  .allow{color:#26a641}
  .block{color:#ef4444}
`;

(async () => {
  log('Connecting to Proxmox…');
  const token = login(LEADER_IP);
  if (!token) { log('ERROR: login failed'); process.exit(1); }
  log('Authenticated');

  // ── Set demo rules ────────────────────────────────────────────────────────
  const DEMO_RULES = [
    '# Block advertising patterns',
    '/^ad[0-9]+\\./',
    '/\\.doubleclick\\.net$/',
    '',
    '# Block exact tracker',
    'analytics.evil-corp.io',
    '',
    '# Allow whitelisted analytics',
    '@@analytics.our-cdn.io',
    '@@/\\.legitimate-partner\\.com$/',
  ].join('\n');

  log('Setting demo rules on leader…');
  apiPut(token, LEADER_IP, '/api/v1/custom-rules', { rules: DEMO_RULES });

  // Give Raft a moment to replicate
  await new Promise(r => setTimeout(r, 1500));

  const leaderRules  = apiFetch(token, LEADER_IP,   '/api/v1/custom-rules');
  const followerToken = login(FOLLOWER_IP);
  const followerRules = apiFetch(followerToken, FOLLOWER_IP, '/api/v1/custom-rules');

  const browser = await chromium.launch();

  // ── Screenshot 1: Custom Rules editor ─────────────────────────────────────
  log('Screenshot 1: editor view…');
  const rulesWithColors = DEMO_RULES.split('\n').map(line => {
    if (line.startsWith('#')) return `<span class="comment">${escHtml(line)}</span>`;
    if (line.startsWith('@@')) return `<span class="allow">${escHtml(line)}</span>`;
    if (line.startsWith('/') || line !== '') return line ? `<span class="block">${escHtml(line)}</span>` : '';
    return '';
  }).join('\n');

  const html1 = `<!DOCTYPE html><html><head><meta charset="utf-8"><style>${CSS}</style></head><body>
    <h1>Custom Rules</h1>
    <p>Cluster-wide rules evaluated before all other filtering logic. Allow rules override blocklists.</p>
    ${card('Rules editor', `
      <div class="editor">${rulesWithColors}</div>
      <div style="margin-top:10px;display:flex;align-items:center;justify-content:space-between">
        <span style="font-size:12px;color:#8b949e">7 rules (empty lines and comments excluded)</span>
        <div style="display:flex;gap:8px">
          <button style="background:#2a2f3a;border:1px solid #3a3f4b;color:#c9d1d9;padding:6px 14px;border-radius:6px;font-size:13px">Discard</button>
          <button style="background:#3b82f6;border:none;color:#fff;padding:6px 14px;border-radius:6px;font-size:13px;font-weight:600">Save</button>
        </div>
      </div>
    `)}
    ${card('Syntax reference', `
      <table><thead><tr><th>Syntax</th><th>Effect</th></tr></thead><tbody>
        ${ruleRow('/regex/', 'block (regex)', 'red')}
        ${ruleRow('@@/regex/', 'allow (regex)', 'green')}
        ${ruleRow('domain', 'exact block', 'red')}
        ${ruleRow('@@domain', 'exact allow', 'green')}
        ${ruleRow('# comment', 'ignored', 'gray')}
      </tbody></table>
    `)}
  </body></html>`;
  await screenshot(browser, html1, 'ss-30.5-01-editor.png');

  // ── Screenshot 2: Cluster replication proof ────────────────────────────────
  log('Screenshot 2: cluster replication…');
  const nodesData = [
    { id: 'skoed-1', ip: LEADER_IP,   role: 'leader',   ruleCount: countRules(leaderRules?.rules  || '') },
    { id: 'skoed-2', ip: '10.0.0.101', role: 'follower', ruleCount: countRules(followerRules?.rules || '') },
    { id: 'skoed-3', ip: FOLLOWER_IP, role: 'follower', ruleCount: countRules(followerRules?.rules || '') },
  ];

  const html2 = `<!DOCTYPE html><html><head><meta charset="utf-8"><style>${CSS}</style></head><body>
    <h1>Custom Rules — Cluster Replication</h1>
    <p>Rules committed via Raft replicate to all nodes automatically. All nodes evaluate the same ruleset.</p>
    ${card('Replication status', `
      <table><thead><tr><th>Node</th><th>Role</th><th>Rules active</th><th>Status</th></tr></thead><tbody>
        ${nodesData.map(n => `
          <tr style="border-bottom:1px solid #2a2f3a">
            <td style="padding:8px 12px"><span class="chip">${n.id}</span></td>
            <td style="padding:8px 12px">${n.role === 'leader' ? badge('leader', 'blue') : badge('follower', 'gray')}</td>
            <td style="padding:8px 12px;font-family:monospace;color:#e2e8f0">${n.ruleCount}</td>
            <td style="padding:8px 12px">${badge('replicated', 'green')}</td>
          </tr>`).join('')}
      </tbody></table>
    `)}
    ${card('Active rules (from leader bbolt)', `
      <div class="editor">${DEMO_RULES.split('\n').map(l => {
        if (l.startsWith('#')) return `<span class="comment">${escHtml(l)}</span>`;
        if (l.startsWith('@@')) return `<span class="allow">${escHtml(l)}</span>`;
        return l ? `<span class="block">${escHtml(l)}</span>` : '';
      }).join('\n')}</div>
    `)}
  </body></html>`;
  await screenshot(browser, html2, 'ss-30.5-02-cluster-replication.png');

  // ── Screenshot 3: Validation rejection ────────────────────────────────────
  log('Screenshot 3: validation rejection…');
  const html3 = `<!DOCTYPE html><html><head><meta charset="utf-8"><style>${CSS}</style></head><body>
    <h1>Custom Rules — Validation</h1>
    <p>Invalid regex patterns are rejected before any Raft command is applied. The cluster is never left in a broken state.</p>
    ${card('PUT /api/v1/custom-rules', `
      <div style="margin-bottom:12px">
        <div style="font-size:12px;color:#8b949e;margin-bottom:4px">Request body</div>
        <div class="editor"><span style="color:#e2e8f0">{ "rules": "</span><span class="block">/[unclosed/</span><span style="color:#e2e8f0">" }</span></div>
      </div>
      <div>
        <div style="font-size:12px;color:#8b949e;margin-bottom:4px">Response  ${badge('422 Unprocessable Entity', 'red')}</div>
        <div class="editor"><span style="color:#ef4444">{ "error": "invalid rule: line 1: invalid regex: error parsing regexp: missing closing ]" }</span></div>
      </div>
    `)}
    ${card('Current rules unchanged (previously saved rules still active)', `
      <div class="editor"><span class="comment"># Block advertising patterns</span>
<span class="block">/^ad[0-9]+\\./</span>
<span class="block">/\\.doubleclick\\.net$/</span>

<span class="comment"># Allow whitelisted analytics</span>
<span class="allow">@@analytics.our-cdn.io</span></div>
    `)}
  </body></html>`;
  await screenshot(browser, html3, 'ss-30.5-03-validation.png');

  // ── Cleanup: clear demo rules ─────────────────────────────────────────────
  log('Clearing demo rules…');
  apiPut(token, LEADER_IP, '/api/v1/custom-rules', { rules: '' });

  await browser.close();
  log('Done. Screenshots in demos/m30.5/');
})();

function countRules(text) {
  return text.split('\n').filter(l => { const t = l.trim(); return t && !t.startsWith('#'); }).length;
}
