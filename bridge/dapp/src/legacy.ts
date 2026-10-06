// Legacy EVM side: MetaMask + the QuantumMigrationBurn contract on chain 2691.
// Kept in one module so the React UI is only about the user flow.
import { BrowserProvider, Contract, parseUnits, formatUnits, id } from 'ethers';

const BURN_CONTRACT = (import.meta.env.VITE_BURN_CONTRACT ?? '').trim();
const SPLV_TOKEN = (import.meta.env.VITE_SPLV_TOKEN ?? '').trim() || '0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689';
const LEGACY_CHAIN_ID = Number(import.meta.env.VITE_LEGACY_CHAIN_ID ?? '2691');

// Minimal ABI: only the functions/event the migration path needs.
const BURN_ABI = [
  'function burnSpld(bytes32 quantumRecipient) payable',
  'function burnSplv(uint256 amount, bytes32 quantumRecipient)',
  'event Burned(uint256 indexed id, address indexed sender, uint8 indexed asset, uint256 amount, bytes32 quantumRecipient)',
];
const ERC20_ABI = [
  'function balanceOf(address) view returns (uint256)',
  'function approve(address spender, uint256 value) returns (bool)',
  'function allowance(address owner, address spender) view returns (uint256)',
];

// topic0 of the Burned event (keccak256 of the canonical signature). Matching on
// this — instead of "any log with >=2 topics" — keeps the SPLV path's earlier
// ERC-20 Transfer(address,address,uint256) log from being read as the burn.
const BURNED_TOPIC0 = id('Burned(uint256,address,uint8,uint256,bytes32)');

export interface LegacyState {
  address: string | null;
  chainId: number | null;
  spld: string | null;
  splv: string | null;
}

type Eip1193Provider = { request(args: { method: string; params?: unknown[] }): Promise<unknown> };

function eth(): Eip1193Provider {
  const w = (window as unknown as { ethereum?: Eip1193Provider }).ethereum;
  if (!w) throw new Error('No EVM wallet found. Install MetaMask and add the Splendor network (chain 2691).');
  return w;
}

function provider(): BrowserProvider {
  return new BrowserProvider(eth() as never);
}

function hexToDecimal(s: string): number {
  return parseInt(String(s), 16);
}

// sqToBytes32 converts an "SQ:hex" quantum address to the 32-byte form the
// burn contract expects (the 64 lowercase hex chars after the prefix).
export function sqToBytes32(sq: string): string {
  const hex = sq.replace(/^SQ:/i, '').replace(/^0x/, '').toLowerCase();
  if (hex.length !== 64) throw new Error(`Invalid quantum address "${sq}" (want SQ: + 64 hex chars)`);
  return '0x' + hex;
}

// connectLegacy requests the MetaMask account and reads both balances.
export async function connectLegacy(): Promise<LegacyState> {
  const p = provider();
  const accounts = (await p.send('eth_requestAccounts', [])) as string[];
  const address = accounts[0] ?? null;
  const rawChain = (await p.send('eth_chainId', [])) as string;
  const chainId = hexToDecimal(rawChain);

  let spld: string | null = null;
  let splv: string | null = null;
  if (address) {
    spld = formatUnits((await p.getBalance(address)).toString(), 18);
    const tok = new Contract(SPLV_TOKEN, ERC20_ABI, p);
    splv = formatUnits((await tok.balanceOf(address)).toString(), 18);
  }
  return { address, chainId, spld, splv };
}

// burnNative sends SPLD (msg.value) to burnSpld and returns the Burned id.
export async function burnNative(wholeAmount: string, recipientBytes32: string): Promise<string> {
  const p = provider();
  const signer = await p.getSigner();
  const c = new Contract(BURN_CONTRACT, BURN_ABI, signer);
  const value = parseUnits(wholeAmount, 18);
  const tx = await c.burnSpld(recipientBytes32, { value });
  const receipt = await tx.wait();
  return burnIdFromReceipt(receipt);
}

// burnToken approves (if needed) then burns SPLV to burnSplv, returning the id.
export async function burnToken(wholeAmount: string, recipientBytes32: string): Promise<string> {
  const p = provider();
  const signer = await p.getSigner();
  const amount = parseUnits(wholeAmount, 18);
  const tok = new Contract(SPLV_TOKEN, ERC20_ABI, signer);
  const allowance = await tok.allowance(await signer.getAddress(), BURN_CONTRACT);
  if (allowance < amount) {
    const ap = await tok.approve(BURN_CONTRACT, amount);
    await ap.wait();
  }
  const c = new Contract(BURN_CONTRACT, BURN_ABI, signer);
  const tx = await c.burnSplv(amount, recipientBytes32);
  const receipt = await tx.wait();
  return burnIdFromReceipt(receipt);
}

// burnIdFromReceipt pulls the Burned id out of the tx receipt logs.
function burnIdFromReceipt(receipt: { logs: Array<{ topics: string[] }> }): string {
  for (const log of receipt.logs) {
    // Burned(uint256 indexed id, address indexed sender, uint8 indexed asset, uint256 amount, bytes32 quantumRecipient):
    // topic0 is the signature hash, id is topics[1]. Match topic0 so the SPLV
    // path's earlier Transfer log (from transferFrom) can't be mistaken for it.
    if (log.topics.length >= 2 && log.topics[0]?.toLowerCase() === BURNED_TOPIC0) {
      return BigInt(log.topics[1]).toString();
    }
  }
  throw new Error('Burned event not found in the transaction receipt.');
}

export { BURN_CONTRACT, SPLV_TOKEN, LEGACY_CHAIN_ID };
