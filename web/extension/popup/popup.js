// skoed popup script — works with both browser.* (Firefox) and chrome.* (Chrome).
const api = typeof browser !== 'undefined' ? browser : chrome;

const ALL_EVENTS = [
  'device.new', 'cluster.node_down', 'cluster.node_rejoined',
  'blocklist.download_failed', 'filter.pause_started', 'filter.pause_expired',
];

function fmtRelTime(ts) {
  const s = Math.floor((Date.now() - ts) / 1000);
  if (s < 60)  return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s/60)}m ago`;
  return `${Math.floor(s/3600)}h ago`;
}

function renderEvents(events) {
  const list = document.getElementById('event-list');
  if (!events || events.length === 0) {
    list.innerHTML = '<li class="empty">No events yet</li>';
    return;
  }
  list.innerHTML = events.slice(0, 10).map(e =>
    `<li><span class="event-type">${e.event}</span><span class="event-ts">${fmtRelTime(e.ts)}</span></li>`
  ).join('');
}

function renderStatus(url, status) {
  document.getElementById('node-url').textContent = url || '—';
  document.getElementById('conn-status').textContent =
    status === 'connected' ? 'Connected' :
    status === 'disconnected' ? 'Disconnected' : 'Unconfigured';
  const badge = document.getElementById('badge');
  badge.className = `badge ${status || 'unconfigured'}`;
  badge.textContent = status === 'connected' ? '' : status === 'disconnected' ? '!' : '?';
}

function buildNotifyChecks(selected) {
  const container = document.getElementById('notify-checks');
  container.innerHTML = ALL_EVENTS.map(e =>
    `<label><input type="checkbox" value="${e}" ${selected.includes(e) ? 'checked' : ''}>${e}</label>`
  ).join('');
}

// Load current settings and state.
api.storage.local.get(
  ['skoed_url', 'api_token', 'notify_events', 'recent_events'],
  ({ skoed_url, api_token, notify_events = ALL_EVENTS, recent_events = [] }) => {
    document.getElementById('inp-url').value   = skoed_url || '';
    document.getElementById('inp-token').value = api_token || '';
    buildNotifyChecks(notify_events);
    renderEvents(recent_events);
    // Determine connection status from badge text set by the background.
    api.browserAction
      ? api.browserAction.getBadgeText({}, text => {
          const s = text === '!' ? 'disconnected' : text === '?' ? 'unconfigured' : 'connected';
          renderStatus(skoed_url, s);
        })
      : api.action.getBadgeText({}, text => {
          const s = text === '!' ? 'disconnected' : text === '?' ? 'unconfigured' : 'connected';
          renderStatus(skoed_url, s);
        });
  }
);

document.getElementById('save-btn').addEventListener('click', () => {
  const url   = document.getElementById('inp-url').value.trim().replace(/\/$/, '');
  const token = document.getElementById('inp-token').value.trim();
  const checked = [...document.querySelectorAll('#notify-checks input:checked')].map(el => el.value);
  if (!url) { document.getElementById('save-msg').textContent = 'URL is required'; return; }
  api.storage.local.set({ skoed_url: url, api_token: token, notify_events: checked }, () => {
    document.getElementById('save-msg').textContent = 'Saved!';
    api.runtime.sendMessage({ type: 'settings-saved' });
    setTimeout(() => { document.getElementById('save-msg').textContent = ''; }, 2000);
  });
});
