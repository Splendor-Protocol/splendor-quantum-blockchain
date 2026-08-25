/**
 * Splendor Quantum Wallet — Popup Controller
 */
'use strict';

// ── Helpers ────────────────────────────────────────────────────────────────

const $ = id => document.getElementById(id);
let _state = {};
let _approvalId = null;

function bg(action, extra = {}) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendMessage({ __sqc_popup: true, action, ...extra }, (r) => {
      if (chrome.runtime.lastError) return reject(new Error(chrome.runtime.lastError.message));
      if (r && r.error) {
        const msg = typeof r.error === 'string' ? r.error
          : (r.error.message || r.error.msg || JSON.stringify(r.error));
        return reject(new Error(msg));
      }
      resolve(r);
    });
  });
}

// rpcCall: sends a provider method request (not a popup action)
// Uses __sqc_req: true so background routes it through the method switch
function rpcCall(method, params = []) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendMessage({ __sqc_req: true, method, params }, (r) => {
      if (chrome.runtime.lastError) return reject(new Error(chrome.runtime.lastError.message));
      if (r && r.error) {
        const msg = typeof r.error === 'string' ? r.error
          : (r.error.message || r.error.msg || JSON.stringify(r.error));
        return reject(new Error(msg));
      }
      resolve(r?.result ?? r);
    });
  });
}

function showPage(id) {
  document.querySelectorAll('.page').forEach(p => p.classList.remove('active'));
  $(id).classList.add('active');
}

function spld(units) {
  if (!units) return '0.000000';
  const n = BigInt(units);
  const div = BigInt('1000000000000000000');
  const whole = n / div;
  const frac = (n % div).toString().padStart(18, '0').slice(0, 6);
  return `${whole}.${frac}`;
}

function shortAddr(addr) {
  if (!addr || addr.length < 20) return addr;
  return addr.slice(0, 12) + '...' + addr.slice(-8);
}

// ── Page routing ──────────────────────────────────────────────────────────

async function init() {
  _state = await bg('get_state');

  // Check URL param for approval
  const params = new URLSearchParams(location.search);
  _approvalId = params.get('approval') ? parseInt(params.get('approval')) : null;

  if (_state.pendingApproval || _approvalId) {
    const [id, entry] = _state.pendingApproval || [_approvalId, null];
    renderApproval(id, entry);
    return;
  }

  if (!_state.hasWallet) {
    showPage('page-setup');
    return;
  }

  if (!_state.unlocked) {
    showPage('page-unlock');
    return;
  }

  showPage('page-main');
  renderMain();
}

// ── Setup page ────────────────────────────────────────────────────────────

$('btnCreate').onclick = async () => {
  const pw = $('setup-pw').value;
  const pw2 = $('setup-pw2').value;
  $('setup-err').textContent = '';

  if (pw.length < 8) { $('setup-err').textContent = 'Password must be at least 8 characters'; return; }
  if (pw !== pw2) { $('setup-err').textContent = 'Passwords do not match'; return; }

  $('btnCreate').disabled = true;
  $('btnCreate').textContent = 'Generating ML-DSA-65 keys…';

  try {
    const info = await bg('create_wallet', { password: pw });
    _state.address = info.address;
    _state.unlocked = true;
    _state.hasWallet = true;
    showPage('page-main');
    renderMain();
  } catch (e) {
    $('setup-err').textContent = e.message;
    $('btnCreate').disabled = false;
    $('btnCreate').textContent = 'Create Wallet';
  }
};

// ── Unlock page ───────────────────────────────────────────────────────────

$('unlock-pw').addEventListener('keydown', e => { if (e.key === 'Enter') $('btnUnlock').click(); });

$('btnUnlock').onclick = async () => {
  $('unlock-err').textContent = '';
  const pw = $('unlock-pw').value;
  $('btnUnlock').disabled = true;

  try {
    const info = await bg('unlock', { password: pw });
    _state.address = info.address;
    _state.unlocked = true;
    showPage('page-main');
    renderMain();
  } catch (e) {
    $('unlock-err').textContent = 'Incorrect password';
    $('unlock-pw').value = '';
  } finally {
    $('btnUnlock').disabled = false;
  }
};

// ── Main page ─────────────────────────────────────────────────────────────

async function renderMain() {
  $('btnLock').style.display = '';
  const addr = _state.address || '';
  $('main-addr').textContent = shortAddr(addr);
  $('main-addr').title = addr;

  // Load settings for explorer link
  const settings = await bg('get_settings');
  const rpc = settings.sqc_rpc || 'https://rpc.splendor.network';
  $('explorer-link').href = rpc + '/status';

  // Load balance
  await refreshBalance();
}

async function refreshBalance() {
  try {
    const result = await rpcCall('sqc_getBalance', [_state.address]);
    $('main-balance').textContent = spld(result || '0');
  } catch (e) {
    $('main-balance').textContent = '—';
  }
}

$('btnLock').onclick = async () => {
  await bg('lock');
  $('btnLock').style.display = 'none';
  showPage('page-unlock');
};

$('btnCopyAddr').onclick = () => {
  navigator.clipboard.writeText(_state.address || '');
  $('btnCopyAddr').textContent = '✓ Copied!';
  setTimeout(() => { $('btnCopyAddr').textContent = '📋 Copy address'; }, 1500);
};

$('btnRefresh').onclick = refreshBalance;

// Tabs
document.querySelectorAll('.tab').forEach(tab => {
  tab.onclick = () => {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    tab.classList.add('active');
    ['send', 'activity', 'tokens'].forEach(t => {
      const el = $('tab-' + t);
      if (el) el.style.display = t === tab.dataset.tab ? '' : 'none';
    });
  };
});

// Send
$('btnSend').onclick = async () => {
  $('send-err').textContent = '';
  const to = $('send-to').value.trim();
  const amountSPLD = $('send-amount').value.trim();
  const fee = $('send-fee').value.trim() || '1000';

  if (!to.startsWith('SQ:')) { $('send-err').textContent = 'Invalid SQ: address'; return; }
  if (!amountSPLD || isNaN(parseFloat(amountSPLD))) { $('send-err').textContent = 'Invalid amount'; return; }

  // Convert SPLD to units (× 10^18)
  const units = BigInt(Math.floor(parseFloat(amountSPLD) * 1e6)) * BigInt(1e12);
  $('btnSend').disabled = true;
  $('btnSend').textContent = 'Confirming…';

  try {
    const result = await bg('sqc_sendTransaction', {
      __sqc_req: true,
      method: 'sqc_sendTransaction',
      params: [{ to, amount: units.toString(), fee, amountSPLD }],
    });
    if (result.error) { $('send-err').textContent = result.error.message; return; }
    $('send-err').style.color = '#3dba7a';
    $('send-err').textContent = `✓ Sent! TX: ${String(result.result).slice(0, 16)}…`;
    $('send-to').value = '';
    $('send-amount').value = '';
    setTimeout(refreshBalance, 3000);
  } catch (e) {
    $('send-err').textContent = e.message;
  } finally {
    $('btnSend').disabled = false;
    $('btnSend').textContent = 'Send SPLD';
    $('send-err').style.color = '';
  }
};

// Deploy token (opens a simple dialog)
$('btnDeployToken').onclick = () => {
  const name = prompt('Token name (e.g. My Token):');
  if (!name) return;
  const symbol = prompt('Token symbol (e.g. MTK):');
  if (!symbol) return;
  const supply = prompt('Total supply (in SPLD units, e.g. 1000000000000000000000000 = 1M tokens):');
  if (!supply) return;

  bg('sqc_moduleCall', {
    __sqc_req: true,
    method: 'sqc_moduleCall',
    params: [{ module: 'qrc20', action: 'deploy', params: {
      name, symbol, decimals: 18, total_supply: supply
    }}],
  }).then(r => {
    if (r.error) { alert('Error: ' + r.error.message); return; }
    alert(`Token deployed!\nContract: ${r.result?.contractAddress}`);
  }).catch(e => alert('Error: ' + e.message));
};

// Settings
$('btnSettings').onclick = async () => {
  const s = await bg('get_settings');
  $('settings-rpc').value = s.sqc_rpc || '';
  showPage('page-settings');
};

$('btnSaveSettings').onclick = async () => {
  await bg('save_settings', { rpcUrl: $('settings-rpc').value.trim() });
  showPage('page-main');
  renderMain();
};

$('btnBackMain').onclick = () => { showPage('page-main'); };

$('btnReset').onclick = () => {
  if (!confirm('This will permanently delete your wallet keys. Make sure you have a backup!')) return;
  chrome.storage.local.clear(() => { location.reload(); });
};

// ── Approval page ─────────────────────────────────────────────────────────

function renderApproval(id, entry) {
  showPage('page-approval');
  _approvalId = id;

  if (!entry) {
    // Get from background
    bg('get_state').then(s => {
      if (s.pendingApproval) {
        const [aid, aentry] = s.pendingApproval;
        _approvalId = aid;
        renderApprovalEntry(aentry);
      }
    });
    return;
  }
  renderApprovalEntry(entry);
}

function renderApprovalEntry(entry) {
  const type = entry?.[1]?.type || 'connect';
  const payload = entry?.[1]?.payload || {};

  switch (type) {
    case 'connect':
      $('approval-title').textContent = '🔗 Connection Request';
      $('approval-body').innerHTML = `
        <div class="approval-detail">Site wants to connect your wallet</div>
        <div class="approval-value">${payload.origin || 'unknown site'}</div>
        <div class="approval-detail" style="margin-top:8px">Your address will be shared:</div>
        <div class="approval-value">${shortAddr(_state.address)}</div>
      `;
      break;
    case 'send':
      $('approval-title').textContent = '💸 Send SPLD';
      $('approval-body').innerHTML = `
        <div class="approval-detail">To</div>
        <div class="approval-value">${shortAddr(payload.to)}</div>
        <div class="approval-detail" style="margin-top:8px">Amount</div>
        <div class="approval-value" style="font-size:20px;color:var(--accent)">${spld(payload.amount)} SPLD</div>
        <div class="approval-detail" style="margin-top:4px">Fee: ${payload.fee || '1000'} units</div>
      `;
      break;
    case 'moduleCall':
      $('approval-title').textContent = '⚙ Module Call';
      $('approval-body').innerHTML = `
        <div class="approval-detail">Module: ${payload.module} / ${payload.action}</div>
        <pre style="background:#0d1710;padding:8px;border-radius:6px;font-size:11px;overflow:auto;max-height:120px">${JSON.stringify(payload.params, null, 2)}</pre>
      `;
      break;
  }
}

$('btnApprove').onclick = async () => {
  await bg('approve', { approvalId: _approvalId, data: true });
  window.close();
};

$('btnReject').onclick = async () => {
  await bg('reject', { approvalId: _approvalId });
  window.close();
};

// ── Boot ───────────────────────────────────────────────────────────────────
init().catch(console.error);
