/**
 * @deprecated LEGACY — unsafe for production submit against current Go wire JSON.
 *
 * Transaction builders here emit string `from`/`to`/`amount`/`fee`, but the node
 * expects byte-array addresses and numeric amount/fee. Use `@splendor/sqc-sdk`
 * (or deprecated `@splendor/pq-bridge` shim) for all signing and submit flows.
 *
 * Splendor Quantum Chain — JavaScript SDK (legacy read helpers only)
 * 
 * A complete client library for interacting with Splendor Quantum Chain nodes.
 * Zero external dependencies — works in Node.js and modern browsers.
 * 
 * Features:
 *   - Account balance and nonce queries
 *   - Transaction submission (pre-signed)
 *   - Block queries (by height or hash)
 *   - Transaction receipt queries
 *   - Real-time event subscriptions (SSE/EventSource)
 *   - X402 micropayment client
 *   - QRC-20 token interaction helpers
 *   - Node status and peer info
 * 
 * Usage:
 *   const sdk = new SplendorQC('http://localhost:8080');
 *   const balance = await sdk.getBalance('SQ:abc123...');
 *   await sdk.submitTransaction(signedTxObject);
 */

'use strict';

// ── Constants ─────────────────────────────────────────────────────────────────

const SDK_VERSION = '1.0.0';
const DEFAULT_RPC = 'http://localhost:8080';
const DEFAULT_TIMEOUT_MS = 10_000;

// ── Core SDK class ────────────────────────────────────────────────────────────

class SplendorQC {
  /**
   * @param {string} rpcUrl - Base URL of the SQC node RPC endpoint.
   * @param {object} [options]
   * @param {number} [options.timeoutMs=10000] - Request timeout in milliseconds.
   * @param {string} [options.chainId='splendor-quantum-1'] - Chain ID for validation.
   */
  constructor(rpcUrl = DEFAULT_RPC, options = {}) {
    this.rpcUrl = rpcUrl.replace(/\/$/, ''); // strip trailing slash
    this.timeoutMs = options.timeoutMs || DEFAULT_TIMEOUT_MS;
    this.chainId = options.chainId || 'splendor-quantum-1';
    this._version = SDK_VERSION;
  }

  // ── Account ────────────────────────────────────────────────────────────────

  /**
   * Get the SPLD balance of an account.
   * @param {string} address - SQ: address string.
   * @returns {Promise<{address: string, balance: string, balance_spld: string, unit: string}>}
   */
  async getBalance(address) {
    _validateAddress(address);
    const data = await this._get(`/balance/${encodeURIComponent(address)}`);
    return {
      ...data,
      balance_spld: _toSPLD(data.balance),
    };
  }

  /**
   * Get the current nonce (transaction count) for an account.
   * @param {string} address - SQ: address string.
   * @returns {Promise<{address: string, nonce: number}>}
   */
  async getNonce(address) {
    _validateAddress(address);
    return this._get(`/nonce/${encodeURIComponent(address)}`);
  }

  // ── Transactions ───────────────────────────────────────────────────────────

  /**
   * Submit a signed transaction to the node's mempool.
   * The transaction must be pre-signed with an ML-DSA-65 key.
   * 
   * @param {object} signedTx - A fully signed transaction object.
   * @param {number} signedTx.version - Must be 1.
   * @param {string} signedTx.chain_id - Must match this.chainId.
   * @param {number} signedTx.type - TxType (1=Transfer, 2=Deploy, 3=Call, 5=X402).
   * @param {string} signedTx.from - SQ: sender address.
   * @param {string} signedTx.to - SQ: recipient address.
   * @param {string} signedTx.amount - Amount in smallest SPLD units (string).
   * @param {string} signedTx.fee - Fee in smallest SPLD units (string).
   * @param {number} signedTx.nonce - Sender's current nonce.
   * @param {number} signedTx.timestamp - Unix timestamp in seconds.
   * @param {string} [signedTx.data] - Base64-encoded calldata/bytecode.
   * @param {string} [signedTx.public_key] - Base64-encoded ML-DSA-65 public key (first tx only).
   * @param {string} signedTx.signature - Base64-encoded ML-DSA-65 signature.
   * @returns {Promise<{status: string, hash: string}>}
   */
  async submitTransaction(signedTx) {
    if (!signedTx || typeof signedTx !== 'object') {
      throw new Error('submitTransaction: signedTx must be an object');
    }
    if (signedTx.chain_id !== this.chainId) {
      throw new Error(`submitTransaction: wrong chain_id (got ${signedTx.chain_id}, want ${this.chainId})`);
    }
    return this._post('/tx/submit', signedTx);
  }

  /**
   * Get a transaction receipt by transaction hash.
   * @param {string} txHash - Hex-encoded transaction hash.
   * @returns {Promise<object>} Receipt object.
   */
  async getReceipt(txHash) {
    return this._get(`/tx/${encodeURIComponent(txHash)}/receipt`);
  }

  // ── Blocks ─────────────────────────────────────────────────────────────────

  /**
   * Get the latest committed block.
   * @returns {Promise<object>} Block object.
   */
  async getLatestBlock() {
    return this._get('/block/latest');
  }

  /**
   * Get a block by height.
   * @param {number|string} height - Block height (0 = genesis).
   * @returns {Promise<object>} Block object.
   */
  async getBlockByHeight(height) {
    return this._get(`/block/${height}`);
  }

  /**
   * Get a block by its SHA3-256 hash.
   * @param {string} hash - Hex-encoded block hash.
   * @returns {Promise<object>} Block object.
   */
  async getBlockByHash(hash) {
    return this._get(`/block/hash/${encodeURIComponent(hash)}`);
  }

  // ── Node ───────────────────────────────────────────────────────────────────

  /**
   * Get the current node status.
   * @returns {Promise<{chain_id: string, height: number, mempool_size: number, peer_count: number, node_version: string, timestamp: number}>}
   */
  async getStatus() {
    return this._get('/status');
  }

  /**
   * Get the list of known peers.
   * @returns {Promise<Array>} Array of peer objects.
   */
  async getPeers() {
    return this._get('/peers');
  }

  /**
   * Get AI scheduler statistics.
   * @returns {Promise<object>} Scheduler stats.
   */
  async getAIStats() {
    return this._get('/ai/stats');
  }

  // ── X402 Micropayments ─────────────────────────────────────────────────────

  /**
   * Verify an X402 payment payload without settling it.
   * @param {object} requirements - Payment requirements from server.
   * @param {object} payload - Signed payment payload.
   * @returns {Promise<{valid: boolean, reason?: string}>}
   */
  async x402Verify(requirements, payload) {
    return this._post('/x402/verify', { requirements, payload });
  }

  /**
   * Settle an X402 payment on-chain.
   * @param {object} requirements - Payment requirements from server.
   * @param {object} payload - Signed payment payload.
   * @returns {Promise<{success: boolean, tx_hash?: string, error?: string}>}
   */
  async x402Settle(requirements, payload) {
    return this._post('/x402/settle', { requirements, payload });
  }

  /**
   * Build an X402 payment payload ready for signing.
   * The returned payload has all fields set except `signature`.
   * 
   * @param {object} opts
   * @param {string} opts.from - Payer SQ: address.
   * @param {string} opts.to - Receiver SQ: address (must match requirements.pay_to).
   * @param {string} opts.amount - Amount in smallest SPLD units.
   * @param {number} [opts.validWindowSecs=300] - Validity window in seconds.
   * @returns {object} Unsigned payment payload.
   */
  buildX402Payload(opts) {
    const now = Math.floor(Date.now() / 1000);
    const window = opts.validWindowSecs || 300;
    return {
      version: 1,
      chain_id: this.chainId,
      from: opts.from,
      to: opts.to,
      amount: opts.amount,
      valid_after: now - 30,
      valid_before: now + window,
      nonce: _randomHex(32),
      // signature: — must be added by the caller using their ML-DSA-65 private key
    };
  }

  // ── Real-time subscriptions ────────────────────────────────────────────────

  /**
   * Subscribe to real-time node events via Server-Sent Events (SSE).
   * 
   * @param {string|string[]} events - Event type(s) to subscribe to.
   *   Possible values: 'new_block', 'new_tx', 'new_receipt', 'node_status', '*'
   * @param {function} onEvent - Callback called with each WSEvent object.
   * @param {function} [onError] - Callback called on connection error.
   * @returns {object} Subscription handle with a .close() method.
   * 
   * @example
   * const sub = sdk.subscribe('new_block', (event) => {
   *   console.log('New block:', event.data.height);
   * });
   * // Later:
   * sub.close();
   */
  subscribe(events, onEvent, onError) {
    const eventsParam = Array.isArray(events) ? events.join(',') : events;
    const url = `${this.rpcUrl}/ws?subscribe=${encodeURIComponent(eventsParam)}`;

    // Use EventSource (SSE) — works in both browser and Node.js (with polyfill)
    let source;
    if (typeof EventSource !== 'undefined') {
      source = new EventSource(url);
    } else {
      // Node.js: use a simple HTTP streaming approach
      return this._nodeSSESubscribe(url, onEvent, onError);
    }

    source.onmessage = (event) => {
      try {
        const parsed = JSON.parse(event.data);
        onEvent(parsed);
      } catch (e) {
        // ignore parse errors for heartbeat comments
      }
    };

    source.onerror = (err) => {
      if (onError) onError(err);
    };

    return {
      close: () => source.close(),
    };
  }

  /**
   * Node.js SSE subscription fallback (no EventSource available).
   * @private
   */
  _nodeSSESubscribe(url, onEvent, onError) {
    const http = url.startsWith('https') ? require('https') : require('http');
    const urlObj = new URL(url);

    const req = http.get({
      hostname: urlObj.hostname,
      port: urlObj.port || (url.startsWith('https') ? 443 : 80),
      path: urlObj.pathname + urlObj.search,
      headers: { Accept: 'text/event-stream' },
    }, (res) => {
      let buffer = '';
      res.on('data', (chunk) => {
        buffer += chunk.toString();
        const lines = buffer.split('\n');
        buffer = lines.pop(); // keep incomplete line in buffer
        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const parsed = JSON.parse(line.slice(6));
              onEvent(parsed);
            } catch (_) {}
          }
        }
      });
      res.on('error', (err) => { if (onError) onError(err); });
    });

    req.on('error', (err) => { if (onError) onError(err); });

    return {
      close: () => req.destroy(),
    };
  }

  // ── Transaction builders ───────────────────────────────────────────────────

  /**
   * Build an unsigned transfer transaction object.
   * You must sign it with your ML-DSA-65 key before submitting.
   * 
   * @param {object} opts
   * @param {string} opts.from - Sender SQ: address.
   * @param {string} opts.to - Recipient SQ: address.
   * @param {string} opts.amount - Amount in smallest SPLD units (string).
   * @param {string} opts.fee - Fee in smallest SPLD units (string).
   * @param {number} opts.nonce - Sender's current nonce (use getNonce() to fetch).
   * @param {string} [opts.memo] - Optional memo (encoded as base64 in data field).
   * @returns {object} Unsigned transaction object ready for signing.
   */
  buildTransfer(opts) {
    _validateAddress(opts.from);
    _validateAddress(opts.to);
    return {
      version: 1,
      chain_id: this.chainId,
      type: 1, // TxTransfer
      from: opts.from,
      to: opts.to,
      amount: String(opts.amount),
      fee: String(opts.fee || '1000'),
      nonce: opts.nonce,
      timestamp: Math.floor(Date.now() / 1000),
      data: opts.memo ? _base64Encode(opts.memo) : undefined,
      // public_key: — include on first transaction from this address
      // signature: — must be added by caller
    };
  }

  /**
   * Build an unsigned contract call transaction object.
   * @param {object} opts
   * @param {string} opts.from - Caller SQ: address.
   * @param {string} opts.contract - Contract SQ: address.
   * @param {Uint8Array|string} opts.calldata - ABI-encoded call data (bytes or base64 string).
   * @param {string} [opts.value='0'] - SPLD value to send with the call.
   * @param {string} opts.fee - Fee in smallest SPLD units.
   * @param {number} opts.nonce - Sender's current nonce.
   * @returns {object} Unsigned transaction object.
   */
  buildCall(opts) {
    _validateAddress(opts.from);
    _validateAddress(opts.contract);
    const calldata = typeof opts.calldata === 'string'
      ? opts.calldata
      : _base64Encode(opts.calldata);
    return {
      version: 1,
      chain_id: this.chainId,
      type: 3, // TxCall
      from: opts.from,
      to: opts.contract,
      amount: String(opts.value || '0'),
      fee: String(opts.fee || '1000'),
      nonce: opts.nonce,
      timestamp: Math.floor(Date.now() / 1000),
      data: calldata,
    };
  }

  // ── Utilities ──────────────────────────────────────────────────────────────

  /**
   * Convert smallest SPLD units to SPLD with 6 decimal places.
   * @param {string|number|bigint} units - Amount in smallest units.
   * @returns {string} Human-readable SPLD amount.
   */
  toSPLD(units) {
    return _toSPLD(String(units));
  }

  /**
   * Convert SPLD to smallest units.
   * @param {string|number} spld - Amount in SPLD.
   * @returns {string} Amount in smallest units (string to avoid precision loss).
   */
  fromSPLD(spld) {
    return _fromSPLD(String(spld));
  }

  /**
   * Check if a string is a valid SQ: address.
   * @param {string} addr
   * @returns {boolean}
   */
  isValidAddress(addr) {
    return typeof addr === 'string' &&
      addr.startsWith('SQ:') &&
      addr.length === 3 + 64 && // "SQ:" + 32 bytes hex = 64 chars
      /^[0-9a-fA-F]{64}$/.test(addr.slice(3));
  }

  // ── HTTP internals ─────────────────────────────────────────────────────────

  async _get(path) {
    const url = this.rpcUrl + path;
    const res = await _fetchWithTimeout(url, {
      method: 'GET',
      headers: { 'Accept': 'application/json', 'X-SQC-SDK': SDK_VERSION },
    }, this.timeoutMs);
    return _parseResponse(res, url);
  }

  async _post(path, body) {
    const url = this.rpcUrl + path;
    const res = await _fetchWithTimeout(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Accept': 'application/json',
        'X-SQC-SDK': SDK_VERSION,
      },
      body: JSON.stringify(body),
    }, this.timeoutMs);
    return _parseResponse(res, url);
  }
}

// ── X402 Middleware (Node.js / Express) ───────────────────────────────────────

/**
 * Express middleware that enforces X402 payment for protected routes.
 * 
 * @param {SplendorQC} sdk - An initialised SplendorQC SDK instance.
 * @param {object} config
 * @param {string} config.payTo - SQ: address of the API operator.
 * @param {string|object} config.price - Price per request in SPLD, or a route-pattern map.
 * @returns {function} Express middleware function.
 * 
 * @example
 * const sdk = new SplendorQC('http://localhost:8080');
 * app.use('/api/premium', x402Middleware(sdk, {
 *   payTo: 'SQ:abc...',
 *   price: '0.001'
 * }));
 */
function x402Middleware(sdk, config) {
  if (!config.payTo) throw new Error('x402Middleware: payTo is required');

  return async function(req, res, next) {
    const paymentHeader = req.headers['x-payment'];

    if (!paymentHeader) {
      // No payment — return 402 with requirements
      const price = _resolvePrice(config.price, req.path);
      const requirements = sdk.buildX402Requirements(config.payTo, price);
      return res.status(402).json({
        error: 'Payment Required',
        requirements,
      });
    }

    try {
      const payload = JSON.parse(paymentHeader);
      const price = _resolvePrice(config.price, req.path);
      const requirements = sdk.buildX402Requirements(config.payTo, price);

      // Verify payment
      const verifyResult = await sdk.x402Verify(requirements, payload);
      if (!verifyResult.valid) {
        return res.status(402).json({
          error: 'Invalid Payment',
          reason: verifyResult.reason,
        });
      }

      // Reject incomplete payments: settlement_tx is required for on-chain settlement.
      // The payer must include a signed TxX402 in the X-Payment payload.
      const settlementTx = payload.settlement_tx;
      if (!settlementTx) {
        return res.status(402).json({
          error: 'Incomplete Payment',
          reason: 'X-Payment payload missing settlement_tx. The payer must include a ' +
            'signed TxX402 settlement transaction. Use the TypeScript SDK payX402() ' +
            'for a compliant client.',
        });
      }

      // Submit settlement on-chain — fire and forget so the API response is not
      // blocked, but log failures loudly so operators can detect broken settlement.
      sdk.x402Settle(requirements, payload, settlementTx).catch((err) => {
        console.error('[x402] settlement error (verified but settlement failed):', err.message || err);
      });

      // Report settlement tx hash in response headers so callers can track it.
      res.setHeader('X-Settlement-Tx', (settlementTx && settlementTx.hash) || 'submitted');

      next();
    } catch (err) {
      return res.status(400).json({ error: 'Malformed X-Payment header' });
    }
  };
}

// Add buildX402Requirements to SplendorQC prototype
SplendorQC.prototype.buildX402Requirements = function(payTo, amountSPLD) {
  const now = Math.floor(Date.now() / 1000);
  return {
    pay_to: payTo,
    amount: _fromSPLD(String(amountSPLD)),
    valid_after: now - 30,
    valid_before: now + 300,
    description: 'API access payment',
  };
};

// ── Private helpers ────────────────────────────────────────────────────────────

function _validateAddress(addr) {
  if (typeof addr !== 'string' || !addr.startsWith('SQ:')) {
    throw new Error(`Invalid SQ: address: ${addr}`);
  }
}

const SPLD_DECIMALS = 18n;
const SPLD_DIVISOR = 10n ** SPLD_DECIMALS;

function _toSPLD(units) {
  try {
    const u = BigInt(units);
    const whole = u / SPLD_DIVISOR;
    const frac = u % SPLD_DIVISOR;
    const fracStr = frac.toString().padStart(18, '0').replace(/0+$/, '') || '0';
    return `${whole}.${fracStr.slice(0, 6)}`;
  } catch (_) {
    return '0.000000';
  }
}

function _fromSPLD(spld) {
  try {
    const parts = spld.split('.');
    const whole = BigInt(parts[0] || '0');
    const fracStr = (parts[1] || '').padEnd(18, '0').slice(0, 18);
    const frac = BigInt(fracStr);
    return String(whole * SPLD_DIVISOR + frac);
  } catch (_) {
    return '0';
  }
}

async function _fetchWithTimeout(url, options, timeoutMs) {
  // Use AbortController if available (Node 18+, modern browsers)
  if (typeof AbortController !== 'undefined') {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const res = await fetch(url, { ...options, signal: controller.signal });
      return res;
    } finally {
      clearTimeout(timer);
    }
  }
  // Fallback: no timeout
  return fetch(url, options);
}

async function _parseResponse(res, url) {
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch (_) {
    throw new Error(`SQC RPC ${url}: non-JSON response (${res.status}): ${text.slice(0, 200)}`);
  }
  if (!res.ok) {
    throw new Error(`SQC RPC ${url}: ${res.status} ${json.error || text.slice(0, 200)}`);
  }
  return json;
}

function _randomHex(bytes) {
  if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
    const arr = new Uint8Array(bytes);
    crypto.getRandomValues(arr);
    return Array.from(arr).map(b => b.toString(16).padStart(2, '0')).join('');
  }
  // Node.js fallback
  const { randomBytes } = require('crypto');
  return randomBytes(bytes).toString('hex');
}

function _base64Encode(data) {
  if (typeof data === 'string') {
    if (typeof btoa !== 'undefined') return btoa(data);
    return Buffer.from(data).toString('base64');
  }
  if (typeof Buffer !== 'undefined') return Buffer.from(data).toString('base64');
  return btoa(String.fromCharCode(...data));
}

function _resolvePrice(price, path) {
  if (typeof price === 'string' || typeof price === 'number') return String(price);
  if (typeof price === 'object') {
    // Route map: { '/api/premium': '0.01', '/api/basic': '0.001' }
    for (const [pattern, amount] of Object.entries(price)) {
      if (path.startsWith(pattern)) return String(amount);
    }
  }
  return '0.001'; // default
}

// ── Exports ────────────────────────────────────────────────────────────────────

// CommonJS (Node.js)
if (typeof module !== 'undefined' && module.exports) {
  module.exports = { SplendorQC, x402Middleware, SDK_VERSION };
}

// ES Module
if (typeof globalThis !== 'undefined') {
  globalThis.SplendorQC = SplendorQC;
  globalThis.x402Middleware = x402Middleware;
}
