/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it } from 'vitest';
import React, { type ReactNode } from 'react';
import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { useActivity, useValidators } from '../react/hooks.js';
import {
  TOPIC_TRANSFER,
  TOPIC_TRANSFER_BATCH,
  TOPIC_TRANSFER_SINGLE,
  activityFilters,
  dataToHex,
  mergeActivity,
  parseActivityLog,
  safeIndexerUrl,
  toSqAddress,
} from '../core/activity.js';
import { MOCK_ADDRESS, MockSplendorProvider, createMockConnectController, mockWallet } from '../testing.js';

const ME = MOCK_ADDRESS;
const OTHER = 'SQ:' + 'cd'.repeat(32);
const TOKEN = 'SQ:' + 'ee'.repeat(32);
const ZERO = 'SQ:' + '0'.repeat(64);

const hex = (a: string) => a.slice(3);
const bytes = (h: string) => Array.from({ length: h.length / 2 }, (_, i) => parseInt(h.slice(i * 2, i * 2 + 2), 16));
const word = (n: bigint) => n.toString(16).padStart(64, '0');
const b64 = (h: string) => btoa(String.fromCharCode(...bytes(h)));

/** A log the way the node serializes it: byte arrays as number arrays, data as base64. */
function nodeLog(topics: string[], dataHex: string, extra: Partial<{ height: number; index: number; tx: string; address: string }> = {}) {
  return {
    address: bytes(hex(extra.address ?? TOKEN)),
    topics: topics.map(bytes),
    data: b64(dataHex),
    block_height: extra.height ?? 10,
    tx_hash: bytes(extra.tx ?? '12'.repeat(32)),
    tx_index: 0,
    log_index: extra.index ?? 0,
  };
}

const matches = (filter: { topics?: string[] }, log: ReturnType<typeof nodeLog>) =>
  (filter.topics ?? []).every((t, i) => !t || log.topics[i]?.map((b) => b.toString(16).padStart(2, '0')).join('') === t);

afterEach(() => cleanup());

describe('activity log parsing', () => {
  it('reads a QRC-20 Transfer in node encoding as an outgoing row', () => {
    const log = nodeLog([TOPIC_TRANSFER, hex(ME), hex(OTHER)], word(5n * 10n ** 18n), { height: 42, index: 3 });
    const item = parseActivityLog(log, ME)!;
    expect(item).toMatchObject({
      kind: 'token',
      direction: 'out',
      contract: TOKEN,
      from: ME,
      to: OTHER,
      amount: (5n * 10n ** 18n).toString(),
      blockHeight: 42,
      logIndex: 3,
      mint: false,
      burn: false,
      txHash: '12'.repeat(32),
    });
  });

  it('accepts 0x hex fields too, and flags mints', () => {
    const item = parseActivityLog(
      { address: TOKEN, topics: ['0x' + TOPIC_TRANSFER, '0x' + '00'.repeat(32), '0x' + hex(ME)], data: '0x' + word(7n) },
      ME,
    )!;
    expect(item.direction).toBe('in');
    expect(item.mint).toBe(true);
    expect(item.from).toBe(ZERO);
    expect(item.amount).toBe('7');
    expect(item.txHash).toBeNull();
  });

  it('decodes QRC-1155 single and batch transfers', () => {
    const single = parseActivityLog(
      nodeLog([TOPIC_TRANSFER_SINGLE, hex(OTHER), hex(OTHER), hex(ME)], word(9n) + word(2n)),
      ME,
    )!;
    expect(single).toMatchObject({ kind: 'multi-token', direction: 'in', tokenId: '9', amount: '2' });
    const batch = parseActivityLog(
      nodeLog([TOPIC_TRANSFER_BATCH, hex(ME), hex(ME), hex(OTHER)], word(2n) + word(1n) + word(4n) + word(3n) + word(5n)),
      ME,
    )!;
    expect(batch).toMatchObject({ direction: 'out', tokenId: '1,4', amount: '8' });
  });

  it('ignores logs that do not touch the address, unknown events and malformed batches', () => {
    expect(parseActivityLog(nodeLog([TOPIC_TRANSFER, hex(OTHER), hex(TOKEN)], word(1n)), ME)).toBeNull();
    expect(parseActivityLog(nodeLog(['ab'.repeat(32), hex(ME)], word(1n)), ME)).toBeNull();
    expect(
      parseActivityLog(nodeLog([TOPIC_TRANSFER_BATCH, hex(ME), hex(ME), hex(OTHER)], word(3n) + word(1n)), ME),
    ).toBeNull();
    expect(parseActivityLog(null, ME)).toBeNull();
  });

  it('de-duplicates rows seen by several filters and sorts newest first', () => {
    const a = nodeLog([TOPIC_TRANSFER, hex(ME), hex(ME)], word(1n), { height: 5, tx: 'aa'.repeat(32) });
    const b = nodeLog([TOPIC_TRANSFER, hex(OTHER), hex(ME)], word(2n), { height: 9, tx: 'bb'.repeat(32) });
    const rows = mergeActivity([a, b, a], ME);
    expect(rows.map((r) => r.amount)).toEqual(['2', '1']);
    expect(rows[1]!.direction).toBe('self');
    expect(mergeActivity([a, b], ME, 1)).toHaveLength(1);
  });

  it('builds sent / received filters with the address as a raw 32-byte topic', () => {
    const f = activityFilters(ME);
    expect(f).toHaveLength(6);
    expect(f[0]!.topics).toEqual([TOPIC_TRANSFER, hex(ME)]);
    expect(f[1]!.topics).toEqual([TOPIC_TRANSFER, '', hex(ME)]);
    expect(() => activityFilters('0x1234')).toThrow();
  });

  it('normalizes byte, hex and base64 inputs', () => {
    expect(toSqAddress(bytes(hex(OTHER)))).toBe(OTHER);
    expect(toSqAddress('SQ:' + 'CD'.repeat(32))).toBe(OTHER);
    expect(toSqAddress([1, 2, 3])).toBeNull();
    expect(dataToHex(b64('00ff'))).toBe('00ff');
    expect(dataToHex('')).toBe('');
  });

  it('only allows https indexers, or http on localhost, without credentials', () => {
    expect(safeIndexerUrl('https://idx.example/')).toBe('https://idx.example');
    expect(safeIndexerUrl('http://localhost:8090')).toBe('http://localhost:8090');
    expect(safeIndexerUrl('http://idx.example')).toBeNull();
    expect(safeIndexerUrl('https://u:p@idx.example')).toBeNull();
    expect(safeIndexerUrl('javascript:alert(1)')).toBeNull();
  });
});

function wrapperFor(ctrl: ReturnType<typeof createMockConnectController>) {
  return ({ children }: { children: ReactNode }) =>
    React.createElement(MockSplendorProvider, { controller: ctrl }, children);
}

describe('useActivity', () => {
  it('queries the last window of blocks and returns parsed rows for the connected account', async () => {
    const logs = [
      nodeLog([TOPIC_TRANSFER, hex(ME), hex(OTHER)], word(3n), { height: 4990, tx: 'aa'.repeat(32) }),
      nodeLog([TOPIC_TRANSFER, hex(OTHER), hex(ME)], word(4n), { height: 4995, tx: 'bb'.repeat(32) }),
      nodeLog([TOPIC_TRANSFER, hex(OTHER), hex(TOKEN)], word(9n), { height: 4996, tx: 'cc'.repeat(32) }),
    ];
    const wallet = mockWallet({ height: 5000, logs: (filter) => logs.filter((l) => matches(filter, l)) });
    const ctrl = createMockConnectController({ wallet });
    const { result } = renderHook(() => useActivity(null, { pollMs: 0 }), { wrapper: wrapperFor(ctrl) });
    await waitFor(() => expect(result.current.items).toHaveLength(2));
    expect(result.current.items.map((i) => i.direction)).toEqual(['in', 'out']);
    expect(result.current.data).toMatchObject({ fromBlock: 3000, toBlock: 5000, source: 'node', truncated: false });
    const calls = wallet.calls.filter((c) => c.method === 'connection.getLogs');
    expect(calls).toHaveLength(6);
    expect(calls[0]!.args[0]).toMatchObject({ from_block: 3000, to_block: 5000 });
  });

  it('caps the node window at 2000 blocks and does nothing without an address', async () => {
    const wallet = mockWallet({ height: 10 });
    const ctrl = createMockConnectController({ wallet, status: 'disconnected' });
    const { result } = renderHook(() => useActivity(undefined, { pollMs: 0 }), { wrapper: wrapperFor(ctrl) });
    expect(result.current.items).toEqual([]);
    expect(wallet.calls.some((c) => c.method === 'connection.getLogs')).toBe(false);
    cleanup();
    const w2 = mockWallet({ height: 100_000 });
    const c2 = createMockConnectController({ wallet: w2, status: 'disconnected' });
    const r2 = renderHook(() => useActivity(OTHER, { pollMs: 0, blocks: 50_000 }), { wrapper: wrapperFor(c2) });
    await waitFor(() => expect(r2.result.current.data).not.toBeNull());
    expect(r2.result.current.data!.fromBlock).toBe(98_000);
  });
});

describe('useValidators', () => {
  it('parses the routed validators.list read and the bond', async () => {
    const wallet = mockWallet({
      moduleCall: ({ module, action }) => {
        if (module !== 'validators') return {};
        if (action === 'list') {
          return [
            { address: bytes(hex(OTHER)), bond: 2.5e21, status: 1, joined_height: 7 },
            { address: 'not-an-address', bond: 1, status: 1 },
          ];
        }
        if (action === 'bond') return { bond: '2500000000000000000000', spld: '2500' };
        if (action === 'approved') return { approved: true };
        return {};
      },
    });
    const ctrl = createMockConnectController({ wallet, status: 'disconnected' });
    const { result } = renderHook(() => useValidators({ pollMs: 0 }), { wrapper: wrapperFor(ctrl) });
    await waitFor(() => expect(result.current.validators).toHaveLength(1));
    expect(result.current.validators[0]).toEqual({
      address: OTHER,
      bondSpld: '2500',
      status: 'active',
      joinedHeight: 7,
      exitHeight: null,
    });
    expect(result.current.data?.bondSpld).toBe('2500');
    await expect(result.current.isApproved(OTHER)).resolves.toBe(true);
  });

  it('treats the empty-set result as no validators, not an error', async () => {
    const wallet = mockWallet({ moduleCall: () => ({}) });
    wallet.connection.moduleCall = (async (env: { action: string }) => {
      if (env.action === 'list') throw new Error('moduleCall: decoded result is empty');
      return { spld: '2500' };
    }) as never;
    const ctrl = createMockConnectController({ wallet });
    const { result } = renderHook(() => useValidators({ pollMs: 0 }), { wrapper: wrapperFor(ctrl) });
    await waitFor(() => expect(result.current.data).not.toBeNull());
    expect(result.current.validators).toEqual([]);
    expect(result.current.error).toBeNull();
  });
});
