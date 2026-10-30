import type { Connection, SqcProvider } from '@splendor/sqc-sdk';
import type { NetworkConfig } from '@splendor/sqc-sdk/networks';

/** What a recipe gets when it runs in the browser against the selected network. */
export type RecipeContext = {
  network: NetworkConfig;
  /** Read-only RPC (no wallet needed). */
  provider: SqcProvider;
  /** Read connection used by module/token reads; null until the provider is ready. */
  connection: Connection | null;
  /** Connected address, or null. */
  address: string | null;
  /** Signing wallet, or null when not connected. Recipes that sign must check `requiresWallet`. */
  wallet: { signMessage(message: string): Promise<unknown> } | null;
  /** Values of the recipe's inputs. */
  inputs: Record<string, string>;
  log: (line: string) => void;
};

/**
 * A runnable SDK snippet for the Learn area. `code` is what the user reads and copies; `run`
 * does the same thing in the playground. New modules add recipes here, grouped by `module`.
 */
export type SdkRecipeManifest = {
  id: string;
  title: string;
  module: 'network' | 'account' | 'qrc20' | 'qrc721' | 'qrc1155' | 'auth' | 'contract' | 'bridge' | (string & {});
  description: string;
  requiresWallet?: boolean;
  inputs?: { name: string; label: string; placeholder?: string; defaultValue?: string }[];
  code: string;
  run: (ctx: RecipeContext) => Promise<unknown>;
};

const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;
function needAddress(v: string | undefined, label: string): string {
  const s = (v ?? '').trim();
  if (!ADDRESS_RE.test(s)) throw new Error(`${label} must be an SQ: address (SQ: + 64 hex)`);
  return s;
}

export const RECIPES: readonly SdkRecipeManifest[] = [
  {
    id: 'network-status',
    title: 'Node status',
    module: 'network',
    description: 'Ask the RPC for its chain id and latest height. No wallet needed.',
    code: `import { SqcProvider } from '@splendor/sqc-sdk';

const provider = new SqcProvider({ network: 'testnet' });
const status = await provider.getStatus();
console.log(status);`,
    run: async ({ provider }) => provider.getStatus(),
  },
  {
    id: 'latest-block',
    title: 'Latest block',
    module: 'network',
    description: 'Read the newest block header.',
    code: `const block = await provider.getLatestBlock();
console.log(block);`,
    run: async ({ provider }) => provider.getLatestBlock(),
  },
  {
    id: 'balance',
    title: 'SPLD balance of an address',
    module: 'account',
    description: 'Balance in atto and SPLD. Leave empty to use the connected wallet.',
    inputs: [{ name: 'address', label: 'Address', placeholder: 'SQ:… (empty: connected wallet)' }],
    code: `const { balance, balance_spld } = await provider.getBalance('SQ:…');`,
    run: async ({ provider, inputs, address }) => {
      const who = inputs.address?.trim() ? needAddress(inputs.address, 'Address') : address;
      if (!who) throw new Error('Connect a wallet or enter an address');
      return provider.getBalance(who);
    },
  },
  {
    id: 'qrc20-info',
    title: 'QRC-20 token info',
    module: 'qrc20',
    description: 'Name, symbol, decimals and supply of a QRC-20 token.',
    inputs: [{ name: 'token', label: 'Token address', placeholder: 'SQ:…' }],
    code: `import { QRC20 } from '@splendor/sqc-sdk/tokens';

const token = new QRC20(connection, 'SQ:…');
console.log(await token.tokenInfo());`,
    run: async ({ connection, inputs }) => {
      if (!connection) throw new Error('Read connection not ready yet');
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
      return new QRC20(connection, needAddress(inputs.token, 'Token address')).tokenInfo();
    },
  },
  {
    id: 'qrc20-balance',
    title: 'QRC-20 balance',
    module: 'qrc20',
    description: 'Token balance (base units) of the connected wallet or any address.',
    inputs: [
      { name: 'token', label: 'Token address', placeholder: 'SQ:…' },
      { name: 'owner', label: 'Owner', placeholder: 'SQ:… (empty: connected wallet)' },
    ],
    code: `const balance = await new QRC20(connection, tokenAddress).balanceOf(owner);`,
    run: async ({ connection, inputs, address }) => {
      if (!connection) throw new Error('Read connection not ready yet');
      const owner = inputs.owner?.trim() ? needAddress(inputs.owner, 'Owner') : address;
      if (!owner) throw new Error('Connect a wallet or enter an owner');
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
      return { balance: await new QRC20(connection, needAddress(inputs.token, 'Token address')).balanceOf(owner) };
    },
  },
  {
    id: 'qrc721-info',
    title: 'QRC-721 collection info',
    module: 'qrc721',
    description: 'Name, symbol, owner and total supply of an NFT collection.',
    inputs: [{ name: 'collection', label: 'Collection address', placeholder: 'SQ:…' }],
    code: `import { QRC721 } from '@splendor/sqc-sdk/tokens';

console.log(await new QRC721(connection, 'SQ:…').collectionInfo());`,
    run: async ({ connection, inputs }) => {
      if (!connection) throw new Error('Read connection not ready yet');
      const { QRC721 } = await import('@splendor/sqc-sdk/tokens');
      return new QRC721(connection, needAddress(inputs.collection, 'Collection address')).collectionInfo();
    },
  },
  {
    id: 'qrc721-owners',
    title: 'NFT collection page: who owns what',
    module: 'qrc721',
    description:
      'QRC-721 has no enumeration read, so a collection page checks a range of ids with ownerOf. Unminted ids throw. Drop the "QRC-721 NFT panel" widget to get this with send and mint.',
    inputs: [
      { name: 'collection', label: 'Collection address', placeholder: 'SQ:…' },
      { name: 'ids', label: 'Token ids', defaultValue: '1-6' },
    ],
    code: `import { QRC721 } from '@splendor/sqc-sdk/tokens';

const nft = new QRC721(connection, 'SQ:…');
const owners = {};
for (let id = 1; id <= 6; id++) {
  owners[id] = await nft.ownerOf(String(id)).catch(() => null); // null = not minted
}

// React: <NftPanel collectionAddress="SQ:…" tokenIds="1-6" />`,
    run: async ({ connection, inputs, address }) => {
      if (!connection) throw new Error('Read connection not ready yet');
      const { QRC721 } = await import('@splendor/sqc-sdk/tokens');
      const nft = new QRC721(connection, needAddress(inputs.collection, 'Collection address'));
      const m = /^(\d+)-(\d+)$/.exec((inputs.ids ?? '').trim() || '1-6');
      const [from, to] = m ? [Number(m[1]), Math.min(Number(m[2]), Number(m[1]) + 49)] : [1, 6];
      const owners: Record<string, string | null> = {};
      for (let id = from; id <= to; id++) owners[id] = await nft.ownerOf(String(id)).catch(() => null);
      const mine = address ? Object.keys(owners).filter((id) => owners[id] === address) : [];
      return { collection: await nft.collectionInfo(), owners, yours: mine };
    },
  },
  {
    id: 'validators-list',
    title: 'Validator dashboard',
    module: 'validators',
    description: 'The active validator set and the registration bond, from the routed validators module reads. No wallet needed.',
    code: `// Module reads go through the router like token reads (POST /call).
const list = await connection.moduleCall({ module: 'validators', action: 'list', params: {} });
const bond = await connection.moduleCall({ module: 'validators', action: 'bond', params: {} });

// React: const { validators } = useValidators();  or drop the "Validator directory" widget.`,
    run: async ({ connection }) => {
      if (!connection) throw new Error('Read connection not ready yet');
      const list = await connection
        .moduleCall<unknown[]>({ module: 'validators', action: 'list', params: {} })
        .catch((e: unknown) => (e instanceof Error && /decoded result is empty/.test(e.message) ? [] : Promise.reject(e)));
      const bond = await connection.moduleCall<Record<string, unknown>>({ module: 'validators', action: 'bond', params: {} });
      const { parseValidator } = await import('@splendor/connect-react');
      return { count: Array.isArray(list) ? list.length : 0, bond, validators: (Array.isArray(list) ? list : []).map(parseValidator) };
    },
  },
  {
    id: 'activity-logs',
    title: 'Token activity from event logs',
    module: 'activity',
    description:
      'QRC-20 Transfer events touching an address in the last 2000 blocks (the node limit per query). Topics are SHA3-256 of the event signature; an indexed address is its 32 raw bytes.',
    inputs: [{ name: 'owner', label: 'Address', placeholder: 'SQ:… (empty: connected wallet)' }],
    code: `import { TOPIC_TRANSFER, mergeActivity } from '@splendor/connect-react';

const me = 'SQ:…';
const { height } = await connection.getStatus();
const window = { from_block: Math.max(0, height - 2000), to_block: height };
const sent = await connection.getLogs({ ...window, topics: [TOPIC_TRANSFER, me.slice(3)] });
const received = await connection.getLogs({ ...window, topics: [TOPIC_TRANSFER, '', me.slice(3)] });
console.log(mergeActivity([...sent.logs, ...received.logs], me));

// React: const { items } = useActivity();  or drop the "Activity feed" widget.`,
    run: async ({ connection, inputs, address }) => {
      if (!connection) throw new Error('Read connection not ready yet');
      const me = inputs.owner?.trim() ? needAddress(inputs.owner, 'Address') : address;
      if (!me) throw new Error('Connect a wallet or enter an address');
      const { TOPIC_TRANSFER, mergeActivity } = await import('@splendor/connect-react');
      const status = (await connection.getStatus()) as { height?: number };
      const head = Number(status.height) || 0;
      const window = { from_block: Math.max(0, head - 2000), to_block: head };
      const topic = me.slice(3).toLowerCase();
      const [sent, received] = await Promise.all([
        connection.getLogs({ ...window, topics: [TOPIC_TRANSFER, topic] }),
        connection.getLogs({ ...window, topics: [TOPIC_TRANSFER, '', topic] }),
      ]);
      return { window, activity: mergeActivity([...(sent.logs ?? []), ...(received.logs ?? [])], me) };
    },
  },
  {
    id: 'x402-quote',
    title: 'Paid content: read an x402 price',
    module: 'x402',
    description:
      'A server behind createX402Middleware answers 402 with payment requirements. Empty URL: show what such a server would send. The "Paywall (x402)" block runs the whole pay-and-retry flow.',
    inputs: [
      { name: 'url', label: 'Paid resource URL', placeholder: 'https://api.example.com/premium (empty: demo)' },
      { name: 'payTo', label: 'Demo pay-to', defaultValue: 'SQ:' + '5e'.repeat(32) },
      { name: 'price', label: 'Demo price (SPLD)', defaultValue: '0.1' },
    ],
    code: `// Server (Express-style):
import { createX402Middleware } from '@splendor/sqc-sdk/x402';
app.use('/premium', createX402Middleware({ payTo: 'SQ:…', price: '0.1', connection }));

// Client: get the price, pay with the wallet, retry with the payment.
const first = await fetch(url);                       // 402
const { requirements } = await first.json();          // { pay_to, amount (atto) }
const { payload } = await wallet.payX402({ to: requirements.pay_to, amount: requirements.amount, requirements, settle: true });
const paid = await fetch(url, { headers: { 'X-Payment': JSON.stringify(payload) } });`,
    run: async ({ connection, inputs }) => {
      const url = inputs.url?.trim();
      if (url) {
        if (!/^https:\/\//i.test(url) && !/^http:\/\/(localhost|127\.0\.0\.1)[:/]/i.test(url)) {
          throw new Error('URL must be https (http only for localhost)');
        }
        const res = await fetch(url, { credentials: 'omit' });
        const body = await res.json().catch(() => null);
        return { status: res.status, requirements: (body as { requirements?: unknown } | null)?.requirements ?? null };
      }
      if (!connection) throw new Error('Read connection not ready yet');
      const { X402Client } = await import('@splendor/sqc-sdk/x402');
      return {
        demo: true,
        status: 402,
        requirements: X402Client.fromConnection(connection).buildRequirements(needAddress(inputs.payTo, 'Demo pay-to'), inputs.price?.trim() || '0.1'),
      };
    },
  },
  {
    id: 'qrc1155-balance',
    title: 'QRC-1155 balance',
    module: 'qrc1155',
    description: 'How many of token `id` an owner holds.',
    inputs: [
      { name: 'collection', label: 'Collection address', placeholder: 'SQ:…' },
      { name: 'id', label: 'Token id', defaultValue: '1' },
      { name: 'owner', label: 'Owner', placeholder: 'SQ:… (empty: connected wallet)' },
    ],
    code: `import { QRC1155 } from '@splendor/sqc-sdk/tokens';

const balance = await new QRC1155(connection, collection).balanceOf(owner, '1');`,
    run: async ({ connection, inputs, address }) => {
      if (!connection) throw new Error('Read connection not ready yet');
      const owner = inputs.owner?.trim() ? needAddress(inputs.owner, 'Owner') : address;
      if (!owner) throw new Error('Connect a wallet or enter an owner');
      const { QRC1155 } = await import('@splendor/sqc-sdk/tokens');
      return { balance: await new QRC1155(connection, needAddress(inputs.collection, 'Collection address')).balanceOf(owner, inputs.id?.trim() || '1') };
    },
  },
  {
    id: 'sign-message',
    title: 'Sign a message',
    module: 'auth',
    description: 'ML-DSA-65 signature over a domain-separated message. Your wallet asks for approval.',
    requiresWallet: true,
    inputs: [{ name: 'message', label: 'Message', defaultValue: 'Hello from the Splendor playground' }],
    code: `const signed = await wallet.signMessage('Hello from the Splendor playground');
// { message, signatureB64, publicKeyB64, address } — verify on a server with verifySignIn / verifyMessage`,
    run: async ({ wallet, inputs }) => {
      if (!wallet) throw new Error('Connect a wallet first');
      return wallet.signMessage(inputs.message || 'Hello');
    },
  },
];

export const RECIPE_MODULES = [...new Set(RECIPES.map((r) => r.module))];
