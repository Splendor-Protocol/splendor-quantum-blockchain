/**
 * Splendor Quantum Wallet — Page-injected Provider
 *
 * Injects window.splendor into every page.
 * dApps call: await window.splendor.request({ method: '...' })
 * API mirrors the EIP-1193 pattern, adapted for Splendor Quantum Chain.
 *
 * Methods:
 *   sqc_requestAccounts       → [SQ:address]
 *   sqc_accounts              → [SQ:address] or []
 *   sqc_getBalance            → balance string (smallest SPLD units)
 *   sqc_sendTransaction       → tx hash
 *   sqc_sign                  → ML-DSA-65 signature
 *   sqc_moduleCall            → receipt (QRC-20 deploy/mint/transfer/etc)
 *   sqc_chainId               → "splendor-quantum-1"
 *   sqc_blockNumber           → latest block height
 *   sqc_getBlock              → block object
 *   sqc_getTransaction        → receipt
 */
(function () {
  'use strict';

  if (window.splendor) return; // already injected

  // ── Message bridge to content script → background ──────────────────────────

  let _reqId = 0;
  const _pending = new Map();

  window.addEventListener('message', (ev) => {
    if (ev.source !== window) return;
    const { __sqc_res, id, result, error } = ev.data || {};
    if (!__sqc_res) return;
    const pending = _pending.get(id);
    if (!pending) return;
    _pending.delete(id);
    if (error) pending.reject(new ProviderError(error.code, error.message));
    else pending.resolve(result);
  });

  function sendToBackground(method, params) {
    return new Promise((resolve, reject) => {
      const id = ++_reqId;
      _pending.set(id, { resolve, reject });
      window.postMessage({ __sqc_req: true, id, method, params }, '*');
    });
  }

  // ── Event emitter ────────────────────────────────────────────────────────
  class EventEmitter {
    constructor() { this._listeners = {}; }
    on(event, fn) { (this._listeners[event] = this._listeners[event] || []).push(fn); return this; }
    removeListener(event, fn) {
      if (this._listeners[event]) {
        this._listeners[event] = this._listeners[event].filter(l => l !== fn);
      }
      return this;
    }
    emit(event, ...args) { (this._listeners[event] || []).forEach(fn => fn(...args)); }
  }

  // ── Provider Error ───────────────────────────────────────────────────────
  class ProviderError extends Error {
    constructor(code, message) {
      super(message);
      this.code = code;
      this.name = 'ProviderError';
    }
  }

  const USER_REJECTED = 4001;
  const UNAUTHORIZED  = 4100;
  const UNSUPPORTED   = 4200;

  // ── SplendorProvider ────────────────────────────────────────────────────
  class SplendorProvider extends EventEmitter {
    constructor() {
      super();
      this.isSplendor = true;
      this.isQuantumSafe = true;
      this.chainId = 'splendor-quantum-1';
      this._connected = false;
      this._accounts = [];

      // Listen for account/chain changes from background
      window.addEventListener('message', (ev) => {
        if (ev.source !== window) return;
        const { __sqc_event, event, data } = ev.data || {};
        if (!__sqc_event) return;
        if (event === 'accountsChanged') {
          this._accounts = data;
          this.emit('accountsChanged', data);
        }
        if (event === 'connect') {
          this._connected = true;
          this.emit('connect', { chainId: this.chainId });
        }
        if (event === 'disconnect') {
          this._connected = false;
          this._accounts = [];
          this.emit('disconnect');
        }
      });
    }

    /**
     * Main entry point — mirrors EIP-1193 provider.request()
     * @param {{ method: string, params?: any }} args
     */
    async request({ method, params } = {}) {
      switch (method) {
        // ── Account methods ───────────────────────────────────────────────
        case 'sqc_requestAccounts':
        case 'sqc_accounts': {
          const accounts = await sendToBackground('sqc_requestAccounts', params);
          this._accounts = accounts;
          if (accounts.length > 0) {
            this._connected = true;
            this.emit('connect', { chainId: this.chainId });
          }
          return accounts;
        }

        // ── Balance ───────────────────────────────────────────────────────
        case 'sqc_getBalance': {
          const addr = (params && params[0]) || this._accounts[0];
          if (!addr) throw new ProviderError(UNAUTHORIZED, 'No account connected');
          return sendToBackground('sqc_getBalance', [addr]);
        }

        // ── Send transfer ─────────────────────────────────────────────────
        case 'sqc_sendTransaction': {
          // params[0] = { to, amount, fee?, memo? }
          if (!this._accounts[0]) throw new ProviderError(UNAUTHORIZED, 'No account connected');
          return sendToBackground('sqc_sendTransaction', params);
        }

        // ── Sign arbitrary message ────────────────────────────────────────
        case 'sqc_sign': {
          if (!this._accounts[0]) throw new ProviderError(UNAUTHORIZED, 'No account connected');
          return sendToBackground('sqc_sign', params);
        }

        // ── Native module call (QRC-20, DEX, etc.) ─────────────────────────
        case 'sqc_moduleCall': {
          // params[0] = { module, action, params: {...} }
          if (!this._accounts[0]) throw new ProviderError(UNAUTHORIZED, 'No account connected');
          return sendToBackground('sqc_moduleCall', params);
        }

        // ── Chain info ────────────────────────────────────────────────────
        case 'sqc_chainId':
          return this.chainId;

        case 'sqc_blockNumber':
          return sendToBackground('sqc_blockNumber', []);

        case 'sqc_getBlock':
          return sendToBackground('sqc_getBlock', params);

        case 'sqc_getTransaction':
          return sendToBackground('sqc_getTransaction', params);

        case 'sqc_nonce': {
          const addr = (params && params[0]) || this._accounts[0];
          return sendToBackground('sqc_nonce', [addr]);
        }

        // ── Wallet info ───────────────────────────────────────────────────
        case 'wallet_getPermissions':
        case 'wallet_requestPermissions':
          return sendToBackground(method, params);

        case 'wallet_disconnect':
          await sendToBackground('wallet_disconnect', []);
          this._accounts = [];
          this._connected = false;
          this.emit('disconnect');
          return null;

        default:
          throw new ProviderError(UNSUPPORTED, `Method not supported: ${method}`);
      }
    }

    /** Convenience: currently connected address or null */
    get selectedAddress() {
      return this._accounts[0] || null;
    }

    /** True when at least one account is connected */
    get isConnected() {
      return this._connected && this._accounts.length > 0;
    }
  }

  // ── Expose on window ─────────────────────────────────────────────────────
  const provider = new SplendorProvider();
  Object.defineProperty(window, 'splendor', {
    value: provider,
    writable: false,
    configurable: false,
  });

  // Announce the provider for dApps that use event-based detection
  window.dispatchEvent(new CustomEvent('splendor#initialized', { detail: provider }));

  console.log('[Splendor Quantum Wallet] Provider injected — window.splendor ready');
})();
