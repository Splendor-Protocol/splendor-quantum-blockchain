import { describe, expect, it } from 'vitest';
import { isValidAddress, isValidSignature, parseAbiInput, parseFunctionsJson, parseSignatureLines, paramsFromSignature } from './abi';

const ok = (text: string) => {
  const r = parseAbiInput(text);
  if ('error' in r) throw new Error(r.error);
  return r.functions;
};
const err = (text: string) => {
  const r = parseAbiInput(text);
  if (!('error' in r)) throw new Error('expected an error');
  return r.error;
};

describe('isValidAddress / isValidSignature', () => {
  it('wants SQ: and 64 hex characters', () => {
    expect(isValidAddress(`SQ:${'ab'.repeat(32)}`)).toBe(true);
    expect(isValidAddress(`  SQ:${'AB'.repeat(32)} `)).toBe(true);
    expect(isValidAddress('SQ:123')).toBe(false);
    expect(isValidAddress(`sq:${'ab'.repeat(32)}`)).toBe(false);
    expect(isValidAddress('')).toBe(false);
  });
  it('wants name(types)', () => {
    expect(isValidSignature('get()')).toBe(true);
    expect(isValidSignature('transfer(address,uint256)')).toBe(true);
    expect(isValidSignature('get')).toBe(false);
    expect(isValidSignature('f((uint256,uint256))')).toBe(false);
  });
  it('reads params from a signature', () => {
    expect(paramsFromSignature('set(uint256 value, address)')).toEqual([
      { name: 'value', type: 'uint256' },
      { name: 'arg1', type: 'address' },
    ]);
    expect(paramsFromSignature('get()')).toEqual([]);
  });
});

describe('parseAbiInput: {name, signature, kind} JSON', () => {
  it('accepts the studio format', () => {
    const fns = ok('[{"name":"get","signature":"get()","kind":"read"},{"name":"set","signature":"set(uint256 value)","kind":"write"}]');
    expect(fns.map((f) => [f.name, f.kind, f.params.length])).toEqual([
      ['get', 'read', 0],
      ['set', 'write', 1],
    ]);
  });
  it('matches parseFunctionsJson', () => {
    const text = '[{"name":"get","signature":"get()","kind":"read"}]';
    expect(ok(text)).toEqual(parseFunctionsJson(text).functions);
  });
});

describe('parseAbiInput: sqc-solc ABI', () => {
  const abi = [
    { type: 'constructor', inputs: [] },
    { type: 'function', name: 'get', inputs: [], outputs: [{ type: 'uint256' }], stateMutability: 'view' },
    { type: 'function', name: 'set', inputs: [{ name: 'value', type: 'uint256' }], outputs: [], stateMutability: 'nonpayable' },
    { type: 'event', name: 'Changed', inputs: [{ name: 'v', type: 'uint256' }] },
  ];
  it('maps view/pure to read and the rest to write, skipping events and constructors', () => {
    const fns = ok(JSON.stringify(abi));
    expect(fns).toEqual([
      { name: 'get', signature: 'get()', params: [], kind: 'read', returns: 'uint256' },
      { name: 'set', signature: 'set(uint256)', params: [{ name: 'value', type: 'uint256' }], kind: 'write' },
    ]);
  });
  it('unwraps a compiler artifact with an abi array', () => {
    expect(ok(JSON.stringify({ contractName: 'C', abi })).map((f) => f.name)).toEqual(['get', 'set']);
  });
  it('rejects tuple parameters instead of dropping them', () => {
    const tuple = [{ type: 'function', name: 'f', inputs: [{ name: 'p', type: 'tuple', components: [{ type: 'uint256' }] }], stateMutability: 'view' }];
    expect(err(JSON.stringify(tuple))).toMatch(/not supported/);
  });
});

describe('parseAbiInput: one signature per line', () => {
  it('reads "get() read" and "set(uint256) write", defaulting to read', () => {
    const fns = ok('get() read\nset(uint256) write\nbalanceOf(address)');
    expect(fns.map((f) => [f.signature, f.kind])).toEqual([
      ['get()', 'read'],
      ['set(uint256)', 'write'],
      ['balanceOf(address)', 'read'],
    ]);
  });
  it('keeps parameter names and canonicalises the signature', () => {
    const [fn] = ok('transfer(address to, uint256 amount) write');
    expect(fn).toEqual({
      name: 'transfer',
      signature: 'transfer(address,uint256)',
      kind: 'write',
      params: [
        { name: 'to', type: 'address' },
        { name: 'amount', type: 'uint256' },
      ],
    });
  });
  it('accepts view/pure, any case, CRLF, comments and blank lines', () => {
    const fns = ok('# my contract\r\n\r\nGET() VIEW\r\n// writes\r\nset(uint256) WRITE;\r\n');
    expect(fns.map((f) => [f.name, f.kind])).toEqual([
      ['GET', 'read'],
      ['set', 'write'],
    ]);
  });
  it('names the broken line', () => {
    expect(err('get() read\nnot a signature')).toMatch(/^Line 2:/);
    expect(err('get() sideways')).toMatch(/^Line 1:/);
    expect(err('set(uint256 value, ) write')).toMatch(/every parameter needs a type|must look like/);
  });
  it('is what parseSignatureLines returns', () => {
    expect(parseSignatureLines('get() read').functions).toEqual(ok('get() read'));
  });
});

describe('parseAbiInput: errors', () => {
  it('asks for input when empty', () => {
    expect(err('   ')).toMatch(/Paste an ABI/);
  });
  it('reports broken JSON and JSON of the wrong shape', () => {
    expect(err('{nope')).toMatch(/Not valid JSON/);
    expect(err('[nope')).toMatch(/Not valid JSON/);
    expect(err('{"a":1}')).toMatch(/abi/);
    expect(err('[]')).toMatch(/No functions/);
    expect(err('[1]')).toMatch(/Entry 1/);
  });
});
