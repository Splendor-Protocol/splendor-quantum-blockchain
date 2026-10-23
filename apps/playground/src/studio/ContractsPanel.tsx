import { useState, type DragEvent } from 'react';
import type { ContractFunction, ContractRef, DappSpec } from '@splendor/dapp-spec';
import type { Dispatch } from 'react';
import type { BuilderAction } from '../builder/state';
import { isValidAddress, isValidSignature, parseAbiInput, paramsFromSignature } from './abi';

// The ABI + signature parsing lives in ./abi; these re-exports keep older imports working.
export { isValidAddress, isValidSignature, paramsFromSignature, parseFunctionsJson } from './abi';

function slugId(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 40) || 'contract';
}

/** `base`, or `base-2`, `base-3`… when `base` is taken. Never overwrites an existing contract. */
export function uniqueContractId(base: string, existing: readonly string[]): string {
  const taken = new Set(existing);
  if (!taken.has(base)) return base;
  let n = 2;
  while (taken.has(`${base}-${n}`)) n += 1;
  return `${base}-${n}`;
}

/** What pressing Try shows. Nothing is sent: it only describes the call. */
export function describeTry(contract: Pick<ContractRef, 'name' | 'address'>, fn: Pick<ContractFunction, 'signature' | 'kind'>, mode: 'mock' | 'live'): string {
  const address = contract.address.trim() || '(no address yet)';
  return `Would call ${fn.signature} on ${address} in ${mode} mode`;
}

type FieldErrors = { address?: string; fnSig?: string; fnJson?: string };

function FieldError({ id, message }: { id: string; message?: string }) {
  return message ? (
    <small id={id} className="pg-error" role="alert">
      {message}
    </small>
  ) : null;
}

/**
 * A contract's address, editable in place. Every component bound to the contract (and the export's
 * `CONTRACTS` entry) follows the saved value.
 */
function AddressEditor({ contract, onSave }: { contract: ContractRef; onSave: (address: string) => void }) {
  const [value, setValue] = useState(contract.address);
  const [error, setError] = useState<string | null>(null);
  const [synced, setSynced] = useState(contract.address);
  if (synced !== contract.address) {
    setSynced(contract.address);
    setValue(contract.address);
    setError(null);
  }
  const dirty = value.trim() !== contract.address;
  const errorId = `contract-address-error-${contract.id}`;
  const save = () => {
    const next = value.trim();
    if (next && !isValidAddress(next)) {
      setError('Enter an address like SQ: followed by 64 hex characters.');
      return;
    }
    setError(null);
    onSave(next);
  };
  return (
    <form
      className="pg-row pg-contract-address"
      onSubmit={(e) => {
        e.preventDefault();
        save();
      }}
    >
      <input
        className="pg-mono"
        aria-label={`${contract.name} address`}
        placeholder={contract.kind === 'token' ? 'Your token address (SQ:…)' : 'Contract address (SQ:…)'}
        value={value}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : undefined}
        onChange={(e) => {
          setValue(e.target.value);
          setError(null);
        }}
      />
      <button type="submit" className="pg-btn pg-btn-sm" disabled={!dirty}>
        Save
      </button>
      <FieldError id={errorId} message={error ?? undefined} />
    </form>
  );
}

export function ContractsPanel({
  spec,
  dispatch,
  onInsertContractBlock,
  mode = 'mock',
}: {
  spec: DappSpec;
  dispatch: Dispatch<BuilderAction>;
  /** Preview mode, shown in the Try dialog. */
  mode?: 'mock' | 'live';
  onInsertContractBlock: (widget: 'contract-read' | 'contract-write', props: Record<string, string>) => void;
}) {
  const contracts = spec.contracts ?? [];
  const [name, setName] = useState('');
  const [address, setAddress] = useState('');
  const [fnJson, setFnJson] = useState('');
  const [fnSig, setFnSig] = useState('get()');
  const [fnKind, setFnKind] = useState<'read' | 'write'>('read');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [added, setAdded] = useState<string | null>(null);
  const [tryTarget, setTryTarget] = useState<{ contract: ContractRef; fn: ContractFunction } | null>(null);

  const saveContracts = (next: ContractRef[]) => dispatch({ type: 'setContracts', contracts: next });
  const clearError = (key: keyof FieldErrors) => {
    setAdded(null);
    setErrors((prev) => (prev[key] ? { ...prev, [key]: undefined } : prev));
  };

  const addContract = () => {
    const next: FieldErrors = {};
    const trimmedAddress = address.trim();
    if (!isValidAddress(trimmedAddress)) next.address = 'Enter an address like SQ: followed by 64 hex characters.';
    let functions: ContractFunction[] = [];
    if (fnJson.trim()) {
      const result = parseAbiInput(fnJson);
      if ('error' in result) next.fnJson = result.error;
      else functions = result.functions;
    } else if (fnSig.trim()) {
      const signature = fnSig.trim();
      if (!isValidSignature(signature)) next.fnSig = 'Use the form name(type,type), for example transfer(address,uint256).';
      else functions = [{ name: signature.slice(0, signature.indexOf('(')), signature, params: paramsFromSignature(signature), kind: fnKind }];
    }
    if (next.address || next.fnSig || next.fnJson) {
      setErrors(next);
      setAdded(null);
      return;
    }
    const base = slugId(name.trim() || 'contract');
    const id = uniqueContractId(base, contracts.map((c) => c.id));
    const label = name.trim() || id;
    saveContracts([...contracts, { id, name: label, address: trimmedAddress, functions }]);
    setErrors({});
    setAdded(`Added ${label}${id !== base ? ` (id ${id})` : ''}.`);
    setName('');
    setAddress('');
    setFnJson('');
    setFnSig('get()');
  };

  const removeContract = (c: ContractRef) => {
    if (!window.confirm(`Remove contract "${c.name}"? Components that use it will ask you to pick another contract.`)) return;
    if (tryTarget?.contract.id === c.id) setTryTarget(null);
    saveContracts(contracts.filter((x) => x.id !== c.id));
  };

  const startFnDrag = (event: DragEvent, contract: ContractRef, fn: ContractFunction) => {
    event.dataTransfer.setData(
      'application/x-splendor-contract-fn',
      JSON.stringify({ contractId: contract.id, address: contract.address, fn }),
    );
    event.dataTransfer.effectAllowed = 'copy';
  };

  return (
    <div className="pg-stack">
      <p className="pg-toybox-lead">Contracts</p>
      {contracts.length === 0 ? <p className="pg-muted">No contracts yet. Add one below.</p> : null}
      <ul className="pg-contracts-list">
        {contracts.map((c) => (
          <li key={c.id}>
            <div className="pg-row">
              <strong>{c.name}</strong>
              <button type="button" className="pg-icon-btn" aria-label={`Remove ${c.name}`} onClick={() => removeContract(c)}>
                ×
              </button>
            </div>
            <AddressEditor
              contract={c}
              onSave={(next) => dispatch({ type: 'setContractAddress', id: c.id, address: next })}
            />
            <ul className="pg-contract-fns">
              {c.functions.map((fn) => (
                <li key={`${fn.name}-${fn.signature}`}>
                  <button
                    type="button"
                    className="pg-link-btn"
                    draggable
                    onDragStart={(e) => startFnDrag(e, c, fn)}
                    onClick={() =>
                      onInsertContractBlock(fn.kind === 'write' ? 'contract-write' : 'contract-read', {
                        contractId: c.id,
                        functionSig: fn.signature,
                        title: `${c.name}: ${fn.name}`,
                        ...(fn.kind === 'write' ? { buttonLabel: fn.name } : {}),
                      })
                    }
                  >
                    {fn.kind === 'write' ? 'Write' : 'Read'} · {fn.signature}
                  </button>
                  <button
                    type="button"
                    className="pg-btn pg-btn-ghost pg-btn-sm"
                    data-testid={`contract-try-${c.id}-${fn.name}`}
                    aria-label={`Try ${fn.signature}`}
                    onClick={() => setTryTarget({ contract: c, fn })}
                  >
                    Try
                  </button>
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
      {tryTarget ? (
        <div className="pg-panel pg-stack" role="dialog" aria-label="Try contract call" data-testid="contract-try-dialog">
          <strong>{tryTarget.contract.name}: {tryTarget.fn.name}</strong>
          <p data-testid="contract-try-result" role="status" aria-live="polite" style={{ margin: 0 }}>
            {describeTry(tryTarget.contract, tryTarget.fn, mode)}
          </p>
          <p className="pg-muted" style={{ margin: 0 }}>
            {tryTarget.fn.kind === 'write' ? 'This is a write: live mode asks your wallet to sign it.' : 'This is a read: it needs no wallet.'}{' '}
            {mode === 'mock' ? 'Mock mode sends nothing.' : 'Switch to the block on the page to run it for real.'}
          </p>
          <div className="pg-row">
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setTryTarget(null)}>
              Close
            </button>
          </div>
        </div>
      ) : null}
      <p className="pg-list-title">Add contract</p>
      <div className="pg-form">
        <div className="pg-field">
          <label htmlFor="contract-name">Name</label>
          <input id="contract-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="My contract" />
        </div>
        <div className="pg-field">
          <label htmlFor="contract-address">Address</label>
          <input
            id="contract-address"
            value={address}
            aria-invalid={errors.address ? true : undefined}
            aria-describedby={errors.address ? 'contract-address-error' : undefined}
            onChange={(e) => {
              setAddress(e.target.value);
              clearError('address');
            }}
            placeholder="SQ:…"
          />
          <FieldError id="contract-address-error" message={errors.address} />
        </div>
        <div className="pg-field">
          <label htmlFor="contract-fn-sig">Function signature</label>
          <input
            id="contract-fn-sig"
            value={fnSig}
            aria-invalid={errors.fnSig ? true : undefined}
            aria-describedby={errors.fnSig ? 'contract-fn-sig-error' : undefined}
            onChange={(e) => {
              setFnSig(e.target.value);
              clearError('fnSig');
            }}
            placeholder="get()"
          />
          <FieldError id="contract-fn-sig-error" message={errors.fnSig} />
        </div>
        <div className="pg-field">
          <label htmlFor="contract-fn-kind">Kind</label>
          <select id="contract-fn-kind" value={fnKind} onChange={(e) => setFnKind(e.target.value as 'read' | 'write')}>
            <option value="read">Read</option>
            <option value="write">Write</option>
          </select>
        </div>
        <div className="pg-field">
          <label htmlFor="contract-fn-json">Or functions JSON / ABI</label>
          <textarea
            id="contract-fn-json"
            rows={3}
            value={fnJson}
            aria-invalid={errors.fnJson ? true : undefined}
            aria-describedby={errors.fnJson ? 'contract-fn-json-error' : undefined}
            onChange={(e) => {
              setFnJson(e.target.value);
              clearError('fnJson');
            }}
            placeholder={'[{"name":"get","signature":"get()","kind":"read"}], an ABI, or one per line:\nget() read\nset(uint256) write'}
          />
          <FieldError id="contract-fn-json-error" message={errors.fnJson} />
        </div>
        <button type="button" className="pg-btn pg-btn-sm" onClick={addContract}>
          Add contract
        </button>
        <p className="pg-muted" role="status" aria-live="polite" data-testid="contract-added" style={{ margin: 0 }}>
          {added}
        </p>
      </div>
    </div>
  );
}
