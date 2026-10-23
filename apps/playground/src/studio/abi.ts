import type { ContractFunction, ContractParam } from '@splendor/dapp-spec';

const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;
const SIGNATURE_RE = /^[A-Za-z_$][A-Za-z0-9_$]*\([^()]*\)$/;

export const isValidAddress = (value: string): boolean => ADDRESS_RE.test(value.trim());
export const isValidSignature = (value: string): boolean => SIGNATURE_RE.test(value.trim());

/** Parameters from the types in a signature: `set(uint256 value, address)` -> value:uint256, arg1:address. */
export function paramsFromSignature(signature: string): ContractParam[] {
  const inner = signature.slice(signature.indexOf('(') + 1, signature.lastIndexOf(')')).trim();
  if (!inner) return [];
  return inner.split(',').map((part, i) => {
    const [type, name] = part.trim().split(/\s+/);
    return { name: name || `arg${i}`, type: type ?? '' };
  });
}

type AbiParam = { name?: unknown; type?: unknown; components?: unknown };

function abiType(p: AbiParam): string {
  if (typeof p.type !== 'string' || !p.type) throw new Error('a parameter has no type');
  if (p.type.startsWith('tuple')) {
    if (!Array.isArray(p.components)) throw new Error('a tuple parameter has no components');
    return `(${(p.components as AbiParam[]).map(abiType).join(',')})${p.type.slice('tuple'.length)}`;
  }
  return p.type;
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);

/**
 * Functions from pasted JSON: either `[{name, signature, kind: 'read'|'write', params?}]` or an
 * sqc-solc style ABI (`[{type?, name, inputs, stateMutability}]`, view/pure -> read, else write).
 * Returns an error message instead of ever dropping input silently.
 */
export function parseFunctionsJson(text: string): { functions: ContractFunction[]; error: string | null } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (e) {
    return { functions: [], error: `Not valid JSON: ${e instanceof Error ? e.message : 'parse error'}` };
  }
  if (!Array.isArray(parsed)) return { functions: [], error: 'Expected a JSON array of functions.' };
  const functions: ContractFunction[] = [];
  for (let i = 0; i < parsed.length; i += 1) {
    const item = parsed[i] as unknown;
    const at = `Entry ${i + 1}`;
    if (!isObject(item)) return { functions: [], error: `${at}: expected an object.` };
    const type = item.type;
    if (typeof type === 'string' && type !== 'function') continue; // ABI constructors, events, errors…
    if (typeof item.signature === 'string') {
      const signature = item.signature.trim();
      if (!isValidSignature(signature)) return { functions: [], error: `${at}: signature "${signature}" must look like name(type,type).` };
      if (item.kind !== 'read' && item.kind !== 'write') return { functions: [], error: `${at}: kind must be "read" or "write".` };
      let params: ContractParam[];
      if (item.params === undefined) params = paramsFromSignature(signature);
      else if (
        Array.isArray(item.params) &&
        item.params.every((p) => isObject(p) && typeof p.name === 'string' && typeof p.type === 'string')
      ) {
        params = (item.params as ContractParam[]).map((p) => ({ name: p.name, type: p.type }));
      } else return { functions: [], error: `${at}: params must be a list of {name, type}.` };
      const name = typeof item.name === 'string' && item.name ? item.name : signature.slice(0, signature.indexOf('('));
      functions.push({
        name,
        signature,
        params,
        kind: item.kind,
        ...(typeof item.returns === 'string' && item.returns ? { returns: item.returns } : {}),
      });
      continue;
    }
    if (typeof item.name === 'string' && ('inputs' in item || 'stateMutability' in item || type === 'function')) {
      try {
        const inputs = item.inputs === undefined ? [] : item.inputs;
        if (!Array.isArray(inputs)) throw new Error('inputs must be an array');
        const types = (inputs as AbiParam[]).map(abiType);
        const signature = `${item.name}(${types.join(',')})`;
        if (!isValidSignature(signature)) throw new Error(`"${signature}" is not supported (tuple parameters are not supported yet)`);
        const mutability = typeof item.stateMutability === 'string' ? item.stateMutability : item.constant === true ? 'view' : 'nonpayable';
        const outputs = Array.isArray(item.outputs) ? (item.outputs as AbiParam[]).map(abiType) : [];
        functions.push({
          name: item.name,
          signature,
          params: (inputs as AbiParam[]).map((p, idx) => ({ name: typeof p.name === 'string' && p.name ? p.name : `arg${idx}`, type: types[idx]! })),
          kind: mutability === 'view' || mutability === 'pure' ? 'read' : 'write',
          ...(outputs.length ? { returns: outputs.length === 1 ? outputs[0]! : `(${outputs.join(',')})` } : {}),
        });
      } catch (e) {
        return { functions: [], error: `${at}: ${e instanceof Error ? e.message : 'invalid ABI entry'}.` };
      }
      continue;
    }
    return { functions: [], error: `${at}: needs {name, signature, kind} or an ABI entry {name, inputs, stateMutability}.` };
  }
  if (!functions.length) return { functions: [], error: 'No functions found in that JSON.' };
  return { functions, error: null };
}

/** `name(type, type name)` -> `name(type,type)`: the canonical signature without parameter names. */
function canonicalSignature(signature: string): string {
  const open = signature.indexOf('(');
  const types = paramsFromSignature(signature).map((p) => p.type);
  return `${signature.slice(0, open).trim()}(${types.join(',')})`;
}

const LINE_RE = /^(.*\))\s*(?:\b(read|write|view|pure)\b)?\s*$/i;

/**
 * One function per line: `get() read`, `set(uint256 value) write`, or just `name(type)`. A line
 * ending in `write` is a write; anything else (`read`, `view`, `pure`, nothing) is a read.
 * Blank lines and `#` / `//` comments are skipped.
 */
export function parseSignatureLines(text: string): { functions: ContractFunction[]; error: string | null } {
  const functions: ContractFunction[] = [];
  const lines = text.split(/\r?\n/);
  for (let i = 0; i < lines.length; i += 1) {
    const raw = lines[i]!.trim().replace(/[;,]+$/, '').trim();
    if (!raw || raw.startsWith('#') || raw.startsWith('//')) continue;
    const at = `Line ${i + 1}`;
    const m = LINE_RE.exec(raw);
    if (!m) return { functions: [], error: `${at}: write one function per line like "get() read" or "set(uint256) write".` };
    const written = m[1]!.trim();
    if (!isValidSignature(written)) return { functions: [], error: `${at}: "${written}" must look like name(type,type).` };
    const params = paramsFromSignature(written);
    if (params.some((p) => !p.type)) return { functions: [], error: `${at}: every parameter needs a type.` };
    const signature = canonicalSignature(written);
    functions.push({
      name: signature.slice(0, signature.indexOf('(')),
      signature,
      params,
      kind: m[2]?.toLowerCase() === 'write' ? 'write' : 'read',
    });
  }
  if (!functions.length) return { functions: [], error: 'No functions found. Write one per line, like "get() read".' };
  return { functions, error: null };
}

export type AbiParseResult = { functions: ContractFunction[] } | { error: string };

/**
 * Functions from whatever the user pasted or uploaded: `{name, signature, kind}` JSON, an sqc-solc
 * ABI (a JSON array, or an object with an `abi` array), or signature lines.
 */
export function parseAbiInput(text: string): AbiParseResult {
  const trimmed = text.trim();
  if (!trimmed) return { error: 'Paste an ABI, or one function per line like "get() read".' };
  if (trimmed.startsWith('[') || trimmed.startsWith('{')) {
    let json = trimmed;
    if (trimmed.startsWith('{')) {
      // A compiler artifact: {"abi": [...]}.
      try {
        const obj = JSON.parse(trimmed) as unknown;
        const inner = isObject(obj) ? (Array.isArray(obj.abi) ? obj.abi : Array.isArray(obj.functions) ? obj.functions : null) : null;
        if (!inner) return { error: 'Expected a JSON array of functions, or an object with an "abi" array.' };
        json = JSON.stringify(inner);
      } catch (e) {
        return { error: `Not valid JSON: ${e instanceof Error ? e.message : 'parse error'}` };
      }
    }
    const r = parseFunctionsJson(json);
    return r.error ? { error: r.error } : { functions: r.functions };
  }
  const r = parseSignatureLines(trimmed);
  return r.error ? { error: r.error } : { functions: r.functions };
}
