// skoed browser extension — Firefox MV2 background script.
// Manages the SSE connection, badge state, and OS notifications.

const BADGE = { connected: '#27ae60', disconnected: '#e74c3c', unconfigured: '#95a5a6' };
const NOTIFY_TITLES = {
  'device.new':                 'skoed — New Device',
  'cluster.node_down':          'skoed — Node Down',
  'cluster.node_rejoined':      'skoed — Node Rejoined',
  'blocklist.download_failed':  'skoed — Blocklist Failed',
  'filter.pause_started':       'skoed — Filtering Paused',
  'filter.pause_expired':       'skoed — Filtering Resumed',
  'webhook.test':               'skoed — Test Event',
};

let evtSource = null;
let reconnectDelay = 1000; // ms; doubles up to 30 s
let reconnectTimer = null;

function setBadge(state) {
  const color = BADGE[state] || BADGE.unconfigured;
  const text  = state === 'disconnected' ? '!' : state === 'unconfigured' ? '?' : '';
  browser.browserAction.setBadgeBackgroundColor({ color });
  browser.browserAction.setBadgeText({ text });
}

function storeRecentEvent(evt) {
  browser.storage.local.get('recent_events').then(({ recent_events = [] }) => {
    recent_events.unshift(evt);
    if (recent_events.length > 10) recent_events.length = 10;
    browser.storage.local.set({ recent_events });
  });
}

function notify(eventType, data) {
  browser.storage.local.get('notify_events').then(({ notify_events = [] }) => {
    if (!notify_events.includes(eventType)) return;
    const title = NOTIFY_TITLES[eventType] || 'skoed — Event';
    let message = eventType;
    if (eventType === 'device.new' && data.client_ip)        message = `New device: ${data.client_ip}`;
    if (eventType === 'cluster.node_down' && data.node_id)   message = `Cluster node ${data.node_id} is down`;
    if (eventType === 'cluster.node_rejoined' && data.node_id) message = `Node ${data.node_id} rejoined`;
    if (eventType === 'blocklist.download_failed' && data.blocklist_name)
      message = `${data.blocklist_name}: ${data.error || 'download failed'}`;
    if (eventType === 'filter.pause_started') message = 'DNS filtering is paused';
    if (eventType === 'filter.pause_expired') message = 'DNS filtering has resumed';

    browser.notifications.create({
      type: 'basic',
      iconUrl: browser.runtime.getURL('icons/icon-128.png'),
      title,
      message,
      requireInteraction: eventType === 'cluster.node_down',
    });
  });
}

function openConnection() {
  browser.storage.local.get(['skoed_url', 'api_token']).then(({ skoed_url, api_token }) => {
    if (!skoed_url || !api_token) { setBadge('unconfigured'); return; }

    // EventSource does not support custom headers natively in Firefox MV2;
    // we use fetch with a ReadableStream to keep the connection open.
    setBadge('disconnected');
    const controller = new AbortController();
    evtSource = controller;

    fetch(`${skoed_url}/api/v1/events`, {
      headers: { Authorization: `Bearer ${api_token}`, Accept: 'text/event-stream' },
      signal: controller.signal,
    }).then(resp => {
      if (!resp.ok) { scheduleReconnect(); return; }
      setBadge('connected');
      reconnectDelay = 1000;
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let currentEvent = {};

      function pump() {
        reader.read().then(({ done, value }) => {
          if (done) { setBadge('disconnected'); scheduleReconnect(); return; }
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop();
          for (const line of lines) {
            if (line === '') {
              if (currentEvent.event && currentEvent.data) {
                try {
                  const payload = JSON.parse(currentEvent.data);
                  storeRecentEvent({ event: currentEvent.event, ts: Date.now(), payload });
                  notify(currentEvent.event, payload.data || {});
                } catch (_) {}
              }
              currentEvent = {};
            } else if (line.startsWith('event:')) {
              currentEvent.event = line.slice(6).trim();
            } else if (line.startsWith('data:')) {
              currentEvent.data = line.slice(5).trim();
            }
          }
          pump();
        }).catch(() => { setBadge('disconnected'); scheduleReconnect(); });
      }
      pump();
    }).catch(() => { setBadge('disconnected'); scheduleReconnect(); });
  });
}

function scheduleReconnect() {
  if (reconnectTimer) clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(() => { openConnection(); }, reconnectDelay);
  reconnectDelay = Math.min(reconnectDelay * 2, 30000);
}

function closeConnection() {
  if (evtSource) { evtSource.abort(); evtSource = null; }
  if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
}

browser.runtime.onInstalled.addListener(openConnection);
browser.runtime.onStartup.addListener(openConnection);

// Reopen the connection when settings are saved from the popup.
browser.runtime.onMessage.addListener(msg => {
  if (msg.type === 'settings-saved') { closeConnection(); openConnection(); }
});
