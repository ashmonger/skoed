// skoed browser extension — Chrome MV3 service worker.
// Same logic as background.js but adapted for the MV3 service worker lifecycle.
// Uses chrome.alarms to keep the worker alive and reconnect periodically.

const BADGE = { connected: '#27ae60', disconnected: '#e74c3c', unconfigured: '#95a5a6' };
const NOTIFY_TITLES = {
  'device.new':                 'skoed — New Device',
  'cluster.node_down':          'skoed — Node Down',
  'cluster.node_rejoined':      'skoed — Node Rejoined',
  'blocklist.download_failed':  'skoed — Blocklist Failed',
  'filter.pause_started':       'skoed — Filtering Paused',
  'filter.pause_expired':       'skoed — Filtering Resumed',
};

let controller = null;
let reconnectDelay = 1000;

function setBadge(state) {
  const color = BADGE[state] || BADGE.unconfigured;
  const text  = state === 'disconnected' ? '!' : state === 'unconfigured' ? '?' : '';
  chrome.action.setBadgeBackgroundColor({ color });
  chrome.action.setBadgeText({ text });
}

function storeRecentEvent(evt) {
  chrome.storage.local.get('recent_events', ({ recent_events = [] }) => {
    recent_events.unshift(evt);
    if (recent_events.length > 10) recent_events.length = 10;
    chrome.storage.local.set({ recent_events });
  });
}

function notify(eventType, data) {
  chrome.storage.local.get('notify_events', ({ notify_events = [] }) => {
    if (!notify_events.includes(eventType)) return;
    const title = NOTIFY_TITLES[eventType] || 'skoed — Event';
    let message = eventType;
    if (eventType === 'device.new' && data.client_ip)
      message = `New device: ${data.client_ip}`;
    if (eventType === 'cluster.node_down' && data.node_id)
      message = `Cluster node ${data.node_id} is down`;
    if (eventType === 'blocklist.download_failed' && data.blocklist_name)
      message = `${data.blocklist_name}: ${data.error || 'download failed'}`;

    chrome.notifications.create({
      type: 'basic',
      iconUrl: chrome.runtime.getURL('icons/icon-128.png'),
      title,
      message,
      requireInteraction: eventType === 'cluster.node_down',
    });
  });
}

function openConnection() {
  chrome.storage.local.get(['skoed_url', 'api_token'], ({ skoed_url, api_token }) => {
    if (!skoed_url || !api_token) { setBadge('unconfigured'); return; }
    if (controller) { controller.abort(); }
    controller = new AbortController();
    setBadge('disconnected');

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
  const delay = Math.min(reconnectDelay, 30000);
  reconnectDelay = Math.min(reconnectDelay * 2, 30000);
  setTimeout(openConnection, delay);
}

// chrome.alarms keeps the service worker alive (MV3 workers sleep after ~30 s idle).
chrome.alarms.create('keepalive', { periodInMinutes: 1 });
chrome.alarms.onAlarm.addListener(alarm => {
  if (alarm.name === 'keepalive' && !controller) openConnection();
});

chrome.runtime.onInstalled.addListener(openConnection);
chrome.runtime.onStartup.addListener(openConnection);
chrome.runtime.onMessage.addListener(msg => {
  if (msg.type === 'settings-saved') { openConnection(); }
});
