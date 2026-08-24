/**
 * Splendor Quantum Wallet — Background Service Worker
 *
 * Handles: key management, Dilithium signing (via WASM), RPC calls to SQC node,
 * popup ↔ content script communication, encrypted storage.
 *
 * Key architecture:
 *  - Keys stored AES-GCM encrypted in chrome.storage.local
 *  - Dilithium signing done in an offscreen document (WASM)
 *  - RPC node URL configurable (default: mainnet)
 */

'use strict';

// ── Constants ──────────────────────────────────────────────────────────────
const STORAGE_KEY = 'sqc_vault';
const SESSION_KEY = 'sqc_session';
const DEFAULT_RPC = 'https://rpc.splendor.network'; // change to your node
const LOCK_TIMEOUT_MS = 15 * 60 * 1000; // 15 minutes

// ── State (in-memory, cleared on extension reload) ─────────────────────────
let _session = null; // { address, dilithiumPub, dilithiumPrivRaw (bytes) }
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

// ── WASM Dilithium interface ────────────────────────────────────────────────
// The WASM module exports: generate(), sign(privBytes, msgBytes), verify()
// We call it via a dedicated offscreen page to avoid MV3 CSP issues.

let _wasmReady = false;
let _wasmModule = null;

async function loadWasm() {
  if (_wasmReady) return;
  // In MV3 service workers we can use WebAssembly directly
  const url = chrome.runtime.getURL('wasm/sqc_signer.wasm');
  const execUrl = chrome.runtime.getURL('wasm/wasm_exec.js');

  // Load wasm_exec.js runtime
  await import(execUrl);
  const go = new Go(); // from wasm_exec.js
  const bytes = await fetch(url).then(r => r.arrayBuffer());
  const result = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(result.instance);
  _wasmModule = globalThis.sqcSigner; // exported by wasm/main.go
  _wasmReady = true;
}

async function dilithiumGenerate() {
  await loadWasm();
  return _wasmModule.generate(); // { publicKey: Uint8Array, privateKey: Uint8Array }
}

async function dilithiumSign(privBytes, msgBytes) {
  await loadWasm();
  return _wasmModule.sign(privBytes, msgBytes); // Uint8Array (signature)
}

function bytesToHex(bytes) {
  return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
}

function hexToBytes(hex) {
  const bytes = new Uint8Array(hex.length / 2);
  for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(hex.slice(i*2, i*2+2), 16);
  return bytes;
}

// ── Address derivation ─────────────────────────────────────────────────────
// Address = "SQ:" + hex(SHA3-256(dilithium_pubkey)[0:32])
// Mirroring core/crypto/dilithium.go DeriveAddress()

async function deriveAddress(pubKeyBytes) {
  const hash = await crypto.subtle.digest('SHA-256', pubKeyBytes); // SHA-256 (stdlib)
  // Note: the Go node uses SHA3-256 — in a real build use a SHA3 library.
  // For the wallet, use the WASM module which has the correct implementation.
  if (_wasmModule && _wasmModule.deriveAddress) {
    return _wasmModule.deriveAddress(pubKeyBytes);
  }
  // Fallback: hex of first 32 bytes of SHA-256 (close enough for dev)
  const hex = bytesToHex(new Uint8Array(hash));
  return `SQ:${hex}`;
}

// ── Vault operations ───────────────────────────────────────────────────────

async function createWallet(password) {
  const kp = await dilithiumGenerate();
  const address = await deriveAddress(kp.publicKey);
  const vaultData = {
    address,
    publicKey: bytesToHex(kp.publicKey),
    privateKey: bytesToHex(kp.privateKey),
    createdAt: Date.now(),
  };
  const vault = await encrypt(vaultData, password);
  await chrome.storage.local.set({ [STORAGE_KEY]: vault, sqc_address: address });
  return { address, publicKey: bytesToHex(kp.publicKey) };
}

async function unlockWallet(password) {
  const store = await chrome.storage.local.get([STORAGE_KEY]);
  if (!store[STORAGE_KEY]) throw new Error('No wallet found');
  const data = await decrypt(store[STORAGE_KEY], password);
  _session = {
    address: data.address,
    publicKeyHex: data.publicKey,
    privateKeyHex: data.privateKey,
  };
  resetLockTimer();
  return { address: data.address };
}

function lockWallet() {
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

  const tx = {
    type: 1, // TxTransfer
    from: _session.address,
    to,
    amount: amountUnits,
    fee,
    nonce,
    timestamp: Math.floor(Date.now() / 1000),
    data: memo ? Array.from(new TextEncoder().encode(memo)) : [],
  };

  // Sign: hash the canonical JSON representation
  const msgBytes = new TextEncoder().encode(JSON.stringify(tx));
  const privBytes = hexToBytes(_session.privateKeyHex);
  const sig = await dilithiumSign(privBytes, msgBytes);

  return {
    ...tx,
    signature: bytesToHex(sig),
    public_key: _session.publicKeyHex,
  };
}

async function buildAndSignModuleCall({ module: mod, action, params: mcParams, fee = '1000' }) {
  if (!isUnlocked()) throw new Error('Wallet locked');

  const nonceResp = await rpc('GET', `/nonce/${_session.address}`);
  const nonce = nonceResp.nonce || 0;

  // Module router address (SHA3-256 of "splendor:module-router:v1")
  // Pre-computed; must match vm/module_router.go deriveModuleRouterAddress()
  const MODULE_ROUTER = 'SQ:module-router-address-here'; // TODO: compute from WASM

  const data = JSON.stringify({ module: mod, action, params: mcParams });
  const tx = {
    type: 2, // TxCall
    from: _session.address,
    to: MODULE_ROUTER,
    amount: '0',
    fee,
    nonce,
    timestamp: Math.floor(Date.now() / 1000),
    data: Array.from(new TextEncoder().encode(data)),
  };

  const msgBytes = new TextEncoder().encode(JSON.stringify(tx));
  const privBytes = hexToBytes(_session.privateKeyHex);
  const sig = await dilithiumSign(privBytes, msgBytes);

  return {
    ...tx,
    signature: bytesToHex(sig),
    public_key: _session.publicKeyHex,
  };
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
      if (!isUnlocked()) return err(4100, 'Wallet locked');
      const msgHex = params?.[0];
      const msgBytes = hexToBytes(msgHex);
      const privBytes = hexToBytes(_session.privateKeyHex);
      const sig = await dilithiumSign(privBytes, msgBytes);
      return res(bytesToHex(sig));
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
