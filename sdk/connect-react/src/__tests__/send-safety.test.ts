/**
 * @vitest-environment jsdom
 */
import { beforeEach, describe, expect, it } from 'vitest';
import {
  addressExplorerUrl,
  compareDecimal,
  getRecentRecipients,
  normalizeSqAddress,
  RECENT_RECIPIENTS_KEY,
  RECENT_RECIPIENTS_MAX,
  rememberRecipient,
  reviewTransfer,
  transferReviewMessage,
} from '../widgets/utils.js';
import * as root from '../index.js';

const A = 'SQ:' + 'ab'.repeat(32);
const B = 'SQ:' + 'cd'.repeat(32);
const addr = (n: number) => 'SQ:' + n.toString(16).padStart(2, '0').repeat(32);

describe('normalizeSqAddress', () => {
  it('lowercases a mixed-case SQ: address', () => {
    expect(normalizeSqAddress('SQ:' + 'AB'.repeat(32))).toBe(A);
  });

  it('trims surrounding whitespace', () => {
    expect(normalizeSqAddress(`  ${A}\n`)).toBe(A);
  });

  it.each([
    ['EVM 0x', '0x' + 'ab'.repeat(20)],
    ['raw 64-hex', 'ab'.repeat(32)],
    ['short', 'SQ:abcd'],
    ['too long', 'SQ:' + 'ab'.repeat(33)],
    ['non-hex', 'SQ:' + 'zz'.repeat(32)],
    ['empty', ''],
    ['whitespace', '   '],
  ])('rejects %s', (_label, value) => {
    expect(normalizeSqAddress(value)).toBeNull();
  });

  it('rejects null / undefined', () => {
    expect(normalizeSqAddress(null)).toBeNull();
    expect(normalizeSqAddress(undefined)).toBeNull();
  });
});

describe('compareDecimal', () => {
  it.each([
    ['1', '1', 0],
    ['1.0', '1', 0],
    ['01.50', '1.5', 0],
    ['0.1', '0.10000', 0],
    ['1', '2', -1],
    ['10', '9', 1],
    ['9.99', '10', -1],
    ['1.0000000000000000001', '1', 1],
    ['0.3', '0.29999999999999999999', 1],
    ['123456789012345678901234567890', '123456789012345678901234567891', -1],
  ] as const)('compare(%s, %s) = %s', (a, b, want) => {
    expect(compareDecimal(a, b)).toBe(want);
  });

  it.each([
    ['abc', '1'],
    ['1', ''],
    ['-1', '1'],
    ['1.', '1'],
    ['.5', '1'],
    ['1e3', '1'],
  ])('returns null for non-decimal input (%s, %s)', (a, b) => {
    expect(compareDecimal(a, b)).toBeNull();
  });
});

describe('reviewTransfer', () => {
  it('accepts a valid transfer and canonicalizes the recipient', () => {
    expect(
      reviewTransfer({ to: 'SQ:' + 'CD'.repeat(32), amount: ' 1.5 ', balance: '2', self: A }),
    ).toEqual({ ok: true, to: B, amount: '1.5' });
  });

  it('flags invalid address first', () => {
    expect(reviewTransfer({ to: '0xabc', amount: 'x', balance: '0', self: A })).toEqual({
      ok: false,
      reason: 'invalid-address',
    });
  });

  it('flags self-send regardless of case', () => {
    expect(reviewTransfer({ to: 'SQ:' + 'AB'.repeat(32), amount: '1', self: A })).toEqual({
      ok: false,
      reason: 'self',
    });
  });

  it.each(['0', '0.0', 'abc', '', '-1', '01'])('flags bad amount %s', (amount) => {
    expect(reviewTransfer({ to: B, amount, self: A })).toEqual({ ok: false, reason: 'bad-amount' });
  });

  it('flags insufficient balance', () => {
    expect(reviewTransfer({ to: B, amount: '2.0001', balance: '2', self: A })).toEqual({
      ok: false,
      reason: 'insufficient',
    });
  });

  it('allows exactly the full balance', () => {
    expect(reviewTransfer({ to: B, amount: '2', balance: '2.000', self: A }).ok).toBe(true);
  });

  it('skips the balance check when balance is unknown or unparsable', () => {
    expect(reviewTransfer({ to: B, amount: '999', balance: null }).ok).toBe(true);
    expect(reviewTransfer({ to: B, amount: '999', balance: 'n/a' }).ok).toBe(true);
  });

  it('has a message for every reason', () => {
    for (const r of ['invalid-address', 'self', 'bad-amount', 'insufficient'] as const) {
      expect(transferReviewMessage(r, 'TOK')).toBeTruthy();
    }
    expect(transferReviewMessage('insufficient', 'TOK')).toMatch(/TOK/);
  });
});

describe('recent recipients', () => {
  beforeEach(() => localStorage.clear());

  it('starts empty', () => {
    expect(getRecentRecipients()).toEqual([]);
  });

  it('stores newest first and de-duplicates', () => {
    rememberRecipient(A);
    rememberRecipient(B);
    rememberRecipient('SQ:' + 'AB'.repeat(32));
    expect(getRecentRecipients()).toEqual([A, B]);
  });

  it(`caps at ${RECENT_RECIPIENTS_MAX}`, () => {
    for (let i = 1; i <= RECENT_RECIPIENTS_MAX + 3; i += 1) rememberRecipient(addr(i));
    const list = getRecentRecipients();
    expect(list).toHaveLength(RECENT_RECIPIENTS_MAX);
    expect(list[0]).toBe(addr(RECENT_RECIPIENTS_MAX + 3));
  });

  it('ignores invalid input and never writes it', () => {
    rememberRecipient('0x' + 'ab'.repeat(20));
    rememberRecipient('a'.repeat(64));
    expect(getRecentRecipients()).toEqual([]);
    expect(localStorage.getItem(RECENT_RECIPIENTS_KEY)).toBeNull();
  });

  it('drops tampered or corrupt storage entries', () => {
    localStorage.setItem(RECENT_RECIPIENTS_KEY, JSON.stringify([A, 42, 'seed words here', B]));
    expect(getRecentRecipients()).toEqual([A, B]);
    localStorage.setItem(RECENT_RECIPIENTS_KEY, '{not json');
    expect(getRecentRecipients()).toEqual([]);
    localStorage.setItem(RECENT_RECIPIENTS_KEY, '{"a":1}');
    expect(getRecentRecipients()).toEqual([]);
  });
});

describe('addressExplorerUrl', () => {
  it('builds /address/<canonical>', () => {
    expect(addressExplorerUrl('https://explorer.example/', 'SQ:' + 'AB'.repeat(32))).toBe(
      `https://explorer.example/address/${A}`,
    );
  });

  it('returns null without explorer or with an invalid address', () => {
    expect(addressExplorerUrl('', A)).toBeNull();
    expect(addressExplorerUrl(undefined, A)).toBeNull();
    expect(addressExplorerUrl('https://explorer.example', '0xabc')).toBeNull();
    expect(addressExplorerUrl('https://explorer.example', null)).toBeNull();
  });
});

describe('root exports', () => {
  it('exposes the new helpers and widget', () => {
    for (const name of [
      'SignInButton',
      'normalizeSqAddress',
      'compareDecimal',
      'spendableAfterFee',
      'exceedsBalanceWithFee',
      'useConnectController',
      'useConnectState',
      'useNetworkSwitch',
      'reviewTransfer',
      'getRecentRecipients',
      'rememberRecipient',
      'addressExplorerUrl',
      'parseAuthMessage',
      'checkAuthMessage',
      'authCheckMessage',
    ] as const) {
      expect(typeof (root as Record<string, unknown>)[name]).toBe('function');
    }
  });
});
