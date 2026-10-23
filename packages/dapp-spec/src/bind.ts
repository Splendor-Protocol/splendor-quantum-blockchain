import type { CatalogEntry, ContractRef, DappBlock, DappSpec, PropValue } from './types.js';

export type BoundBlock = {
  /** Props to hand to the component: contract picks replaced by addresses, ABI params added. */
  props: Record<string, PropValue>;
  /**
   * Props whose value came from `spec.contracts` (prop name → contract id). Codegen emits these as
   * `CONTRACTS[id].address` so editing a contract once updates every block that uses it.
   */
  addressRefs: Record<string, string>;
};

/**
 * Resolve a block's data bindings against the spec's contracts:
 * - a `format: 'contract'` prop (a contract id) is dropped and the contract's address is written to
 *   the schema's `addressProp` (default `contract`); a token contract's `decimals` fills an unset
 *   `decimals` prop;
 * - a `format: 'function'` prop keeps its signature, and the function's ABI params are added as
 *   `fnParams` (JSON) so forms can render one typed field per argument.
 * Unknown ids leave the block's raw props untouched (validation reports them).
 */
export function bindBlockProps(block: DappBlock, entry: CatalogEntry | undefined, contracts: readonly ContractRef[] = []): BoundBlock {
  const props: Record<string, PropValue> = { ...block.props };
  const addressRefs: Record<string, string> = {};
  if (!entry) return { props, addressRefs };
  const byId = new Map(contracts.map((c) => [c.id, c]));
  for (const schema of entry.props) {
    const value = block.props[schema.name];
    if (schema.format === 'contract') {
      delete props[schema.name];
      const c = typeof value === 'string' ? byId.get(value) : undefined;
      if (!c) continue;
      const target = schema.addressProp ?? 'contract';
      if (c.address) {
        props[target] = c.address;
        addressRefs[target] = c.id;
      }
      if (c.kind === 'token' && entry.props.some((p) => p.name === 'decimals') && block.props.decimals == null) {
        props.decimals = c.decimals ?? 18;
      }
    } else if (schema.format === 'function' && typeof value === 'string' && value) {
      const owner = block.props[schema.contractProp ?? 'contractId'];
      const c = typeof owner === 'string' ? byId.get(owner) : undefined;
      const fn = c?.functions.find((f) => f.signature === value);
      if (fn) props.fnParams = JSON.stringify(fn.params);
    }
  }
  return { props, addressRefs };
}

/** `bindBlockProps` with the spec's contracts. */
export function bindBlock(spec: Pick<DappSpec, 'contracts'>, block: DappBlock, entry: CatalogEntry | undefined): BoundBlock {
  return bindBlockProps(block, entry, spec.contracts ?? []);
}

/** Split a signature's argument types: `transfer(address,uint256)` → `['address', 'uint256']`. */
export function signatureTypes(signature: string): string[] {
  const m = /\(([^()]*)\)\s*$/.exec(signature);
  return m && m[1]!.trim() ? m[1]!.split(',').map((t) => t.trim()) : [];
}
