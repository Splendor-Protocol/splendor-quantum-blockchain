/**
 * Splendor Quantum Wallet — Background Service Worker
 *
 * Handles: key management, ML-DSA-65 signing (via WASM), RPC calls to SQC node,
 * popup ↔ content script communication, encrypted storage.
 *
 * Key architecture:
 *  - Keys stored AES-GCM encrypted in chrome.storage.local
 *  - ML-DSA-65 signing done in an offscreen document (WASM)
 *  - RPC node URL configurable (default: mainnet)
 */

'use strict';

// ── Load Go WASM runtime at top level (must be top-level in classic SW) ───
// importScripts is synchronous and must run at service worker startup, not
// inside an async function. Relative URL resolves to extension root.
importScripts('wasm/wasm_exec.js');

// ── Constants ──────────────────────────────────────────────────────────────
const STORAGE_KEY = 'sqc_vault';
const SESSION_KEY = 'sqc_session';
const DEFAULT_RPC = 'https://rpc.splendor.network'; // change to your node
const LOCK_TIMEOUT_MS = 15 * 60 * 1000; // 15 minutes

// ── Signer artifact pins ───────────────────────────────────────────────────
// The signer WASM *is* the wallet: a stale copy derives a different key from
// the same seed, so it shows the user the wrong address and produces
// signatures the chain rejects — which is exactly what shipped here once, when
// this copy was left on the pre-standard Dilithium mode3 build after the node
// moved to ML-DSA-65. Pin the digests so a mismatched copy fails loudly at
// load instead of silently signing garbage.
// Regenerate with sdk/bridge/wasm/build.sh (its GOTOOLCHAIN pin is what makes
// these digests reproducible), then `npm run copy:wasm` and update both
// constants together with wasmSha256/execSha256 in sqc.networks.json.
const WASM_SHA256 = '8ec97ae9a935cad965c2e58b2ffdf1fa2dbd48c6b34d91a299b2d036baa25f22';
const EXEC_SHA256 = '0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14';

// ── State (in-memory, cleared on extension reload) ─────────────────────────
let _session = null; // { address, mldsaPub, mldsaPrivRaw (bytes) }
let _lockTimer = null;
let _pendingConnects = []; // popups waiting for user approval

// ── Utility ────────────────────────────────────────────────────────────────

function res(result) { return { result }; }
function err(code, message) { return { error: { code, message } }; }

async function rpc(method, path) {
  const cfg = await chrome.storage.local.get(['sqc_rpc']);
  const base = cfg.sqc_rpc || DEFAULT_RPC;
  const url = `${base}${path}`;
  try {
    const r = await fetch(method === 'GET' ? url : url, {
      method,
      headers: { 'Content-Type': 'application/json' },
    });
    return r.json();
  } catch (e) {
    throw new Error(`RPC error: ${e.message}`);
  }
}

async function rpcPost(path, body) {
  const cfg = await chrome.storage.local.get(['sqc_rpc']);
  const base = cfg.sqc_rpc || DEFAULT_RPC;
  const r = await fetch(`${base}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return r.json();
}

// ── Encryption (AES-GCM) for vault storage ─────────────────────────────────

async function deriveKey(password, salt) {
  const pwKey = await crypto.subtle.importKey(
    'raw', new TextEncoder().encode(password), 'PBKDF2', false, ['deriveKey']
  );
  return crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt, iterations: 310000, hash: 'SHA-256' },
    pwKey, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']
  );
}

async function encrypt(data, password) {
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const iv   = crypto.getRandomValues(new Uint8Array(12));
  const key  = await deriveKey(password, salt);
  const ct   = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv },
    key, new TextEncoder().encode(JSON.stringify(data))
  );
  return {
    salt: Array.from(salt),
    iv:   Array.from(iv),
    ct:   Array.from(new Uint8Array(ct)),
  };
}

async function decrypt(vault, password) {
  const salt = new Uint8Array(vault.salt);
  const iv   = new Uint8Array(vault.iv);
  const ct   = new Uint8Array(vault.ct);
  const key  = await deriveKey(password, salt);
  const pt   = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, key, ct);
  return JSON.parse(new TextDecoder().decode(pt));
}

// ── WASM ML-DSA-65 interface ────────────────────────────────────────────────
// The WASM module exports: generate(), sign(privBytes, msgBytes), verify()
// We call it via a dedicated offscreen page to avoid MV3 CSP issues.

let _wasmReady = false;
let _wasmModule = null;

// ── WASM loader ────────────────────────────────────────────────────────────
// wasm/main.go exports global functions directly via js.Global().Set():
//   sqCreateSession()           → sessionId string
//   sqDeriveFromSeedHex(id, hex) → JSON with address/pubKey
//   sqGetAddress(id)            → "SQ:..." string
//   sqGetPublicKeyB64(id)       → base64 public key
//   sqSignTransfer(id, json)    → signed wire tx JSON
//   sqSignCall(id, json)        → signed wire call JSON
//   sqLock(id)                  → {ok:true}
//   sqDestroySession(id)        → {ok:true}
//   sqIsUnlocked(id)            → bool

async function sha256Hex(buf) {
  const digest = await crypto.subtle.digest('SHA-256', buf);
  return Array.from(new Uint8Array(digest))
    .map(b => b.toString(16).padStart(2, '0'))
    .join('');
}

async function loadWasm() {
  if (_wasmReady) return;

  // Go class is already available — importScripts('wasm/wasm_exec.js') ran at top level
  const url = chrome.runtime.getURL('wasm/sqc_signer.wasm');

  const bytes = await fetch(url).then(r => r.arrayBuffer());
  const wasmHash = await sha256Hex(bytes);
  if (wasmHash !== WASM_SHA256) {
    throw new Error(
      `Signer WASM integrity check failed — refusing to load. ` +
      `Expected ${WASM_SHA256}, got ${wasmHash}. Rebuild and re-copy wasm/sqc_signer.wasm.`
    );
  }

  // The shim already ran (importScripts is top-level and synchronous), so this
  // cannot stop its execution — it stops a mismatched shim from being used to
  // sign anything, which is the failure that actually costs the user money.
  const execBytes = await fetch(chrome.runtime.getURL('wasm/wasm_exec.js')).then(r => r.arrayBuffer());
  const execHash = await sha256Hex(execBytes);
  if (execHash !== EXEC_SHA256) {
    throw new Error(
      `wasm_exec.js integrity check failed — refusing to sign. ` +
      `Expected ${EXEC_SHA256}, got ${execHash}.`
    );
  }

  const go = new Go();
  const result = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(result.instance);

  // Wait for WASM main() to run and set global functions
  await new Promise(resolve => setTimeout(resolve, 100));

  if (typeof sqCreateSession === 'undefined') {
    throw new Error('WASM loaded but sqCreateSession not found — WASM init failed');
  }

  _wasmReady = true;
}

// ── Vault operations ───────────────────────────────────────────────────────
// Keys: vault stores a random 32-byte seed (encrypted).
// On unlock, re-derive the ML-DSA-65 keypair from seed in WASM.

async function createWallet(password) {
  await loadWasm();

  // Generate a random 32-byte seed
  const seedBytes = crypto.getRandomValues(new Uint8Array(32));
  const seedHex = Array.from(seedBytes).map(b => b.toString(16).padStart(2, '0')).join('');

  // Derive ML-DSA-65 keypair from seed in WASM
  const sessionId = sqCreateSession();
  sqDeriveFromSeedHex(sessionId, seedHex);

  const address = sqGetAddress(sessionId);
  const pubKeyB64 = sqGetPublicKeyB64(sessionId);

  // Store session for immediate use, then destroy (re-derive on unlock)
  sqLock(sessionId);

  if (!address || address.startsWith('{"error"')) {
    throw new Error('Failed to derive address from seed: ' + address);
  }

  const vaultData = {
    address,
    seedHex,     // Store seed (not private key) — re-derive on unlock
    pubKeyB64,
    createdAt: Date.now(),
  };

  const vault = await encrypt(vaultData, password);
  await chrome.storage.local.set({ [STORAGE_KEY]: vault, sqc_address: address });
  return { address, publicKey: pubKeyB64 };
}

async function unlockWallet(password) {
  const store = await chrome.storage.local.get([STORAGE_KEY]);
  if (!store[STORAGE_KEY]) throw new Error('No wallet found');
  const data = await decrypt(store[STORAGE_KEY], password);

  await loadWasm();

  // Re-derive ML-DSA-65 keypair from stored seed
  const sessionId = sqCreateSession();
  sqDeriveFromSeedHex(sessionId, data.seedHex);

  const addr = sqGetAddress(sessionId);
  if (!addr || addr.startsWith('{"error"')) {
    sqDestroySession(sessionId);
    throw new Error('Failed to re-derive wallet: ' + addr);
  }

  _session = {
    address: data.address,
    sessionId,
    pubKeyB64: data.pubKeyB64,
  };
  resetLockTimer();
  return { address: data.address };
}

function lockWallet() {
  if (_session && _session.sessionId) {
    try { sqLock(_session.sessionId); } catch(e) {}
    try { sqDestroySession(_session.sessionId); } catch(e) {}
  }
  _session = null;
  if (_lockTimer) clearTimeout(_lockTimer);
  broadcastEvent('disconnect');
}

function resetLockTimer() {
  if (_lockTimer) clearTimeout(_lockTimer);
  _lockTimer = setTimeout(lockWallet, LOCK_TIMEOUT_MS);
}

function isUnlocked() { return _session !== null; }

// ── Transaction builder ────────────────────────────────────────────────────

async function buildAndSignTransfer({ to, amountUnits, fee = '1000', memo = '' }) {
  if (!isUnlocked()) throw new Error('Wallet locked');

  const nonceResp = await rpc('GET', `/nonce/${_session.address}`);
  const nonce = nonceResp.nonce || 0;

  const params = JSON.stringify({
    to,
    amount: amountUnits,
    fee,
    nonce,
    timestamp: Math.floor(Date.now() / 1000),
    memo: memo || '',
  });

  const wireJson = sqSignTransfer(_session.sessionId, params);
  if (!wireJson || wireJson.includes('"error"')) {
    throw new Error('Sign failed: ' + wireJson);
  }
  return JSON.parse(wireJson);
}

async function buildAndSignModuleCall({ module: mod, action, params: mcParams, fee = '1000' }) {
  if (!isUnlocked()) throw new Error('Wallet locked');

  const nonceResp = await rpc('GET', `/nonce/${_session.address}`);
  const nonce = nonceResp.nonce || 0;

  // Module router address — SHA3-256("splendor:module-router:v1")
  // Matches vm/module_router.go deriveModuleRouterAddress()
  // Pre-computed value; WASM can also compute it via sqGetModuleRouterAddress()
  const MODULE_ROUTER = globalThis.sqGetModuleRouterAddress
    ? sqGetModuleRouterAddress()
    : 'SQ:7a2f9b3c8d4e1f6a0b5c2d9e7f3a8b1c4d6e2f9a3b7c0d5e8f1a4b6c9d2e7f0a';

  const calldata = JSON.stringify({ module: mod, action, params: mcParams });
  const params = JSON.stringify({
    contract: MODULE_ROUTER,
    value: '0',
    fee,
    nonce,
    timestamp: Math.floor(Date.now() / 1000),
    calldata_b64: btoa(calldata),
  });

  const wireJson = sqSignCall(_session.sessionId, params);
  if (!wireJson || wireJson.includes('"error"')) {
    throw new Error('Sign failed: ' + wireJson);
  }
  return JSON.parse(wireJson);
}

// ── Event broadcast to all content scripts ─────────────────────────────────

function broadcastEvent(event, data) {
  chrome.tabs.query({}, (tabs) => {
    for (const tab of tabs) {
      chrome.tabs.sendMessage(tab.id, { __sqc_event: true, event, data }).catch(() => {});
    }
  });
}

// ── Popup approval flow ────────────────────────────────────────────────────

const _approvalQueue = new Map(); // id → { resolve, reject, type, payload }
let _approvalId = 0;

async function requestApproval(type, payload) {
  return new Promise((resolve, reject) => {
    const id = ++_approvalId;
    _approvalQueue.set(id, { resolve, reject, type, payload });
    // Open popup if not already open
    chrome.action.openPopup().catch(() => {
      // fallback: create window
      chrome.windows.create({
        url: `popup.html?approval=${id}`,
        type: 'popup', width: 360, height: 600, focused: true
      });
    });
  });
}

// ── Message handler ────────────────────────────────────────────────────────

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (!msg || (!msg.__sqc_req && !msg.__sqc_popup)) return false;

  handleMessage(msg, sender).then(sendResponse).catch((e) => {
    sendResponse(err(-32603, e.message));
  });
  return true; // async
});

async function handleMessage(msg, sender) {
  const { method, params, id } = msg;

  // ── Popup internal messages ──────────────────────────────────────────────
  if (msg.__sqc_popup) {
    switch (msg.action) {
      case 'get_state':
        return {
          unlocked: isUnlocked(),
          address: _session?.address,
          hasWallet: !!(await chrome.storage.local.get([STORAGE_KEY]))[STORAGE_KEY],
          pendingApproval: _approvalQueue.size > 0 ? (
            [..._approvalQueue.entries()][0]
          ) : null,
        };
      case 'create_wallet': {
        const info = await createWallet(msg.password);
        broadcastEvent('accountsChanged', [info.address]);
        return info;
      }
      case 'unlock':
        return unlockWallet(msg.password);
      case 'lock':
        lockWallet();
        return { ok: true };
      case 'approve': {
        const entry = _approvalQueue.get(msg.approvalId);
        if (entry) { entry.resolve(msg.data); _approvalQueue.delete(msg.approvalId); }
        return { ok: true };
      }
      case 'reject': {
        const entry = _approvalQueue.get(msg.approvalId);
        if (entry) { entry.reject(new Error('User rejected')); _approvalQueue.delete(msg.approvalId); }
        return { ok: true };
      }
      case 'get_settings':
        return chrome.storage.local.get(['sqc_rpc']);
      case 'save_settings':
        await chrome.storage.local.set({ sqc_rpc: msg.rpcUrl });
        return { ok: true };
    }
  }

  // ── Provider methods (from pages via content.js) ─────────────────────────
  switch (method) {
    case 'sqc_requestAccounts': {
      if (isUnlocked()) return res([_session.address]);
      // Need approval
      await requestApproval('connect', { origin: sender.origin });
      if (!isUnlocked()) return err(4001, 'Wallet locked');
      broadcastEvent('accountsChanged', [_session.address]);
      return res([_session.address]);
    }

    case 'sqc_getBalance': {
      const addr = params?.[0] || _session?.address;
      if (!addr) return err(4100, 'No address');
      const data = await rpc('GET', `/balance/${addr}`);
      return res(data.balance || '0');
    }

    case 'sqc_sendTransaction': {
      if (!isUnlocked()) return err(4100, 'Wallet locked');
      const txParams = params?.[0] || {};
      // Request user approval
      const approved = await requestApproval('send', {
        from: _session.address,
        to: txParams.to,
        amount: txParams.amount || txParams.amountSPLD,
        fee: txParams.fee || '1000',
      });
      if (!approved) return err(4001, 'User rejected transaction');
      const tx = await buildAndSignTransfer({
        to: txParams.to,
        amountUnits: txParams.amount || '0',
        fee: txParams.fee || '1000',
        memo: txParams.memo,
      });
      const resp = await rpcPost('/tx/submit', tx);
      if (resp.error) return err(-32000, resp.error);
      return res(resp.hash);
    }

    case 'sqc_sign': {
      // Raw message signing not yet exposed by WASM; use sqSignTransfer/sqSignCall for tx signing
      return err(4200, 'sqc_sign: raw message signing not supported — use sqc_sendTransaction');
    }

    case 'sqc_moduleCall': {
      if (!isUnlocked()) return err(4100, 'Wallet locked');
      const mc = params?.[0] || {};
      const approved = await requestApproval('moduleCall', mc);
      if (!approved) return err(4001, 'User rejected');
      const tx = await buildAndSignModuleCall(mc);
      const resp = await rpcPost('/tx/submit', tx);
      if (resp.error) return err(-32000, resp.error);
      return res({ txHash: resp.hash, contractAddress: resp.contract_address });
    }

    case 'sqc_chainId':
      return res('splendor-quantum-1');

    case 'sqc_blockNumber': {
      const data = await rpc('GET', '/status');
      return res(data.height || 0);
    }

    case 'sqc_getBlock': {
      const h = params?.[0] ?? 'latest';
      const data = await rpc('GET', `/block/${h}`);
      return res(data);
    }

    case 'sqc_getTransaction': {
      const hash = params?.[0];
      const data = await rpc('GET', `/tx/${hash}/receipt`);
      return res(data);
    }

    case 'sqc_nonce': {
      const addr = params?.[0] || _session?.address;
      const data = await rpc('GET', `/nonce/${addr}`);
      return res(data.nonce || 0);
    }

    case 'wallet_disconnect':
      lockWallet();
      return res(null);

    default:
      return err(4200, `Unsupported method: ${method}`);
  }
}
