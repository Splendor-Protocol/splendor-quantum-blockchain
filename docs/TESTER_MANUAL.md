# Tester manual: SDK, widgets and dApp Studio

This manual is for people testing the Splendor developer products by hand. It explains what each
product does, how to run it, and, for every feature, the exact steps and the result you should see.
Mark each case **Pass** or **Fail** and note the browser, screen size and network you used.

| Product | What it is | Who uses it |
|---------|------------|-------------|
| `@splendor/sqc-sdk` | The core JavaScript SDK: wallet (post-quantum ML-DSA-65 keys in a WASM signer), transfers, tokens, contracts, bridge | dApp developers, in code |
| `@splendor/connect-react` | React UI kit: Connect button, modals and 12 drop-in widgets (transfer, tokens, NFTs, bridge, …) | dApp developers, in React apps |
| `@splendor/dapp-spec` + `create-splendor-dapp` | A dApp described as JSON, and a CLI that turns a template into a ready-to-run project | Developers starting a new dApp |
| dApp Studio (`apps/playground`) | A web app: learn the SDK, try every widget, and build a dApp visually, then export or share it | Developers and non-developers |
| Studio API (`apps/playground-api`) | Sign-in, saved projects, sharing, admin | Behind the Studio |

Contents:
1. [Set up](#1-set-up)
2. [Words used in this manual](#2-words-used-in-this-manual)
3. [Test cases: wallet connect and sign-in](#3-wallet-connect-and-sign-in)
4. [Test cases: the 12 widgets](#4-the-12-widgets)
5. [Test cases: dApp Studio](#5-dapp-studio)
6. [Test cases: the CLI](#6-the-cli-create-splendor-dapp)
7. [Negative and security cases](#7-negative-and-security-cases)
8. [Devices, browsers and screen sizes](#8-devices-browsers-and-screen-sizes)
9. [Automated tests: what each command covers](#9-automated-tests)
10. [Known limits (not bugs)](#10-known-limits-not-bugs)
11. [Reporting a bug](#11-reporting-a-bug)

---

## 1. Set up

### 1.1 Prerequisites

| Tool | Version | Why |
|------|---------|-----|
| Node.js | **22.20 or newer** (`node -v`) | The SDK and the test tooling require it. On 22.14 the widget tests fail with "Failed to start forks worker". With nvm: `nvm install 22 && nvm use 22`. |
| npm | comes with Node | |
| Go | the version in `go.mod` | Builds the WASM signer once |
| Git, a modern browser | Chrome, Edge, Firefox or Safari | |
| Splendor Quantum Wallet extension | optional | Only for the extension-wallet cases |

### 1.2 Install and build (once)

From the repository root:

```bash
bash sdk/sqc-sdk/wasm/build.sh      # the signer (Windows without bash: cd sdk/sqc-sdk && npm run build:wasm)
npm install
npm run build:packages              # sqc-sdk, connect-react, dapp-spec
```

### 1.3 Run the dApp Studio

```bash
npm run playground                  # http://localhost:5180
npm run playground:api              # second terminal, optional: http://127.0.0.1:8787
```

Without the API the Studio works, but there is no sign-in, saving to an account, sharing or admin.
To test those, before starting the API copy `apps/playground-api/.env.example` to
`apps/playground-api/.env.local` and set:

| Variable | Value for testing |
|----------|-------------------|
| `DATABASE_URL` | `sqlite:./data/playground.db` (default) |
| `PLAYGROUND_ORIGINS` | `http://localhost:5180,http://127.0.0.1:5180` (default) |
| `ADMIN_ADDRESSES` | your test wallet's `SQ:` address, to reach `/admin` |
| `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, `INTEGRATION_TOKEN_KEY` | only for "Push to GitHub" (needs a GitHub OAuth App) |

### 1.4 Run the Feature Lab (widgets and hooks against a live network)

```bash
npm run lab                         # builds, runs unit tests, opens http://localhost:5173
npm run lab:testnet                 # same, forced to testnet
```

The lab shows one card per feature with a checklist and Done / Failed buttons. It defaults to
**testnet**. Do not switch it to mainnet unless you are told to: lab sends are real transactions.
Get test SPLD from the testnet faucet or your team lead before running send cases.

### 1.5 Test wallets

- **Mock (test) wallet**: built into the Studio preview. It signs instantly and sends nothing.
  Its calls appear under **Test wallet activity** below the canvas.
- **Seed wallet**: in the Connect modal choose the native wallet and **Create** a new one. Write the
  seed down for the session; it is never saved and must be typed again after a reload.
- **Extension wallet**: the Splendor Quantum Wallet browser extension, if installed.

Never use a mainnet seed on testnet, devnet or local, or the reverse.

---

## 2. Words used in this manual

| Word | Meaning |
|------|---------|
| SPLD | The chain's native coin |
| `SQ:` address | A Splendor address: `SQ:` followed by 64 hex characters. `0x…` addresses are refused. |
| Widget | A ready-made UI block, e.g. the SPLD transfer form |
| Spec | The JSON description of a dApp built in the Studio |
| Template / kit | A ready-made dApp to start from. A kit has setup questions (your token or contract). |
| Canvas | The page you edit in the Studio |
| Preview app | Full-screen "use it like a visitor" mode in the Studio |
| Live mode | The Studio preview with a real wallet and real transactions |

---

## 3. Wallet connect and sign-in

Run these in the Feature Lab (testnet) unless noted. Each case: **Preconditions → Steps → Expected**.

**C-01 Create a seed wallet**
- Pre: not connected.
- Steps: click **Connect** → choose the native wallet → **Create** → reveal the seed → type back the 4 characters it asks for → confirm.
- Expected: the button becomes an account chip with a balance and a shortened `SQ:` address. The seed is masked again after 60 s if left revealed.
- [ ] Pass  [ ] Fail

**C-02 Import a seed wallet**
- Steps: **Connect** → native wallet → **Import** → paste the seed from C-01 → connect.
- Expected: the same address as C-01.
- [ ] Pass  [ ] Fail

**C-03 Wrong seed**
- Steps: import `abc` or a 63-character hex string.
- Expected: a readable error under the field; the field is cleared; nothing connects.
- [ ] Pass  [ ] Fail

**C-04 Seed is never stored**
- Steps: after C-01, reload the page.
- Expected: you are disconnected (a seed wallet is never restored automatically). An extension wallet, by contrast, reconnects.
- [ ] Pass  [ ] Fail

**C-05 Every seed signature asks first**
- Steps: with a seed wallet, start any send (W-02).
- Expected: an approval dialog shows what will be signed (summary, recipient, amount) with Approve / Reject. Reject → nothing is sent and the widget shows "rejected".
- [ ] Pass  [ ] Fail

**C-06 Account modal and disconnect**
- Steps: click the account chip.
- Expected: a dialog with address, Copy, explorer link (opens in a new tab), balance and **Disconnect**. Disconnect returns to the Connect button.
- [ ] Pass  [ ] Fail

**C-07 Network badge**
- Steps: click the network name next to Connect; use the arrow keys, then Enter.
- Expected: a list of networks; the current one is marked "Current"; Enter switches; Escape closes without switching.
- [ ] Pass  [ ] Fail

**C-08 Wrong-network banner (extension only)**
- Steps: switch the extension to another network than the app.
- Expected: "Your wallet is on …, but this app is on …" with a one-click switch; every widget's controls are disabled until both match.
- [ ] Pass  [ ] Fail

**C-09 Sign-in with the wallet**
- Steps: on the sign-in card, click **Sign in** and approve.
- Expected: success with the signed message; the domain in the message is the page's own origin.
- [ ] Pass  [ ] Fail

**C-10 Keyboard only**
- Steps: using only Tab, Shift+Tab, Enter and Escape, open the Connect dialog, move through it, and close it.
- Expected: focus stays inside the dialog while it is open; Escape closes it; focus returns to the Connect button.
- [ ] Pass  [ ] Fail

---

## 4. The 12 widgets

Each widget can be tried three ways: the Feature Lab (live network), the Studio **Widgets** page
(`/widgets/<id>`, mock wallet, editable props), and inside a Studio dApp. Do the mock version for
every case and the live version where a case says **Live**.

Every widget shares this behavior; check it once per widget:
- Disconnected: a "Connect a wallet" prompt with a Connect button (Validators, Bridge and Activity also show read-only data without a wallet).
- Wrong network: a red message and all controls disabled.
- Long addresses wrap inside the card; the card never overflows the screen at 360px.
- Links to the explorer open in a new tab.

| ID | Widget | Status | Steps | Expected |
|----|--------|--------|-------|----------|
| W-01 | Account card | stable | Connect; view the card; click Copy; click Disconnect | Address, balance, network, explorer link. Copy flips to "Copied". Disconnect works. |
| W-02 | SPLD transfer | stable | Enter an `SQ:` recipient and `0.01` → **Review** → check the fee → **Confirm send** (**Live**: real testnet send) | The review shows From, To, Amount, Network and the real fee. After confirming: "Submitted", then "Confirmed", with the hash, Copy and explorer link. The balance updates. |
| W-03 | Token panel (QRC-20) | stable | Enter a token address you hold → amount → Review → Confirm | Shows the token balance; sends; the result row has the hash. |
| W-04 | Deploy token | stable | Name, symbol, supply → **Review deploy** → **Confirm deploy** | Shows the new token's address with Copy. |
| W-05 | Sign-in button | stable | Click; approve | As C-09. |
| W-06 | NFT panel (QRC-721) | beta | Enter a collection address | A grid of tokens; yours are marked "Yours"; **Send NFT #n** works; the owner sees Mint. |
| W-07 | Bridge status and claims | beta | Enter a bridge address; type a burn id → **Check** | Status (Active / Paused), threshold, limits, totals; the lookup says claimed or not. Without an address: a hint asking for one. |
| W-08 | Validator list | beta | Open | The validator set; an admission check for an address. Works without a wallet. |
| W-09 | Activity feed | beta | Connect; make a transfer; click **Refresh** | Incoming / outgoing entries, pending ones marked; Refresh shows "…" while loading. |
| W-10 | Receive | beta | Connect; optionally enter an amount | Your address, Copy, a QR code (dark on white in every theme, readable by a phone camera), and a payment link. |
| W-11 | Address book | beta | Save a contact with a label; reload; switch network | Contacts are per network and stay after reload; `0x…` addresses are refused. |
| W-12 | Token balances | beta | Add token addresses | SPLD plus each token's balance. |

**Also check the composable parts** on the Widgets page: QR code, Pending transactions (stays after a reload, drops off once confirmed) and Fee row (red "Will revert" when the node predicts a revert).

**Themes** (Widgets page → Theme tab, or the Studio Theme panel): check each widget in Splendor
(dark), Light and a custom accent. Expected: all text is readable; links and Copy in the light theme
are a darker teal than buttons.

**Touch screens** (a phone or a tablet): Copy, Refresh and menu rows are easy to tap (about a
fingertip in size).

---

## 5. dApp Studio

Open `http://localhost:5180`. Clear site data between runs if a case says "fresh".

### 5.1 Home, Learn and the widget gallery

**S-01 Home**: Expected: product sections, ready-made kits, templates, FAQ, and your workspaces. No sideways scrolling at 360px. [ ] Pass [ ] Fail

**S-02 Learn**: open **Learn** → a guide → a recipe → **Run in sandbox** (mock) and, if connected, live. Expected: code examples scroll inside their own box and can be scrolled with the keyboard (Tab to the box, then arrow keys); on phones a topic picker replaces the sidebar. [ ] Pass [ ] Fail

**S-03 Gallery search**: **Widgets** → search "transfer". Expected: only matching widgets; nonsense text shows an empty state. [ ] Pass [ ] Fail

**S-04 Widget page**: open a widget → change props (Settings), theme (Theme), copy the code (Code) → **Add to my dApp**. Expected: the preview updates live; the Code tab's snippet matches the props; Add opens the builder with the widget on a blank page. [ ] Pass [ ] Fail

### 5.2 Building a dApp

**S-10 Start from a template (fresh)**: **Build** → pick **Wallet dashboard**. Expected: the canvas shows Hero, Account, Transfer and Pending transactions; nothing is flagged under "Finish setup". [ ] Pass [ ] Fail

**S-11 Kit setup wizard**: **Build** → **Token project** (ready-made app) → enter your token address (or keep the demo) → create. Expected: two pages (Home, Dashboard); the token appears on both. [ ] Pass [ ] Fail

**S-12 Add, move, remove, undo**:
- Steps: **Components** panel → click a piece (or drag it onto the page) → in **Layers**, select it → press Delete → click **Undo**.
- Expected: the piece appears where expected, is removed, then comes back. Keyboard in Layers: arrow keys move, Alt+Arrow reorders, Ctrl+D duplicates, Delete removes, Left/Right fold a section, F2 renames it.
- [ ] Pass  [ ] Fail

**S-13 Edit props (Inspector)**: select a block → change text, toggles, style. Expected: the canvas updates immediately; invalid input (a `0x` address, a negative amount) is flagged in the field. [ ] Pass [ ] Fail

**S-14 Pages**: **Pages** panel → **+ Add page** → title "About" → **Add blank**; duplicate a page; set it as home. Expected: page chips update; exactly one home page. [ ] Pass [ ] Fail

**S-15 Theme**: **Theme** panel → pick presets, change accent, switch Light / Dark. Expected: canvas restyles; text stays readable. [ ] Pass [ ] Fail

**S-16 Contracts**: **Contracts** panel → add a contract by name, address and ABI (`get() read`, `set(uint256 x) write`) → drop a function onto the page. Expected: a read button and a write form; in the preview, Read shows a result and the write goes to Test wallet activity. [ ] Pass [ ] Fail

**S-17 Flows**: **Flows** panel. Expected: a map of links between pages; clicking a link selects the button that holds it. [ ] Pass [ ] Fail

**S-18 Device sizes**: switch Desktop / Tablet / Mobile above the canvas. Expected: the page reflows; on a laptop Desktop is zoomed to fit (a % badge). [ ] Pass [ ] Fail

**S-19 Test wallet states**: **Test wallet state** → **No wallet**. Expected: widgets show their connect prompts; back to **Connected** restores them. [ ] Pass [ ] Fail

**S-20 Preview app**: **Preview app** → use the dApp like a visitor (send a transfer) → open the calls list → **Back to editor**. Expected: full-screen dApp; the transfer appears in the calls list; Escape also closes. On phones and tablets this is the way to use widgets (tapping the canvas edits it). [ ] Pass [ ] Fail

**S-21 Live mode (careful)**: switch the preview to **Real wallet** on testnet; send 0.01 SPLD. Then pick mainnet. Expected: testnet sends a real transaction; mainnet asks for an explicit confirmation first ("Live mode on mainnet signs real transactions…"). [ ] Pass [ ] Fail

**S-22 Narrow studio (tablet / phone)**: at 768px and 390px. Expected: panels open as drawers (dialogs) from the icon rail; **Edit selection** opens the Inspector; Escape closes a drawer; nothing scrolls sideways. [ ] Pass [ ] Fail

### 5.3 Drafts, saving, versions and trash

**S-30 Draft survives a reload**: edit the dApp name → reload. Expected: the edit is still there. [ ] Pass [ ] Fail

**S-31 Template start keeps your draft**: with an unsaved edit, open another template. Expected: a notice offers **Restore**; Restore brings your draft back. [ ] Pass [ ] Fail

**S-32 Browser workspaces (no account)**: Home → **New workspace** → name → build a project in it → back on Home rename and duplicate the project. Expected: the project card updates; "this browser" label (these live only in this browser). [ ] Pass [ ] Fail

**S-33 Save to account (needs API)**: sign in (connect wallet, then Sign in) → **Project** tab → **Save to my account**. Expected: "Saved to your account."; the URL gets `?project=…`; the chip shows "Synced". [ ] Pass [ ] Fail

**S-34 Conflict**: open the same saved project in two browsers; edit and save in both. Expected: the second save reports a conflict and offers to open the newer copy or keep yours. [ ] Pass [ ] Fail

**S-35 Version history**: **Project** → **Version history** → **Restore version 1**. Expected: the older version loads and becomes the newest version. [ ] Pass [ ] Fail

**S-36 Trash**: delete a project from Home → open Trash → **Restore**. Expected: it is back; deleting forever removes it. [ ] Pass [ ] Fail

### 5.4 Share, export and deploy

**S-40 Share and remix (needs API)**: **Create share link** → open it in a private window. Expected: a read-only page with a caution to check every address; **Remix** opens a copy in the builder. Stopping sharing makes the link fail. [ ] Pass [ ] Fail

**S-41 Export blocked until setup is complete**: start **Pay / tip jar** without a recipient → **Export**. Expected: export is disabled with a "Finish setup" pointer; filling the recipient enables it. [ ] Pass [ ] Fail

**S-42 Export a zip (Vite and Next.js)**: **Export** → **Vite + React** → download the zip; repeat for Next.js. Unzip, then `npm install && npm run dev`. Expected: the project runs and looks like the preview; its README explains networks, the signer pin and deployment. [ ] Pass [ ] Fail

**S-43 StackBlitz**: **Export** → open in StackBlitz. Expected: the project boots in the browser. [ ] Pass [ ] Fail

**S-44 Push to GitHub (needs API + OAuth app)**: **Export** → connect GitHub → repository name → push. Expected: a link to the new repository and "Deploy on Vercel / Netlify" buttons. [ ] Pass [ ] Fail

### 5.5 Admin (needs API, admin wallet)

**S-50 Non-admin refused**: sign in with a non-admin wallet → `/admin`. Expected: "This wallet is not an admin." [ ] Pass [ ] Fail

**S-51 Catalog flags**: `/admin/catalog` → untick **Show SPLD transfer** → open the builder. Expected: SPLD transfer is not in Components; re-tick restores it; `/admin/audit` lists both changes. [ ] Pass [ ] Fail

**S-52 Templates and recipes**: `/admin/templates` → untick **Show** for "Pay / tip jar". Expected: it disappears from the Build template picker; re-tick restores it. [ ] Pass [ ] Fail

**S-53 Releases**: `/admin/releases` → dry run. Expected: "Dry run started…" (needs `GITHUB_TOKEN`; without it the page is read-only). [ ] Pass [ ] Fail

---

## 6. The CLI (`create-splendor-dapp`)

From an empty folder (inside the repo, use `node <repo>/packages/create-splendor-dapp/bin/index.mjs` instead of `npm create`):

| ID | Command | Expected |
|----|---------|----------|
| L-01 | `npm create splendor-dapp@latest` | Asks for name, template, framework, network; creates the folder; prints `cd …`, `npm install`, `npm run dev`. |
| L-02 | `… my-app -- --yes` | Creates a Vite **Wallet dashboard** on testnet with no questions. |
| L-03 | `… kit -- --template token-project --framework next --network devnet` | A Next.js project; `app/providers.tsx` contains `"devnet"`. |
| L-04 | `… tips -- --template tip-jar --set tips.recipient=SQ:<64 hex>` | Created; the recipient is in `src/pages/HomePage.tsx`. |
| L-05 | `… tips -- --template tip-jar` | Exit code 1: "The template needs more input … --set tips.recipient=…". |
| L-06 | `… "bad name!" -- --yes` / `--template nope` / `--bogus` | Exit code 1 with a readable message each time. |
| L-07 | `… old -- --template landing --yes` | Works: the retired id now creates the Token project. |
| L-08 | In a created project: `npm install && npm run build` | Builds for production. |

---

## 7. Negative and security cases

| ID | Case | Steps | Expected |
|----|------|-------|----------|
| N-01 | EVM address | Transfer to `0x1234…` | Inline error; Review stays disabled. |
| N-02 | Bad `SQ:` address | `SQ:abc`, or 63 hex characters | Inline error. |
| N-03 | Send to yourself | Your own address | Refused. |
| N-04 | Amount above balance | More than the balance, or balance minus fee + 1 | Error; Confirm disabled ("cannot cover amount plus fee"). |
| N-05 | Zero / negative / too many decimals | `0`, `-1`, `0.0000000000000000001` | Refused. |
| N-06 | Predicted revert | A contract call the node says will fail | Fee row shows "Will revert"; Confirm disabled. |
| N-07 | Wallet rejects | Reject in the approval dialog / extension | "Rejected" message; nothing sent; no toast says success. |
| N-08 | Node error text | Make a send fail (e.g. disconnect the network mid-send) | A safe, plain message; never a raw stack or node dump. |
| N-09 | Wrong network | C-08 | Controls disabled until fixed. |
| N-10 | Mainnet live mode | S-21 | Explicit confirmation required. |
| N-11 | API offline | Stop the API; reload; open the Project tab | "The playground API is not reachable, so sign-in and cloud saves are off. Projects are still saved in this browser."; local editing keeps working; nothing is lost. |
| N-12 | Conflicting saves | S-34 | No silent overwrite. |
| N-13 | Malicious spec import | **Project** → **Spec JSON** → paste a spec whose text props contain `<script>alert(1)</script>` and a link `javascript:alert(1)` → import | The text shows literally; the bad link is refused by validation; no alert ever runs (also check the export). |
| N-14 | Unsafe explorer / link | A widget link prop set to `javascript:…` | No link is rendered. |
| N-15 | Seed hygiene | After C-01 open DevTools → Application → Local Storage | No seed or private key anywhere; only connector choice, network, contacts, recent recipients and pending tx hashes. |
| N-16 | Shared link tampering | Edit the share URL id | "Not found" page; no error dump. |
| N-17 | Admin API without admin | As a non-admin, call `PUT /api/admin/catalog/transfer` from DevTools | 403. |

---

## 8. Devices, browsers and screen sizes

Run every Studio case at least at **1440** (desktop) and **390** (phone), and the Studio cases
S-10 to S-22 also at **768** (tablet). Run the widget cases in the themes listed in section 4.

| Width × height | Typical device | What to check |
|----------------|----------------|---------------|
| 360 × 740 | Small Android | Nothing cut off or scrolling sideways; header menu works |
| 390 × 844 | iPhone 13–15 | Studio drawers, Preview app, widget forms, QR code |
| 768 × 1024 | iPad portrait | Narrow studio (drawers), gallery grid |
| 1024 × 768 | iPad landscape / small laptop | Studio still narrow (drawers below 1100px) |
| 1440 × 900 | Laptop / desktop | Full studio: rail, sidebar, canvas, inspector |

| Browser | Desktop | Mobile |
|---------|---------|--------|
| Chrome / Edge (latest) | required | Android Chrome required |
| Firefox (latest) | required | optional |
| Safari (latest) | required on macOS | iOS Safari required |

Also check once: browser zoom at 200% (no overlapping text), and the OS "reduce motion" setting
(no animations in the Connect dialog).

---

## 9. Automated tests

All commands run from the repository root with Node 22.20+. Times are from a Windows laptop.

| Command | Covers | Time |
|---------|--------|------|
| `npm run build:packages` | Builds sqc-sdk, connect-react, dapp-spec | ~30 s |
| `npm run test:packages` | Unit tests: sqc-sdk (459), connect-react (594), dapp-spec (255), CLI (14) — security, wallet, tokens, bridge, widgets, hooks, themes, contrast, links, codegen | ~30 s |
| `npm run test:prod` | Production-scenario tiers (L1–L4) for the SDK, widgets, codegen and Studio templates | ~1 min |
| `npm run test:playground` | Studio (404) and API (83) unit tests: builder state, drafts, preview isolation, auth, CSRF, projects, revisions, trash, sharing, admin | ~1 min |
| `npm run test:e2e` | Playwright on the production build under the real CSP (158 tests): every template, saving, sharing, admin, export, GitHub push (fake), security headers; layout, user journeys and an accessibility scan (WCAG 2.1 AA) on desktop, 390px phone and 768px tablet | ~4–5 min |
| `npm run test:codegen` | Generates every template for Vite and Next.js, installs from packed tarballs, runs each production build (16 builds) | ~12–25 min |
| `npm run test:webcontainer` | Static checks of the StackBlitz export | ~10 s |
| `npm run lab:check` | Builds, unit-tests and typechecks the Feature Lab | ~1 min |
| `npm audit` | Known vulnerabilities in all dependencies (currently 0) | ~20 s |
| `npm run release:check` | The full pre-publish gate | ~5 min |

First time only for e2e: `cd apps/playground && npx playwright install chromium`.

---

## 10. Known limits (not bugs)

- **Hosting** inside the Studio is off (`HOSTING_ENABLED`); users deploy exports to their own host.
- **Shared variables / form wiring between blocks** is planned (Studio roadmap, Phase 2).
- **DEX / Exchange** are not available: the node does not route them yet.
- **WalletConnect, MetaMask and other EVM wallets** are not supported, by design (post-quantum keys only).
- **Beta widgets** (NFT panel, Bridge, Validators, Activity, Receive, Address book, Token balances) may change props in a minor release.
- A **seed wallet** is never remembered: re-enter it after each reload.
- The Studio **API is single-instance**; rate limits are per process.
- The widget preview on `/widgets/<id>` keeps a fixed height so review and result steps fit; short widgets leave empty space below.

---

## 11. Reporting a bug

Include: case ID, what you did, what you expected, what happened, browser and version, screen
size, network, wallet type (mock / seed / extension), a screenshot, and for sends the transaction
hash. Never paste a seed phrase or private key into a bug report. Report security issues privately
as described in [SECURITY.md](../SECURITY.md).
