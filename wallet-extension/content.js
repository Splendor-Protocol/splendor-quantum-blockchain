/**
 * Splendor Quantum Wallet — Content Script
 * Bridges inject.js (page world) ↔ background service worker.
 * Runs in the page's isolated context, relays messages via chrome.runtime.
 */
'use strict';

// Forward page → background
window.addEventListener('message', (ev) => {
  if (ev.source !== window) return;
  const msg = ev.data;
  if (!msg || !msg.__sqc_req) return;
  chrome.runtime.sendMessage(msg, (response) => {
    if (chrome.runtime.lastError) {
      window.postMessage({
        __sqc_res: true,
        id: msg.id,
        error: { code: -32000, message: chrome.runtime.lastError.message }
      }, '*');
      return;
    }
    window.postMessage({ __sqc_res: true, id: msg.id, ...response }, '*');
  });
});

// Forward background → page (events: accountsChanged, connect, disconnect)
chrome.runtime.onMessage.addListener((msg) => {
  if (msg && msg.__sqc_event) {
    window.postMessage(msg, '*');
  }
});
