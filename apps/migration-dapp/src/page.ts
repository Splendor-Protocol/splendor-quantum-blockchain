/**
 * The whole frontend: one self-contained HTML document with inline CSS and JS.
 *
 * No external resources at all — no fonts, no CSS frameworks, no bundler, no
 * web3 library. The palette mirrors the Splendor Quantum explorer (ink #070709
 * / surface #121215 / surface-soft #0b0b0e / paper #f5f6f9 / muted #a6a9b6 /
 * line rgba(255,255,255,.09) / acid #c8ff63 / violet #936dff / cyan #66e7ff /
 * danger #ff8066) so the properties read as one system — same precedent the
 * faucet set.
 *
 * Every chain interaction goes through the user's injected wallet provider
 * (window.ethereum); the page itself only ever fetches same-origin /api/*.
 * Contract and token addresses come EXCLUSIVELY from /api/info — nothing is
 * hardcoded here except the 4-byte selectors, which are pure functions of the
 * contract ABI and pinned by test/selectors.test.ts.
 */

export interface PageOptions {
  nonce: string;
}

export function renderPage(opts: PageOptions): string {
  return `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Splendor Quantum Migration</title>
<meta name="description" content="Burn SPLD or SPLV on Splendor Mainnet and receive the same amount on the Splendor Quantum chain.">
<meta name="color-scheme" content="dark">
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' fill='%23070709'/%3E%3Cpath d='M9 21 16 9l7 12h-4.6L16 17l-2.4 4z' fill='%23c8ff63'/%3E%3C/svg%3E">
<style>
*{box-sizing:border-box}
:root{
  --ink:#070709; --surface:#121215; --surface-soft:#0b0b0e; --paper:#f5f6f9;
  --muted:#a6a9b6; --dim:#7c7f8c; --line:rgba(255,255,255,.09);
  --acid:#c8ff63; --violet:#936dff; --cyan:#66e7ff; --danger:#ff8066;
  --mono:ui-monospace,SFMono-Regular,"SF Mono",Menlo,Consolas,"Liberation Mono",monospace;
  --sans:ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
}
html{background:var(--ink)}
body{margin:0;min-height:100vh;color:var(--paper);font-family:var(--sans);-webkit-font-smoothing:antialiased;
  background:radial-gradient(circle at 80% -4%,rgba(147,109,255,.12),transparent 30%),
    radial-gradient(circle at 4% 40%,rgba(200,255,99,.045),transparent 30%),
    linear-gradient(180deg,var(--ink),#09090c 45%,var(--ink))}
a{color:inherit;text-decoration:none}
:focus-visible{outline:2px solid var(--acid);outline-offset:3px;border-radius:2px}
[hidden]{display:none!important}

/* ── topbar ─────────────────────────────────────────────────────────────── */
.topbar{height:76px;display:flex;align-items:center;gap:18px;padding:0 clamp(20px,5vw,78px);
  border-bottom:1px solid var(--line);background:linear-gradient(180deg,rgba(255,255,255,.016),transparent)}
.brand{display:flex;flex-direction:column;gap:4px;margin-right:auto}
.brand b{font:600 15px/1 var(--mono);letter-spacing:.34em;text-transform:uppercase}
.brand span{color:#8f929f;font:9px/1 var(--mono);letter-spacing:.28em;text-transform:uppercase;padding-left:1px}
.network-pill{display:flex;align-items:center;gap:7px;padding:7px 11px;border:1px solid var(--line);
  border-radius:999px;font:11px var(--mono);text-transform:uppercase;letter-spacing:.08em;color:#999991;white-space:nowrap}
.network-pill i{width:8px;height:8px;border-radius:50%;background:var(--acid);
  box-shadow:0 0 8px rgba(200,255,99,.55);flex:none}
.network-pill.down i{background:var(--danger);box-shadow:0 0 8px rgba(255,128,102,.55)}

/* ── hero ───────────────────────────────────────────────────────────────── */
.hero{padding:clamp(52px,8vw,96px) clamp(20px,7vw,110px) clamp(44px,6vw,64px);border-bottom:1px solid var(--line)}
.eyebrow{display:flex;align-items:center;gap:12px;margin:0 0 22px;color:var(--acid);
  font:11px var(--mono);text-transform:uppercase;letter-spacing:.16em}
.eyebrow::before{content:"";width:28px;height:1px;background:var(--acid)}
h1{margin:0;font-size:clamp(46px,7vw,98px);line-height:.9;letter-spacing:-.07em;font-weight:570;max-width:900px}
h1 em{color:transparent;-webkit-text-stroke:1px rgba(216,220,231,.58);font-style:normal}
.hero p{max-width:600px;margin:28px 0 0;color:#aaa9a3;font-size:clamp(15px,1.3vw,18px);line-height:1.6;letter-spacing:-.015em}
.hero p code{font:.86em var(--mono);color:var(--paper);background:rgba(255,255,255,.06);padding:1px 5px;border-radius:3px}
.hero-warn{display:inline-flex;align-items:center;gap:10px;margin-top:22px;padding:9px 13px;
  border:1px solid rgba(255,128,102,.28);background:rgba(255,128,102,.04);color:var(--danger);
  font:10px var(--mono);text-transform:uppercase;letter-spacing:.1em;border-radius:4px}

/* ── metrics strip ──────────────────────────────────────────────────────── */
.metrics{display:grid;grid-template-columns:repeat(4,1fr);padding:0 clamp(20px,5vw,78px);border-bottom:1px solid var(--line)}
.metrics>div{min-height:124px;display:flex;flex-direction:column;justify-content:center;padding:22px 26px;border-left:1px solid var(--line)}
.metrics>div:last-child{border-right:1px solid var(--line)}
.metrics span{color:#909088;font:9px var(--mono);text-transform:uppercase;letter-spacing:.12em}
.metrics strong{margin:8px 0 6px;font-size:clamp(22px,2.4vw,34px);letter-spacing:-.05em;font-weight:520;font-variant-numeric:tabular-nums}
.metrics small{color:#a0a099;font:10px var(--mono)}
.metrics .paused strong{color:var(--danger)}

/* ── sections ───────────────────────────────────────────────────────────── */
.section{padding:clamp(40px,6vw,64px) clamp(20px,5vw,78px) clamp(56px,7vw,84px);border-bottom:1px solid var(--line)}
.section-head{margin-bottom:28px}
.section-head h2{margin:10px 0 0;font-size:clamp(24px,2.6vw,32px);font-weight:520;letter-spacing:-.04em}
.section-inner{max-width:860px}

/* ── migration card ─────────────────────────────────────────────────────── */
.card{border:1px solid var(--line);border-radius:7px;background:linear-gradient(180deg,rgba(255,255,255,.022),transparent);overflow:hidden}
.step{padding:clamp(20px,3.4vw,28px);border-bottom:1px solid var(--line)}
.step:last-child{border-bottom:0}
.step-head{display:flex;align-items:center;justify-content:space-between;gap:14px;margin-bottom:16px}
.step-tag{color:var(--acid);font:10px var(--mono);text-transform:uppercase;letter-spacing:.2em}
.step.locked{opacity:.38;pointer-events:none}
.step.locked .step-tag{color:var(--dim)}
label,.legend{display:block;margin-bottom:9px;color:var(--dim);font:10px var(--mono);letter-spacing:.16em;text-transform:uppercase}
input[type=text]{width:100%;padding:14px 15px;border:1px solid var(--line);border-radius:5px;
  background:var(--surface-soft);color:var(--paper);font:13.5px var(--mono);letter-spacing:.01em}
input[type=text]::placeholder{color:#5d606b}
input[type=text]:focus{outline:none;border-color:var(--cyan)}
input[type=text][aria-invalid=true]{border-color:var(--danger)}
.hint{margin:8px 0 0;color:var(--dim);font:11px var(--mono);line-height:1.55}
.hint.bad{color:var(--danger)}
.hint.good{color:var(--acid)}
button{border:0;cursor:pointer;font-family:var(--sans)}
button:disabled{opacity:.42;cursor:not-allowed}
.btn{width:100%;padding:15px 18px;border-radius:5px;background:var(--acid);color:#0a0c05;
  font:600 14px var(--sans);letter-spacing:-.01em;transition:filter .15s,opacity .15s}
.btn:hover:not(:disabled){filter:brightness(1.08)}
.btn.danger{background:var(--danger);color:#170703}
.mini{width:auto;padding:8px 12px;border:1px solid var(--line);border-radius:5px;background:transparent;
  color:var(--muted);font:10px var(--mono);letter-spacing:.14em;text-transform:uppercase;transition:border-color .15s,color .15s}
.mini:hover:not(:disabled){border-color:var(--violet);color:var(--paper)}
.register{margin:0;padding:0;list-style:none;display:grid;gap:1px;border:1px solid var(--line);border-radius:5px;overflow:hidden;background:var(--line)}
.register li{display:flex;justify-content:space-between;align-items:baseline;gap:16px;padding:11px 14px;background:var(--surface-soft);font:11px var(--mono)}
.register .k{color:var(--dim);letter-spacing:.1em;text-transform:uppercase;font-size:9px;white-space:nowrap}
.register .v{color:var(--paper);text-align:right;overflow-wrap:anywhere;font-variant-numeric:tabular-nums}
.register .v.acid{color:var(--acid)}
.acct-row{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.acct-chip{display:inline-flex;align-items:center;gap:8px;padding:8px 12px;border:1px solid rgba(200,255,99,.28);
  border-radius:4px;background:rgba(200,255,99,.05);font:11px var(--mono);color:var(--paper);overflow-wrap:anywhere}
.acct-chip::before{content:"";width:7px;height:7px;border-radius:50%;background:var(--acid);box-shadow:0 0 8px var(--acid);flex:none}
.seg{display:grid;grid-template-columns:1fr 1fr;gap:8px}
.seg input{position:absolute;opacity:0;width:0;height:0}
.seg label{display:block;margin:0;padding:13px 14px;border:1px solid var(--line);border-radius:5px;
  background:var(--surface-soft);color:var(--muted);cursor:pointer;text-transform:none;letter-spacing:0;
  font:400 14px/1.25 var(--sans);transition:border-color .15s,color .15s,background .15s}
.seg label small{display:block;margin-top:5px;color:var(--dim);font:10px var(--mono);letter-spacing:.1em;text-transform:uppercase}
.seg label:hover{border-color:rgba(255,255,255,.2);color:var(--paper)}
.seg input:checked+label{border-color:var(--acid);color:var(--paper);background:rgba(200,255,99,.06)}
.seg input:checked+label small{color:var(--acid)}
.seg input:focus-visible+label{outline:2px solid var(--acid);outline-offset:3px}
.amount-row{display:grid;grid-template-columns:1fr auto;gap:8px}
.field{margin-bottom:20px}
.field:last-child{margin-bottom:0}
.review{margin-bottom:18px}
.consent{display:flex;gap:11px;align-items:flex-start;margin:0 0 18px;padding:13px 14px;
  border:1px solid rgba(255,128,102,.3);border-radius:5px;background:rgba(255,128,102,.04)}
.consent input{width:15px;height:15px;margin:2px 0 0;accent-color:var(--danger);flex:none}
.consent span{color:#d8b3a8;font-size:12.5px;line-height:1.55}
.consent b{color:var(--danger);font-weight:600}
.out{margin-top:16px;padding:14px 16px;border:1px solid var(--line);border-radius:6px;background:var(--surface-soft);font-size:13px;line-height:1.6}
.out.ok{border-color:rgba(200,255,99,.4);background:rgba(200,255,99,.05)}
.out.err{border-color:rgba(255,128,102,.4);background:rgba(255,128,102,.05)}
.out .t{margin:0 0 7px;font:10px var(--mono);letter-spacing:.16em;text-transform:uppercase}
.out.ok .t{color:var(--acid)}
.out.err .t{color:var(--danger)}
.out p{margin:0 0 6px}
.out p:last-child{margin:0}

/* ── status timeline ────────────────────────────────────────────────────── */
.timeline{margin:0;padding:0;list-style:none;border:1px solid var(--line);border-radius:7px;overflow:hidden}
.timeline li{position:relative;display:flex;gap:16px;padding:18px 20px 18px 22px;border-bottom:1px solid var(--line);background:var(--surface-soft)}
.timeline li:last-child{border-bottom:0}
.timeline i{position:relative;width:11px;height:11px;margin-top:3px;border-radius:50%;border:1px solid var(--dim);flex:none;font-style:normal}
.timeline li.done i{background:var(--acid);border-color:var(--acid);box-shadow:0 0 9px rgba(200,255,99,.5)}
.timeline li.active i{border-color:var(--cyan);background:var(--cyan);box-shadow:0 0 9px rgba(102,231,255,.6);animation:pulse 1.6s ease-in-out infinite}
.timeline li.bad i{background:var(--danger);border-color:var(--danger)}
.timeline b{display:block;font:11px var(--mono);letter-spacing:.14em;text-transform:uppercase;color:var(--paper)}
.timeline li:not(.done):not(.active):not(.bad) b{color:var(--dim)}
.timeline span{display:block;margin-top:5px;color:var(--muted);font:11.5px var(--mono);line-height:1.55;overflow-wrap:anywhere}
.timeline a{color:var(--cyan);text-decoration:underline;text-underline-offset:3px}
.timeline .bar{height:3px;margin-top:9px;max-width:340px;background:#242426;border-radius:2px;overflow:hidden}
.timeline .bar b{display:block;height:100%;background:linear-gradient(90deg,var(--violet),var(--acid));transition:width .5s}
.track-foot{display:flex;align-items:center;justify-content:space-between;gap:14px;margin-top:14px;flex-wrap:wrap}
.track-foot p{margin:0;color:var(--dim);font:10px var(--mono);letter-spacing:.06em}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.35}}

/* ── FAQ ────────────────────────────────────────────────────────────────── */
.faq{border-top:1px solid var(--line)}
.faq details{border-bottom:1px solid var(--line)}
.faq summary{display:flex;align-items:center;gap:12px;padding:17px 4px;cursor:pointer;list-style:none;
  color:var(--paper);font-size:14.5px;letter-spacing:-.01em}
.faq summary::-webkit-details-marker{display:none}
.faq summary::before{content:"+";color:var(--acid);font:15px var(--mono);width:14px;flex:none}
.faq details[open] summary::before{content:"\\2212"}
.faq div{padding:0 4px 18px 26px;color:#9a9a94;font-size:13px;line-height:1.7;max-width:70ch}
.faq code{font:.9em var(--mono);color:var(--paper);background:rgba(255,255,255,.06);padding:1px 5px;border-radius:3px}
.faq b{color:var(--danger);font-weight:600}

/* ── footer ─────────────────────────────────────────────────────────────── */
footer{padding:34px clamp(20px,5vw,78px) 44px;color:var(--dim);font:11px var(--mono);line-height:1.9}
footer a{color:var(--muted);text-decoration:underline;text-underline-offset:3px}
footer a:hover{color:var(--acid)}
.sep{color:var(--line);padding:0 8px}

@media (max-width:900px){.metrics{grid-template-columns:1fr 1fr}
  .metrics>div:nth-child(3),.metrics>div:nth-child(4){border-top:1px solid var(--line)}}
@media (max-width:560px){.topbar{height:64px;padding-inline:16px}.brand b{font-size:13px}
  .hero{padding:40px 16px 36px}.metrics{padding:0}.metrics>div{min-height:100px;padding:16px 14px}
  .section{padding:34px 16px 48px}.seg,.amount-row{grid-template-columns:1fr}
  .register li{flex-direction:column;gap:4px}.register .v{text-align:left}}
@media (prefers-reduced-motion:reduce){*{transition:none!important;animation:none!important}}
</style>
</head>
<body>
<header class="topbar">
  <div class="brand"><b>Splendor</b><span>Quantum Migration</span></div>
  <div class="network-pill" id="pill"><i></i><span id="pill-t">Connecting&#8230;</span></div>
</header>

<section class="hero">
  <p class="eyebrow">One-way bridge &#183; Splendor Mainnet &#8594; Quantum</p>
  <h1>Burn legacy.<br>Arrive <em>quantum</em>.</h1>
  <p>Burn SPLD or SPLV on Splendor Mainnet (chain 2691) and the bridge credits the
    same amount to your post-quantum <code>SQ:</code> address &#8212; SPLD as the native
    coin, SPLV as a QRC-20. Minimum 1 token per burn. Typical end-to-end time:
    2&#8211;5 minutes.</p>
  <span class="hero-warn">Irreversible &#8212; burned tokens can never return to the legacy chain</span>
</section>

<section class="metrics" aria-label="Bridge metrics">
  <div><span>Escrow pool</span><strong id="m-pool">&#8212;</strong><small>SPLD available to claim</small></div>
  <div><span>SPLD claimed</span><strong id="m-spld">&#8212;</strong><small>credited on quantum</small></div>
  <div><span>SPLV claimed</span><strong id="m-splv">&#8212;</strong><small>minted on quantum</small></div>
  <div><span>Confirmation depth</span><strong id="m-conf">&#8212;</strong><small>blocks before claim</small></div>
</section>

<section class="section">
  <div class="section-head">
    <p class="eyebrow">Migrate</p>
    <h2>Four steps, one direction</h2>
  </div>
  <div class="section-inner">
  <div class="out err" id="cfg-out" hidden><p class="t">Bridge offline</p><p id="cfg-msg"></p></div>
  <form class="card" id="mig" novalidate>

    <div class="step" id="s1">
      <div class="step-head"><span class="step-tag">01 / Connect wallet</span></div>
      <div class="acct-row" id="acct-off">
        <button type="button" class="btn" id="connect" style="width:auto;padding:13px 22px">Connect MetaMask</button>
        <p class="hint" id="connect-hint">Your wallet signs on Splendor Mainnet (chain id 2691, hex 0xa83).</p>
      </div>
      <div id="acct-on" hidden>
        <div class="acct-row" style="margin-bottom:14px">
          <span class="acct-chip" id="acct-addr"></span>
          <button type="button" class="mini" id="switch-net" hidden>Switch to Splendor Mainnet</button>
        </div>
        <ul class="register">
          <li><span class="k">SPLD balance</span><span class="v" id="bal-spld">&#8212;</span></li>
          <li><span class="k">SPLV balance</span><span class="v" id="bal-splv">&#8212;</span></li>
        </ul>
      </div>
    </div>

    <div class="step locked" id="s2">
      <div class="step-head"><span class="step-tag">02 / Destination on quantum</span></div>
      <div class="field">
        <label for="sq">Your SQ: address</label>
        <input type="text" id="sq" spellcheck="false" autocomplete="off" autocapitalize="none"
               autocorrect="off" placeholder="SQ:0000&#8230;" aria-describedby="sq-hint">
        <p class="hint" id="sq-hint">Copy it from the Splendor Wallet app &#8594; Quantum tab. SQ: followed by 64 hex characters.</p>
      </div>
    </div>

    <div class="step locked" id="s3">
      <div class="step-head"><span class="step-tag">03 / Asset &amp; amount</span></div>
      <fieldset class="field" style="border:0;margin:0 0 20px;padding:0">
        <span class="legend">Asset</span>
        <div class="seg">
          <input type="radio" name="asset" id="a-spld" value="spld" checked>
          <label for="a-spld">SPLD<small>Native coin &#8594; native coin</small></label>
          <input type="radio" name="asset" id="a-splv" value="splv">
          <label for="a-splv">SPLV<small>ERC-20 &#8594; QRC-20</small></label>
        </div>
      </fieldset>
      <div class="field">
        <label for="amt">Amount</label>
        <div class="amount-row">
          <input type="text" id="amt" inputmode="decimal" spellcheck="false" autocomplete="off"
                 placeholder="1.0" aria-describedby="amt-hint">
          <button type="button" class="mini" id="max">Max</button>
        </div>
        <p class="hint" id="amt-hint">Minimum 1 token per burn. 18 decimals.</p>
      </div>
    </div>

    <div class="step locked" id="s4">
      <div class="step-head"><span class="step-tag">04 / Review &amp; burn</span></div>
      <ul class="register review">
        <li><span class="k">Action</span><span class="v" id="rv-act">&#8212;</span></li>
        <li><span class="k">From</span><span class="v" id="rv-from">&#8212;</span></li>
        <li><span class="k">Credited to</span><span class="v acid" id="rv-to">&#8212;</span></li>
        <li><span class="k">Burn destination</span><span class="v">0x&#8230;dEaD (unrecoverable)</span></li>
      </ul>
      <label class="consent" style="margin-bottom:18px;text-transform:none;letter-spacing:0">
        <input type="checkbox" id="ack">
        <span><b>Funds burned on Splendor Mainnet are irreversible.</b>
        They will be credited to exactly this quantum address. I have verified the
        <span style="font-family:var(--mono)" id="ack-addr">SQ: address</span> above belongs to me.</span>
      </label>
      <button type="submit" class="btn danger" id="go" disabled>Burn &amp; migrate</button>
      <div class="out" id="tx-out" hidden role="status" aria-live="polite"></div>
    </div>
  </form>
  </div>
</section>

<section class="section" id="track-sec" hidden>
  <div class="section-head">
    <p class="eyebrow">Status</p>
    <h2>Migration in flight</h2>
  </div>
  <div class="section-inner">
    <ol class="timeline" id="tl">
      <li id="tl0"><i></i><div><b>Burn sent</b><span id="tl0-d">&#8212;</span></div></li>
      <li id="tl1"><i></i><div><b id="tl1-t">Confirmations</b><span id="tl1-d">&#8212;</span>
        <div class="bar"><b id="tl1-bar" style="width:0%"></b></div></div></li>
      <li id="tl2"><i></i><div><b>Claim in flight</b><span id="tl2-d">The bridge operator submits the claim on the quantum chain.</span></div></li>
      <li id="tl3"><i></i><div><b>Credited</b><span id="tl3-d">&#8212;</span></div></li>
    </ol>
    <div class="track-foot">
      <p id="track-note">Status refreshes every 6 seconds. Safe to close this page &#8212; the burn is on-chain and resumes here on reload.</p>
      <button type="button" class="mini" id="track-reset">Start another migration</button>
    </div>
  </div>
</section>

<section class="section">
  <div class="section-head">
    <p class="eyebrow">Notes</p>
    <h2>Before you burn</h2>
  </div>
  <div class="section-inner faq">
    <details>
      <summary>Is migration reversible?</summary>
      <div><b>No.</b> The burn contract forwards your tokens straight to
      <code>0x&#8230;dEaD</code> and holds nothing, ever. There is no admin withdraw, no
      rescue function and no reverse bridge. Once burned, value exists only as the
      credit on the Splendor Quantum chain.</div>
    </details>
    <details>
      <summary>Is there a per-burn limit?</summary>
      <div>Yes. Each burn is between 1 token and a per-burn maximum (shown under
      the amount field once your wallet is connected). The maximum equals the
      bridge's claim ceiling, so a larger burn could never be credited &#8212;
      it is refused on-chain. Migrate a large balance as several burns.</div>
    </details>
    <details>
      <summary>Why at least 1 token?</summary>
      <div>The contract enforces a minimum of 1 whole token (10&#185;&#8312; smallest units
      &#8212; 1.0 SPLD or 1.0 SPLV) per burn. Smaller burns are refused on-chain before
      anything is destroyed.</div>
    </details>
    <details>
      <summary>Use only your own wallet's quantum address</summary>
      <div>The bridge credits <b>exactly</b> the address the burn event names, and
      cannot credit a made-up address or a quantum contract address (a token, the
      bridge itself) &#8212; a burn to one of those is destroyed and never claimable.
      Paste only the <code>Quantum</code> tab address from your own Splendor Wallet.</div>
    </details>
    <details>
      <summary>Never plain-transfer SPLV to the contract</summary>
      <div><b>Do not</b> send SPLV to the burn contract with an ordinary token
      transfer. Only <code>burnSplv</code> called through this page (or the wallet app)
      emits the <code>Burned</code> event the bridge credits from. A direct transfer
      emits no event, the contract custodies nothing, and those tokens are lost
      exactly as if sent to any wrong address.</div>
    </details>
    <details>
      <summary>Where does my SQ: address come from?</summary>
      <div>From the Splendor Wallet app &#8594; <code>Quantum</code> tab. It is a
      post-quantum account (ML-DSA-65, FIPS 204) with its own independent key &#8212;
      it is <em>not</em> derived from your MetaMask key. Copy it exactly; the bridge
      credits whatever address the burn event names.</div>
    </details>
    <details>
      <summary>How long does it take?</summary>
      <div>Typically 2&#8211;5 minutes: the bridge waits 120 block confirmations on
      Splendor Mainnet (about 2 minutes) before the operator submits the claim on
      the quantum chain. The wait is deliberate &#8212; it protects your credit against
      chain reorganisations.</div>
    </details>
    <details>
      <summary>Burn confirmed but not credited yet?</summary>
      <div>Your burn is never lost: every burn has a permanent on-chain id that
      stays claimable forever. If the bridge is paused or briefly out of pool
      funds, the claim completes once resolved &#8212; keep your burn transaction hash
      and check back here.</div>
    </details>
  </div>
</section>

<footer>
  <span id="links"></span><br>
  Migration is one-way by design. Verify the destination address before confirming &#8212; burned tokens are unrecoverable.
</footer>

<script nonce="${opts.nonce}">
'use strict';
// ── contract constants ──────────────────────────────────────────────────────
// 4-byte selectors + event topic, precomputed keccak-256 and pinned by
// test/selectors.test.ts. Addresses are NEVER hardcoded here — they come from
// /api/info at runtime.
var SEL_BURN_SPLD  = '0xb4b27926'; // burnSpld(bytes32)
var SEL_BURN_SPLV  = '0xe5e2a80a'; // burnSplv(uint256,bytes32)
var SEL_APPROVE    = '0x095ea7b3'; // approve(address,uint256)
var SEL_ALLOWANCE  = '0xdd62ed3e'; // allowance(address,address)
var SEL_BALANCE_OF = '0x70a08231'; // balanceOf(address)
// keccak256("Burned(uint256,address,uint8,uint256,bytes32)")
var BURNED_TOPIC0  = '0x18fb7d6af55f185ad81a39bf0537706c6c62595e260cf5aaeb3f491a4240cd58';

var WEI = 1000000000000000000n;         // 10^18
var GAS_HEADROOM = 5000000000000000n;   // 0.005 SPLD kept back for gas on MAX
var STORE_KEY = 'sq-migration:burn:v1';

var $ = function (id) { return document.getElementById(id); };
var INFO = null, acct = null, chainOk = false, balSpld = null, balSplv = null;
var busy = false, pollTimer = null;

function esc(s) {
  return String(s).replace(/[&<>"']/g, function (c) {
    return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c];
  });
}
function short(a) { return a.length > 14 ? a.slice(0, 8) + '\\u2026' + a.slice(-6) : a; }
function eth() { return window.ethereum || null; }

// ── units ───────────────────────────────────────────────────────────────────
function parseAmount(str) {
  var m = /^([0-9]+)(?:\\.([0-9]{1,18}))?$/.exec(String(str).trim());
  if (!m) return null;
  var frac = (m[2] || '').padEnd(18, '0');
  try { return BigInt(m[1]) * WEI + BigInt(frac); } catch (e) { return null; }
}
function fmtUnits(v, dp) {
  if (v === null || v === undefined) return '\\u2014';
  var neg = v < 0n; if (neg) v = -v;
  var whole = (v / WEI).toString(), frac = (v % WEI).toString().padStart(18, '0');
  frac = frac.slice(0, dp === undefined ? 4 : dp).replace(/0+$/, '');
  return (neg ? '-' : '') + whole + (frac ? '.' + frac : '');
}
function fmtCompact(dec) {
  // dec: smallest-unit decimal string, rendered as a whole-token figure
  if (typeof dec !== 'string' || !/^[0-9]+$/.test(dec)) return '\\u2014';
  var whole = dec.length > 18 ? dec.slice(0, dec.length - 18) : '0';
  var n = whole.length;
  if (n > 9) return whole.slice(0, n - 9) + '.' + whole.slice(n - 9, n - 8) + 'B';
  if (n > 6) return whole.slice(0, n - 6) + '.' + whole.slice(n - 6, n - 5) + 'M';
  if (n > 4) return whole.slice(0, n - 3) + 'K';
  return fmtUnits(BigInt(dec), 2);
}
function pad32(hex) { return hex.replace(/^0x/, '').toLowerCase().padStart(64, '0'); }
function encU256(v) { return v.toString(16).padStart(64, '0'); }

// Liberal SQ: extraction — find the address anywhere in a paste, any case,
// then hold it to the strict canonical form. The trailing (?![0-9a-f]) refuses
// a 65th hex character so a double-paste or trailing garbage is rejected
// rather than silently truncated to a valid-looking WRONG destination.
function extractSq(raw) {
  var m = /sq:([0-9a-f]{64})(?![0-9a-f])/i.exec(String(raw).trim());
  if (!m) return null;
  var norm = 'SQ:' + m[1].toLowerCase();
  return /^SQ:[0-9a-f]{64}$/.test(norm) ? norm : null;
}

// ── boot: /api/info feeds everything ────────────────────────────────────────
async function boot() {
  var res, data = null;
  try {
    res = await fetch('/api/info', { cache: 'no-store' });
    data = await res.json();
  } catch (e) { /* fall through */ }

  var pill = $('pill'), pillT = $('pill-t');
  if (!data || !data.ok) {
    pill.className = 'network-pill down';
    pillT.textContent = data && data.code === 'unconfigured' ? 'Not configured' : 'Bridge unreachable';
    $('cfg-out').hidden = false;
    $('cfg-msg').textContent = data && data.error ? data.error :
      'Could not load bridge configuration. Reload the page to retry.';
    $('connect').disabled = true;
    return;
  }
  INFO = data;
  pillT.textContent = INFO.network.chainName + ' \\u00b7 ' + parseInt(INFO.network.chainIdHex, 16);
  $('m-conf').textContent = String(INFO.confirmations);
  var links = '<a href="' + esc(INFO.network.explorer) + '" target="_blank" rel="noopener">Mainnet explorer</a>' +
    '<span class="sep">/</span>' +
    '<a href="' + esc(INFO.quantum.explorer) + '" target="_blank" rel="noopener">Quantum explorer</a>' +
    '<span class="sep">/</span>' +
    '<span>burn contract ' + esc(short(INFO.burnContract)) + '</span>';
  $('links').innerHTML = links;
  renderStats(INFO.stats);
  refreshStats();
  resume();
}
function renderStats(s) {
  if (!s) return; // dashes stay — the page never breaks on a dark quantum node
  $('m-pool').textContent = fmtCompact(s.poolBalance);
  $('m-spld').textContent = fmtCompact(s.claimedSpld);
  $('m-splv').textContent = fmtCompact(s.claimedSplv);
  if (s.paused) {
    var pill = $('pill');
    pill.className = 'network-pill down';
    $('pill-t').textContent = 'Bridge paused';
    show('err', 'Bridge paused', '<p>Claims are paused by the operator. Burns still work but will only be credited after the bridge resumes. Consider waiting.</p>');
  }
}
function refreshStats() {
  setInterval(async function () {
    try {
      var r = await fetch('/api/info', { cache: 'no-store' });
      var d = await r.json();
      if (d && d.ok) renderStats(d.stats);
    } catch (e) { /* next tick */ }
  }, 30000);
}

// ── wallet ──────────────────────────────────────────────────────────────────
async function connect() {
  if (!eth()) {
    show('err', 'No wallet found', '<p>Install MetaMask (or any injected EVM wallet), then reload this page.</p>');
    return;
  }
  try {
    var accounts = await eth().request({ method: 'eth_requestAccounts' });
    if (!accounts || !accounts[0]) return;
    acct = String(accounts[0]).toLowerCase();
    await ensureChain();
    await onAccount();
  } catch (e) {
    show('err', 'Connection failed', '<p>' + esc(friendly(e)) + '</p>');
  }
}
async function ensureChain() {
  var want = INFO.network.chainIdHex;
  try {
    var current = await eth().request({ method: 'eth_chainId' });
    chainOk = String(current).toLowerCase() === want.toLowerCase();
    if (chainOk) return;
    try {
      await eth().request({ method: 'wallet_switchEthereumChain', params: [{ chainId: want }] });
      chainOk = true;
    } catch (err) {
      if (err && (err.code === 4902 || (err.data && err.data.originalError && err.data.originalError.code === 4902))) {
        await eth().request({
          method: 'wallet_addEthereumChain',
          params: [{
            chainId: want,
            chainName: INFO.network.chainName,
            rpcUrls: [INFO.network.rpcUrl],
            nativeCurrency: { name: 'Splendor', symbol: 'SPLD', decimals: 18 },
            blockExplorerUrls: [INFO.network.explorer]
          }]
        });
        await eth().request({ method: 'wallet_switchEthereumChain', params: [{ chainId: want }] });
        chainOk = true;
      } else { throw err; }
    }
  } catch (e) {
    chainOk = false;
  }
}
async function onAccount() {
  if (!acct) return;
  $('acct-off').hidden = true;
  $('acct-on').hidden = false;
  $('acct-addr').textContent = short(acct);
  $('switch-net').hidden = chainOk;
  if (chainOk) await refreshBalances();
  unlockSteps();
  syncReview();
}
async function refreshBalances() {
  try {
    var results = await Promise.all([
      eth().request({ method: 'eth_getBalance', params: [acct, 'latest'] }),
      eth().request({
        method: 'eth_call',
        params: [{ to: INFO.splvToken, data: SEL_BALANCE_OF + pad32(acct) }, 'latest']
      })
    ]);
    balSpld = BigInt(results[0]);
    balSplv = BigInt(results[1] === '0x' ? '0x0' : results[1]);
    $('bal-spld').textContent = fmtUnits(balSpld) + ' SPLD';
    $('bal-splv').textContent = fmtUnits(balSplv) + ' SPLV';
  } catch (e) {
    $('bal-spld').textContent = $('bal-splv').textContent = 'unavailable';
  }
}
function unlockSteps() {
  var on = !!acct && chainOk;
  ['s2', 's3', 's4'].forEach(function (id) { $(id).classList.toggle('locked', !on); });
}

// ── form state ──────────────────────────────────────────────────────────────
function currentAsset() { return document.querySelector('input[name=asset]:checked').value; }
function currentSq() { return extractSq($('sq').value); }
function currentAmount() { return parseAmount($('amt').value); }

function maxBurnWei() { return INFO && INFO.maxBurn ? BigInt(INFO.maxBurn) : null; }
function validate() {
  var sq = currentSq(), amt = currentAmount(), asset = currentAsset();
  var bal = asset === 'spld' ? balSpld : balSplv;
  var mx = maxBurnWei();
  var ok = !!acct && chainOk && !!INFO && sq !== null && amt !== null &&
    amt >= BigInt(INFO.minBurn) && (mx === null || amt <= mx) &&
    (bal === null || amt <= bal) && $('ack').checked && !busy;
  $('go').disabled = !ok;
  return ok;
}
function syncReview() {
  var sq = currentSq(), amt = currentAmount(), asset = currentAsset().toUpperCase();
  $('rv-act').textContent = amt !== null ? 'Burn ' + fmtUnits(amt) + ' ' + asset : '\\u2014';
  $('rv-from').textContent = acct ? short(acct) : '\\u2014';
  $('rv-to').textContent = sq ? sq : '\\u2014';
  $('ack-addr').textContent = sq ? short(sq) : 'SQ: address';
  validate();
}
$('sq').addEventListener('input', function () {
  var raw = $('sq').value.trim();
  var sq = extractSq(raw);
  var bad = raw.length > 0 && sq === null;
  $('sq').setAttribute('aria-invalid', bad ? 'true' : 'false');
  $('sq-hint').className = bad ? 'hint bad' : (sq ? 'hint good' : 'hint');
  $('sq-hint').textContent = bad
    ? 'Not a valid quantum address \\u2014 SQ: followed by 64 hex characters.'
    : sq ? 'Destination: ' + sq
    : 'Copy it from the Splendor Wallet app \\u2192 Quantum tab. SQ: followed by 64 hex characters.';
  syncReview();
});
$('amt').addEventListener('input', function () {
  var raw = $('amt').value.trim(), amt = parseAmount(raw);
  var asset = currentAsset(), bal = asset === 'spld' ? balSpld : balSplv;
  var min = INFO ? BigInt(INFO.minBurn) : WEI;
  var mx = maxBurnWei();
  var msg = mx !== null
    ? 'Between 1 and ' + fmtUnits(mx) + ' tokens per burn. 18 decimals.'
    : 'Minimum 1 token per burn. 18 decimals.';
  var cls = 'hint';
  if (raw.length > 0 && amt === null) { msg = 'Enter a plain decimal amount, e.g. 12.5'; cls = 'hint bad'; }
  else if (amt !== null && amt < min) { msg = 'Below the minimum \\u2014 burn at least 1 whole token.'; cls = 'hint bad'; }
  else if (amt !== null && mx !== null && amt > mx) { msg = 'Above the ' + fmtUnits(mx) + '-token per-burn limit \\u2014 migrate in several burns.'; cls = 'hint bad'; }
  else if (amt !== null && bal !== null && amt > bal) { msg = 'More than your ' + asset.toUpperCase() + ' balance.'; cls = 'hint bad'; }
  $('amt').setAttribute('aria-invalid', cls.indexOf('bad') >= 0 ? 'true' : 'false');
  $('amt-hint').className = cls;
  $('amt-hint').textContent = msg;
  syncReview();
});
document.querySelectorAll('input[name=asset]').forEach(function (r) {
  r.addEventListener('change', function () {
    $('amt').dispatchEvent(new Event('input'));
  });
});
$('max').addEventListener('click', function () {
  var asset = currentAsset();
  var v = asset === 'spld'
    ? (balSpld !== null ? (balSpld > GAS_HEADROOM ? balSpld - GAS_HEADROOM : 0n) : null)
    : balSplv;
  if (v === null) return;
  // Clamp to the per-burn ceiling: MAX means "the most this one burn may be".
  var mx = maxBurnWei();
  if (mx !== null && v > mx) v = mx;
  $('amt').value = fmtUnits(v, 18);
  $('amt').dispatchEvent(new Event('input'));
});
$('ack').addEventListener('change', validate);
$('connect').addEventListener('click', connect);
$('switch-net').addEventListener('click', async function () {
  await ensureChain();
  await onAccount();
});

if (eth() && eth().on) {
  eth().on('accountsChanged', function (accounts) {
    acct = accounts && accounts[0] ? String(accounts[0]).toLowerCase() : null;
    if (!acct) { $('acct-off').hidden = false; $('acct-on').hidden = true; }
    else onAccount();
    unlockSteps(); syncReview();
  });
  eth().on('chainChanged', async function () {
    if (!acct || !INFO) return;
    await ensureChain();
    await onAccount();
  });
}

// ── the burn itself ─────────────────────────────────────────────────────────
function show(kind, title, html) {
  var out = $('tx-out');
  out.hidden = false;
  out.className = 'out ' + kind;
  out.innerHTML = '<p class="t">' + title + '</p>' + html;
}
function friendly(e) {
  if (e && (e.code === 4001 || e.code === 'ACTION_REJECTED')) return 'Transaction rejected in the wallet.';
  if (e && typeof e.message === 'string') return e.message.slice(0, 200);
  return 'Unexpected wallet error.';
}
async function waitReceipt(hash) {
  for (var i = 0; i < 100; i++) {
    try {
      var r = await eth().request({ method: 'eth_getTransactionReceipt', params: [hash] });
      if (r && r.blockNumber) return r;
    } catch (e) { /* keep polling */ }
    await new Promise(function (res) { setTimeout(res, 3000); });
  }
  return null;
}
async function fetchAllowance() {
  var data = SEL_ALLOWANCE + pad32(acct) + pad32(INFO.burnContract);
  var r = await eth().request({ method: 'eth_call', params: [{ to: INFO.splvToken, data: data }, 'latest'] });
  return BigInt(r === '0x' ? '0x0' : r);
}

$('mig').addEventListener('submit', async function (ev) {
  ev.preventDefault();
  if (!validate()) return;
  var asset = currentAsset(), amt = currentAmount(), sq = currentSq();
  var recipient = pad32('0x' + sq.slice(3)); // the 32 bytes after "SQ:"
  busy = true; validate();
  var go = $('go'), label = go.textContent, hash;

  try {
    if (asset === 'splv') {
      var current = await fetchAllowance();
      if (current < amt) {
        go.textContent = 'Approve SPLV in wallet\\u2026';
        var approveHash = await eth().request({
          method: 'eth_sendTransaction',
          params: [{ from: acct, to: INFO.splvToken, data: SEL_APPROVE + pad32(INFO.burnContract) + encU256(amt) }]
        });
        show('ok', 'Approval sent', '<p>Waiting for the approval to mine\\u2026 do not close this tab yet.</p>');
        go.textContent = 'Waiting for approval\\u2026';
        var rec = await waitReceipt(approveHash);
        if (!rec || rec.status !== '0x1') {
          throw new Error('The approval transaction failed or timed out. Nothing was burned \\u2014 try again.');
        }
      }
      go.textContent = 'Confirm burn in wallet\\u2026';
      hash = await eth().request({
        method: 'eth_sendTransaction',
        params: [{ from: acct, to: INFO.burnContract, data: SEL_BURN_SPLV + encU256(amt) + recipient }]
      });
    } else {
      go.textContent = 'Confirm burn in wallet\\u2026';
      hash = await eth().request({
        method: 'eth_sendTransaction',
        params: [{
          from: acct, to: INFO.burnContract,
          value: '0x' + amt.toString(16),
          data: SEL_BURN_SPLD + recipient
        }]
      });
    }
    startTracking({ tx: String(hash).toLowerCase(), asset: asset, amount: amt.toString(), recipient: sq, t: Date.now() });
    show('ok', 'Burn submitted',
      '<p>Transaction <span style="font-family:var(--mono);color:var(--cyan);overflow-wrap:anywhere">' + esc(hash) + '</span></p>' +
      '<p>Track it in the status section below.</p>');
    $('ack').checked = false;
  } catch (e) {
    show('err', 'Burn not sent', '<p>' + esc(friendly(e)) + '</p>');
  } finally {
    busy = false;
    go.textContent = label;
    validate();
    refreshBalances();
  }
});

// ── status timeline ─────────────────────────────────────────────────────────
function loadBurn() {
  try {
    var raw = localStorage.getItem(STORE_KEY);
    if (!raw) return null;
    var b = JSON.parse(raw);
    return b && /^0x[0-9a-f]{64}$/.test(b.tx) ? b : null;
  } catch (e) { return null; }
}
function saveBurn(b) { try { localStorage.setItem(STORE_KEY, JSON.stringify(b)); } catch (e) {} }
function clearBurn() { try { localStorage.removeItem(STORE_KEY); } catch (e) {} }

function setStep(i, state, detail) {
  var li = $('tl' + i);
  li.className = state;
  if (detail !== undefined) $('tl' + i + '-d').innerHTML = detail;
}
function startTracking(burn) {
  saveBurn(burn);
  $('track-sec').hidden = false;
  var txUrl = INFO ? INFO.network.explorer + '/tx/' + burn.tx : '#';
  setStep(0, 'done', 'Burned ' + esc(fmtUnits(BigInt(burn.amount))) + ' ' + esc(burn.asset.toUpperCase()) +
    ' \\u2014 <a href="' + esc(txUrl) + '" target="_blank" rel="noopener">view on the mainnet explorer</a>');
  setStep(1, 'active'); setStep(2, ''); setStep(3, '');
  if (pollTimer) clearInterval(pollTimer);
  var tick = function () { poll(burn); };
  tick();
  pollTimer = setInterval(tick, 6000);
  $('track-sec').scrollIntoView({ behavior: 'smooth', block: 'start' });
}
async function poll(burn) {
  var data = null;
  try {
    var res = await fetch('/api/status/' + burn.tx, { cache: 'no-store' });
    if (res.status === 429) return; // pacing — next tick will land
    data = await res.json();
  } catch (e) { return; }
  if (!data || !data.ok) return;

  if (!data.found) {
    setStep(1, 'active', 'Waiting for the transaction to appear on-chain\\u2026');
    return;
  }
  if (data.status === 'failed') {
    setStep(1, 'bad', 'The burn transaction REVERTED on-chain. Nothing was burned and nothing will be credited \\u2014 check your balance and try again.');
    return;
  }
  if (data.burns.length === 0) {
    setStep(1, 'bad', 'This transaction emitted no Burned event \\u2014 the bridge cannot credit it. (A plain transfer to the contract is not a burn.)');
    return;
  }

  var conf = Math.min(data.confirmations, data.required);
  var pct = Math.floor((conf / data.required) * 100);
  $('tl1-bar').style.width = pct + '%';
  var b = data.burns[0];
  var claimed = data.claim && data.claim.claimed === true;

  if (data.confirmations < data.required && !claimed) {
    setStep(1, 'active', conf + ' / ' + data.required + ' confirmations on Splendor Mainnet\\u2026');
    setStep(2, ''); setStep(3, '');
    return;
  }
  setStep(1, 'done', data.required + ' / ' + data.required + ' confirmations \\u2014 final for the bridge.');
  if (!claimed) {
    setStep(2, 'active', 'Burn id ' + esc(b.id) + ' is claimable \\u2014 the bridge operator is submitting the credit on the quantum chain\\u2026');
    setStep(3, '');
    return;
  }
  setStep(2, 'done', 'Claim recorded on the quantum chain' +
    (data.claim.height !== undefined ? ' at height ' + esc(String(data.claim.height)) : '') + '.');
  var q = INFO ? INFO.quantum.explorer : '';
  var target = b.recipientSq || burn.recipient;
  setStep(3, 'done', esc(fmtUnits(BigInt(b.amount))) + ' ' + esc(b.asset.toUpperCase()) +
    ' credited to <span style="font-family:var(--mono)">' + esc(short(target)) + '</span> \\u2014 ' +
    '<a href="' + esc(q + '/address/' + target) + '" target="_blank" rel="noopener">view on the quantum explorer</a>');
  $('track-note').textContent = 'Migration complete.';
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
}
$('track-reset').addEventListener('click', function () {
  clearBurn();
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  $('track-sec').hidden = true;
  $('track-note').textContent = 'Status refreshes every 6 seconds. Safe to close this page \\u2014 the burn is on-chain and resumes here on reload.';
  window.scrollTo({ top: 0, behavior: 'smooth' });
});
function resume() {
  var burn = loadBurn();
  if (burn) startTracking(burn);
}

boot();
</script>
</body>
</html>`;
}
