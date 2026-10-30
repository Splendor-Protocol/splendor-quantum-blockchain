import { TOPIC_TRANSFER, TOPIC_TRANSFER_SINGLE, type NetworkId } from '@splendor/connect-react';
import { MOCK_ADDRESS, createMockConnectController, mockWallet, type MockConnectController } from '@splendor/connect-react/testing';
import { DEMO_CONTRACT_ADDRESS, DEMO_TOKEN_ADDRESS } from '@splendor/dapp-spec';
import { fromUnits, functionSelectorHex, qrc1155Address, qrc20Address, qrc721Address, toUnits } from '@splendor/sqc-sdk';

export type MockWalletState = 'connected' | 'disconnected';

/** Fee charged per send when the dApp passes none (matches the mock estimate). */
const DEFAULT_FEE = 21_000_000_000_000n;
/** How long a send stays in the pending list before its receipt reports success. */
export const MOCK_CONFIRM_MS = 3000;
/** An address with no history: tips sent here are visibly "someone else's". */
export const DEMO_RECIPIENT = 'SQ:' + '5e'.repeat(32);

type Token = { name: string; symbol: string; decimals: number; owner: string; total: bigint; balances: Map<string, bigint> };
type Collection = { name: string; owner: string; uriPrefix: string; balances: Map<string, bigint> };
type Nft = { name: string; symbol: string; owner: string; owners: Map<string, string> };
type Bridge = { paused: boolean; threshold: number; claimed: Map<number, number>; pool: bigint; claimedSpld: bigint; claimedSplv: bigint };
type Log = { address: string; topics: string[]; data: string; block_height: number; tx_hash: string; log_index: number };

/** Demo validator set the validator widgets list in previews. */
export const DEMO_VALIDATORS = ['SQ:' + 'a1'.repeat(32), 'SQ:' + 'b2'.repeat(32), 'SQ:' + 'c3'.repeat(32)];
const ZERO = 'SQ:' + '00'.repeat(32);
const BOND = 2500n * 10n ** 18n;

const big = (v: unknown): bigint => {
  try {
    return BigInt(String(v ?? '0'));
  } catch {
    return 0n;
  }
};
const word = (n: bigint) => '0x' + n.toString(16).padStart(64, '0');
/** Read selectors the mock answers for token addresses on the contract-call path (ContractRead, LiveStat). */
const TOKEN_SELECTORS = {
  totalSupply: functionSelectorHex('totalSupply()'),
  decimals: functionSelectorHex('decimals()'),
  balanceOf: functionSelectorHex('balanceOf(address)'),
};
const lower = (a: unknown) => (typeof a === 'string' && /^SQ:/i.test(a) ? `SQ:${a.slice(3).toLowerCase()}` : String(a ?? ''));

/**
 * In-memory chain for previews: no WASM, no RPC, every signature succeeds instantly. Balances,
 * deployed tokens and collections change as the dApp sends, and each send waits in the pending
 * list for MOCK_CONFIRM_MS before it "confirms", so every widget has something real to show.
 */
export function createPlaygroundMock(opts: { network: NetworkId; state: MockWalletState; balance?: string }): MockConnectController {
  const me = lower(MOCK_ADDRESS);
  let spld = BigInt(fromUnits(opts.balance ?? '1250.5', 18));
  const tokens = new Map<string, Token>();
  const collections = new Map<string, Collection>();
  const nfts = new Map<string, Nft>();
  const contracts = new Map<string, bigint>();
  const bridges = new Map<string, Bridge>();
  const confirmAt = new Map<string, number>();
  const logs: Log[] = [];
  let seq = 0;
  // Chain head: starts with some history and moves one block per send.
  let height = 1200;
  let currentTx = '';

  const topic = (addr: string) => lower(addr).slice(3);
  const emit = (address: string, topics: string[], data: bigint[]) => {
    logs.push({
      address: lower(address),
      topics,
      data: '0x' + data.map((d) => d.toString(16).padStart(64, '0')).join(''),
      block_height: height,
      tx_hash: currentTx || `${height.toString(16)}`.padStart(64, '0'),
      log_index: logs.filter((l) => l.block_height === height).length,
    });
  };
  const transferLog = (token: string, from: string, to: string, amount: bigint) =>
    emit(token, [TOPIC_TRANSFER, topic(from), topic(to)], [amount]);
  const matches = (filter: { address?: string; from_block?: number; to_block?: number; topics?: string[] }, l: Log) =>
    (!filter.address || lower(filter.address) === l.address) &&
    (filter.from_block == null || l.block_height >= filter.from_block) &&
    (filter.to_block == null || l.block_height <= filter.to_block) &&
    (filter.topics ?? []).every((t, i) => !t || l.topics[i] === t.replace(/^0x/i, '').toLowerCase());

  // The demo assets the templates and the Content pickers point at (DEMO_TOKEN_ADDRESS / DEMO_CONTRACT_ADDRESS).
  tokens.set(lower(DEMO_TOKEN_ADDRESS), {
    name: 'Demo Token',
    symbol: 'DEMO',
    decimals: 18,
    owner: me,
    total: 1_000_000n * 10n ** 18n,
    balances: new Map([[me, 25_000n * 10n ** 18n]]),
  });
  contracts.set(lower(DEMO_CONTRACT_ADDRESS), 42n);
  // A little history so the activity feed has something to show before the first send.
  for (const [h, from, to, amount] of [
    [1100, ZERO, me, 25_000n],
    [1150, DEMO_RECIPIENT, me, 500n],
    [1180, me, DEMO_RECIPIENT, 500n],
  ] as const) {
    height = h;
    currentTx = (h * 7919).toString(16).padStart(64, 'a');
    transferLog(DEMO_TOKEN_ADDRESS, from, to, amount * 10n ** 18n);
  }
  height = 1200;
  currentTx = '';

  // Addresses the dApp did not deploy here (pasted into a block) behave like a demo asset you hold.
  const token = (addr: string): Token => {
    let t = tokens.get(addr);
    if (!t) {
      const unit = 10n ** 18n;
      t = { name: 'Demo Token', symbol: 'DEMO', decimals: 18, owner: me, total: 1_000_000n * unit, balances: new Map([[me, 1000n * unit]]) };
      tokens.set(addr, t);
    }
    return t;
  };
  const collection = (addr: string): Collection => {
    let c = collections.get(addr);
    if (!c) {
      c = { name: 'Demo Collection', owner: me, uriPrefix: 'ipfs://demo/', balances: new Map([[`${me}|1`, 3n]]) };
      collections.set(addr, c);
    }
    return c;
  };
  // A pasted QRC-721 collection: you own #1-#3, someone else #4-#6, #7 and up are unminted.
  const nft = (addr: string): Nft => {
    let n = nfts.get(addr);
    if (!n) {
      const owners = new Map<string, string>();
      for (let i = 1; i <= 6; i++) owners.set(String(i), i <= 3 ? me : lower(DEMO_RECIPIENT));
      n = { name: 'Demo NFTs', symbol: 'DNFT', owner: me, owners };
      nfts.set(addr, n);
    }
    return n;
  };
  const bridge = (addr: string): Bridge => {
    let b = bridges.get(addr);
    if (!b) {
      b = { paused: false, threshold: 2, claimed: new Map([[1, 1050]]), pool: 50_000n * 10n ** 18n, claimedSpld: 120n * 10n ** 18n, claimedSplv: 0n };
      bridges.set(addr, b);
    }
    return b;
  };
  const move = (balances: Map<string, bigint>, key: (owner: string) => string, from: string, to: string, amount: bigint) => {
    const have = balances.get(key(from)) ?? 0n;
    if (amount > have) throw new Error('Insufficient balance');
    balances.set(key(from), have - amount);
    balances.set(key(to), (balances.get(key(to)) ?? 0n) + amount);
  };

  function applyModuleCall(module: string, action: string, p: Record<string, unknown>, nonce: number) {
    if (module === 'qrc20') {
      if (action === 'deploy') {
        const total = big(p.total_supply);
        tokens.set(lower(qrc20Address(MOCK_ADDRESS, nonce)), {
          name: String(p.name),
          symbol: String(p.symbol),
          decimals: Number(p.decimals ?? 18),
          owner: me,
          total,
          balances: new Map([[me, total]]),
        });
        return;
      }
      const t = token(lower(p.token));
      if (action === 'transfer') {
        move(t.balances, (o) => o, me, lower(p.to), big(p.amount));
        transferLog(String(p.token), me, String(p.to), big(p.amount));
      } else if (action === 'mint') {
        t.total += big(p.amount);
        t.balances.set(lower(p.to), (t.balances.get(lower(p.to)) ?? 0n) + big(p.amount));
        transferLog(String(p.token), ZERO, String(p.to), big(p.amount));
      } else if (action === 'burn') {
        move(t.balances, (o) => o, me, 'burned', big(p.amount));
        t.total -= big(p.amount);
        transferLog(String(p.token), me, ZERO, big(p.amount));
      }
      return;
    }
    if (module === 'qrc1155') {
      if (action === 'deploy') {
        collections.set(lower(qrc1155Address(MOCK_ADDRESS, nonce)), {
          name: String(p.name),
          owner: me,
          uriPrefix: String(p.uri_prefix ?? ''),
          balances: new Map(),
        });
        return;
      }
      const c = collection(lower(p.collection));
      const key = (id: string) => (o: string) => `${o}|${id}`;
      const single = (from: string) =>
        emit(String(p.collection), [TOPIC_TRANSFER_SINGLE, topic(me), topic(from), topic(String(p.to))], [big(p.id), big(p.amount)]);
      if (action === 'mint') {
        const k = `${lower(p.to)}|${String(p.id)}`;
        c.balances.set(k, (c.balances.get(k) ?? 0n) + big(p.amount));
        single(ZERO);
      } else if (action === 'transfer') {
        move(c.balances, key(String(p.id)), me, lower(p.to), big(p.amount));
        single(me);
      }
      return;
    }
    if (module === 'bridge' && action === 'claim') {
      const b = bridge(lower(p.bridge));
      const id = Number(p.id);
      if (b.paused) throw new Error('bridge is paused');
      if (b.claimed.has(id)) throw new Error(`burn #${id} is already claimed`);
      if (!Array.isArray(p.attestations) || p.attestations.length < b.threshold) {
        throw new Error(`need ${b.threshold} attestations`);
      }
      const amount = big(p.amount);
      b.claimed.set(id, height);
      if (p.asset === 'splv') b.claimedSplv += amount;
      else {
        if (amount > b.pool) throw new Error('bridge pool is too small');
        b.pool -= amount;
        b.claimedSpld += amount;
        if (lower(p.recipient) === me) spld += amount;
      }
      return;
    }
    if (module === 'qrc721') {
      if (action === 'deploy') {
        nfts.set(lower(qrc721Address(MOCK_ADDRESS, nonce)), { name: String(p.name), symbol: String(p.symbol), owner: me, owners: new Map() });
        return;
      }
      const n = nft(lower(p.collection));
      const id = String(p.id);
      if (action === 'mint') {
        if (n.owners.has(id)) throw new Error(`Token #${id} is already minted`);
        n.owners.set(id, lower(p.to));
      } else if (action === 'transfer') {
        if (n.owners.get(id) !== me) throw new Error(`You do not own token #${id}`);
        n.owners.set(id, lower(p.to));
      } else if (action === 'burn') {
        if (n.owners.get(id) !== me) throw new Error(`You do not own token #${id}`);
        n.owners.delete(id);
      }
    }
  }

  function answerRead(module: string, action: string, p: Record<string, unknown>): unknown {
    if (module === 'qrc20') {
      const t = token(lower(p.token));
      if (action === 'token_info') {
        return { name: t.name, symbol: t.symbol, decimals: t.decimals, total_supply: String(t.total), owner: t.owner, exists: true };
      }
      if (action === 'balance_of') return { balance: String(t.balances.get(lower(p.owner)) ?? 0n) };
      if (action === 'total_supply') return { total_supply: String(t.total) };
      if (action === 'owner') return { owner: t.owner };
      if (action === 'paused') return { paused: false };
    }
    if (module === 'qrc1155') {
      const c = collection(lower(p.collection));
      const supply = [...c.balances.values()].reduce((a, b) => a + b, 0n);
      if (action === 'token_info') return { name: c.name, symbol: c.name.slice(0, 8).toUpperCase(), owner: c.owner, total_supply: String(supply), uri_prefix: c.uriPrefix };
      if (action === 'balance_of') return { balance: String(c.balances.get(`${lower(p.owner)}|${String(p.id)}`) ?? 0n) };
      if (action === 'total_supply') {
        const id = String(p.id);
        return { total_supply: String([...c.balances].filter(([k]) => k.endsWith(`|${id}`)).reduce((a, [, v]) => a + v, 0n)) };
      }
    }
    if (module === 'qrc721') {
      const n = nft(lower(p.collection));
      if (action === 'token_info') return { name: n.name, symbol: n.symbol, owner: n.owner, total_supply: String(n.owners.size) };
      if (action === 'balance_of') return String([...n.owners.values()].filter((o) => o === lower(p.owner)).length);
      if (action === 'owner_of') {
        const o = n.owners.get(String(p.id));
        if (!o) throw new Error(`Token #${String(p.id)} does not exist`);
        return o;
      }
    }
    if (module === 'bridge') {
      const b = bridge(lower(p.bridge));
      if (action === 'bridge_info') {
        return {
          address: lower(p.bridge),
          owner: me,
          operators: [me],
          paused: b.paused,
          threshold: b.threshold,
          evm_chain_id: 1,
          evm_contract: '0x' + '42'.repeat(20),
          splv_token: 'SQ:' + '5f'.repeat(32),
          max_claim_amount: String(1000n * 10n ** 18n),
          claimed_total_spld: String(b.claimedSpld),
          claimed_total_splv: String(b.claimedSplv),
          pool_balance: String(b.pool),
        };
      }
      if (action === 'claim_status') {
        const id = Number(p.id);
        const at = b.claimed.get(id);
        return { module: 'bridge', action: 'claim_status', bridge: lower(p.bridge), id, claimed: at != null, ...(at != null ? { height: at } : {}) };
      }
    }
    if (module === 'validators') {
      // Same shape as the node: big.Int bond as a JSON number, status 1 = active.
      const record = (address: string, i: number) => ({ address, public_key: '', bond: Number(BOND), status: 1, joined_height: 10 + i * 40 });
      if (action === 'list') return DEMO_VALIDATORS.map(record);
      if (action === 'count') return { count: DEMO_VALIDATORS.length };
      if (action === 'bond') return { bond: String(BOND), spld: '2500' };
      if (action === 'approved') return { approved: DEMO_VALIDATORS.includes(lower(p.address)) || lower(p.address) === me };
      if (action === 'validator') {
        const i = DEMO_VALIDATORS.indexOf(lower(p.address));
        return i >= 0 ? record(DEMO_VALIDATORS[i]!, i) : null;
      }
    }
    return {};
  }

  const session = Date.now().toString(16);
  const wallet = mockWallet({
    // Unique across mock re-creations too: the pending list outlives a network or wallet toggle.
    hash: () => `${session}${(++seq).toString(16).padStart(6, '0')}`.padStart(64, 'c'),
    confirm: false,
    receipt: (hash) => {
      const at = confirmAt.get(hash.toLowerCase().replace(/^0x/, ''));
      return at == null || Date.now() >= at ? { status: 'success' } : {};
    },
    balanceSPLD: () => toUnits(spld, 18),
    balanceRaw: () => String(spld),
    estimate: { fee: String(DEFAULT_FEE), fee_is_floor: false, gas: 21000 },
    height: () => height,
    logs: (filter) => logs.filter((l) => matches(filter, l)),
    moduleCall: ({ module, action, params }) => answerRead(module, action, params ?? {}),
    call: (req) => {
      const to = lower(req.to);
      const t = tokens.get(to);
      if (t) {
        // Token addresses answer the standard reads, so a contract-call block on a token works too.
        const data = String(req.data ?? '').replace(/^0x/i, '');
        const sel = data.slice(0, 8);
        if (sel === TOKEN_SELECTORS.totalSupply) return { return_data: word(t.total) };
        if (sel === TOKEN_SELECTORS.decimals) return { return_data: word(BigInt(t.decimals)) };
        if (sel === TOKEN_SELECTORS.balanceOf) return { return_data: word(t.balances.get(lower('SQ:' + data.slice(8, 72))) ?? 0n) };
      }
      return { return_data: word(contracts.get(to) ?? 0n) };
    },
    // Throwing here fails the send, like a revert, before any fee is charged.
    onSend: (call, res) => {
      const o = (call.args[0] ?? {}) as Record<string, unknown>;
      const fee = o.fee ? big(o.fee) : DEFAULT_FEE;
      height += 1;
      currentTx = res.hash;
      if (call.method === 'payX402') {
        // x402 settles a native SPLD payment; the mock charges it like a transfer.
        const amount = big(o.amount);
        if (lower(o.to) !== me) {
          if (amount + fee > spld) throw new Error('Insufficient SPLD balance');
          spld -= amount;
        }
      } else if (call.method === 'sendTransfer') {
        const amount = o.amountSPLD != null ? BigInt(fromUnits(String(o.amountSPLD), 18)) : big(o.amount);
        const spent = (lower(o.to) === me ? 0n : amount) + fee;
        if (spent > spld) throw new Error('Insufficient SPLD balance');
        spld -= spent - fee;
      } else if (call.method === 'sendModuleCall') {
        applyModuleCall(String(o.module), String(o.action), (o.params ?? {}) as Record<string, unknown>, res.signedTx.nonce);
      } else if (call.method === 'callContract') {
        const sig = String(o.function ?? '');
        const addr = lower(o.contract);
        const args = (o.args ?? []) as unknown[];
        if (/^set\w*\(uint256\)$/.test(sig)) contracts.set(addr, big(args[0]));
        else if (/^increment\(\)$/.test(sig)) contracts.set(addr, (contracts.get(addr) ?? 0n) + 1n);
      }
      spld -= fee;
      confirmAt.set(res.hash, Date.now() + MOCK_CONFIRM_MS);
    },
  });
  return createMockConnectController({ status: opts.state, network: opts.network, balance: () => toUnits(spld, 18), wallet });
}

export { MOCK_ADDRESS };
