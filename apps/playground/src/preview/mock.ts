import type { NetworkId } from '@splendor/connect-react';
import { MOCK_ADDRESS, createMockConnectController, mockWallet, type MockConnectController } from '@splendor/connect-react/testing';
import { fromUnits, qrc1155Address, qrc20Address, qrc721Address, toUnits } from '@splendor/sqc-sdk';

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

const big = (v: unknown): bigint => {
  try {
    return BigInt(String(v ?? '0'));
  } catch {
    return 0n;
  }
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
  const confirmAt = new Map<string, number>();
  let seq = 0;

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
  const nft = (addr: string): Nft => {
    let n = nfts.get(addr);
    if (!n) {
      n = { name: 'Demo NFTs', symbol: 'DNFT', owner: me, owners: new Map([['1', me]]) };
      nfts.set(addr, n);
    }
    return n;
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
      if (action === 'transfer') move(t.balances, (o) => o, me, lower(p.to), big(p.amount));
      else if (action === 'mint') {
        t.total += big(p.amount);
        t.balances.set(lower(p.to), (t.balances.get(lower(p.to)) ?? 0n) + big(p.amount));
      } else if (action === 'burn') {
        move(t.balances, (o) => o, me, 'burned', big(p.amount));
        t.total -= big(p.amount);
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
      if (action === 'mint') {
        const k = `${lower(p.to)}|${String(p.id)}`;
        c.balances.set(k, (c.balances.get(k) ?? 0n) + big(p.amount));
      } else if (action === 'transfer') move(c.balances, key(String(p.id)), me, lower(p.to), big(p.amount));
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
      } else if (action === 'transfer') n.owners.set(id, lower(p.to));
      else if (action === 'burn') n.owners.delete(id);
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
    moduleCall: ({ module, action, params }) => answerRead(module, action, params ?? {}),
    call: (req) => ({ return_data: '0x' + (contracts.get(lower(req.to)) ?? 0n).toString(16).padStart(64, '0') }),
    // Throwing here fails the send, like a revert, before any fee is charged.
    onSend: (call, res) => {
      const o = (call.args[0] ?? {}) as Record<string, unknown>;
      const fee = o.fee ? big(o.fee) : DEFAULT_FEE;
      if (call.method === 'sendTransfer') {
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
