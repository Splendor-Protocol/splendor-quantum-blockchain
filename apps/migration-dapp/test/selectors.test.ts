/**
 * The hardcoded 4-byte selectors and the Burned topic0 are pure functions of
 * the contract ABI. This test recomputes every one of them with a real
 * keccak-256 (@noble/hashes, devDependency only — the runtime ships none), so
 * a transcription error can never survive `npm test`.
 */
import { describe, expect, it } from 'vitest';
import { keccak_256 } from '@noble/hashes/sha3';
import {
  SEL_BURN_SPLD,
  SEL_BURN_SPLV,
  SEL_APPROVE,
  SEL_ALLOWANCE,
  SEL_BALANCE_OF,
  BURNED_TOPIC0,
} from '../src/selectors';

function hex(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

const keccakHex = (sig: string) => hex(keccak_256(new TextEncoder().encode(sig)));
const selector = (sig: string) => '0x' + keccakHex(sig).slice(0, 8);

describe('function selectors match keccak-256 of the canonical signatures', () => {
  it('burnSpld(bytes32)', () => {
    expect(SEL_BURN_SPLD).toBe(selector('burnSpld(bytes32)'));
  });
  it('burnSplv(uint256,bytes32)', () => {
    expect(SEL_BURN_SPLV).toBe(selector('burnSplv(uint256,bytes32)'));
  });
  it('approve(address,uint256)', () => {
    expect(SEL_APPROVE).toBe(selector('approve(address,uint256)'));
  });
  it('allowance(address,address)', () => {
    expect(SEL_ALLOWANCE).toBe(selector('allowance(address,address)'));
  });
  it('balanceOf(address)', () => {
    expect(SEL_BALANCE_OF).toBe(selector('balanceOf(address)'));
  });
});

describe('event topic', () => {
  it('Burned(uint256,address,uint8,uint256,bytes32)', () => {
    expect(BURNED_TOPIC0).toBe('0x' + keccakHex('Burned(uint256,address,uint8,uint256,bytes32)'));
  });

  it('is 32 bytes, selectors are 4', () => {
    expect(BURNED_TOPIC0).toMatch(/^0x[0-9a-f]{64}$/);
    for (const s of [SEL_BURN_SPLD, SEL_BURN_SPLV, SEL_APPROVE, SEL_ALLOWANCE, SEL_BALANCE_OF]) {
      expect(s).toMatch(/^0x[0-9a-f]{8}$/);
    }
  });
});

describe('well-known ERC-20 selectors as an independent cross-check', () => {
  // These three values are canon across the entire EVM ecosystem. If the local
  // keccak disagreed with THESE, the test harness itself would be broken.
  it('matches the published ERC-20 selector values', () => {
    expect(SEL_APPROVE).toBe('0x095ea7b3');
    expect(SEL_ALLOWANCE).toBe('0xdd62ed3e');
    expect(SEL_BALANCE_OF).toBe('0x70a08231');
  });
});
