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
