import { useState, type DragEvent } from 'react';
import type { ContractFunction, ContractRef, DappSpec } from '@splendor/dapp-spec';
import type { Dispatch } from 'react';
import type { BuilderAction } from '../builder/state';

function slugId(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 40) || 'contract';
}

/** What pressing Try shows. Nothing is sent: it only describes the call. */
export function describeTry(contract: Pick<ContractRef, 'name' | 'address'>, fn: Pick<ContractFunction, 'signature' | 'kind'>, mode: 'mock' | 'live'): string {
  const address = contract.address.trim() || '(no address yet)';
  return `Would call ${fn.signature} on ${address} in ${mode} mode`;
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
  const [tryTarget, setTryTarget] = useState<{ contract: ContractRef; fn: ContractFunction } | null>(null);

  const saveContracts = (next: ContractRef[]) => dispatch({ type: 'setContracts', contracts: next });

  const addContract = () => {
    const id = slugId(name || 'contract');
    let functions: ContractFunction[] = [];
    if (fnJson.trim()) {
      try {
        const parsed = JSON.parse(fnJson) as unknown;
        functions = Array.isArray(parsed) ? (parsed as ContractFunction[]) : [];
      } catch {
        return;
      }
    } else if (fnSig.trim()) {
      functions = [{ name: fnSig.split('(')[0] || 'call', signature: fnSig, params: [], kind: fnKind }];
    }
    const ref: ContractRef = { id, name: name || id, address, functions };
    saveContracts([...contracts.filter((c) => c.id !== id), ref]);
    setName('');
    setAddress('');
    setFnJson('');
    setFnSig('get()');
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
              <button type="button" className="pg-icon-btn" aria-label={`Remove ${c.name}`} onClick={() => saveContracts(contracts.filter((x) => x.id !== c.id))}>
                ×
              </button>
            </div>
            <code className="pg-mono pg-muted">{c.address || 'No address'}</code>
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
                        contract: c.address,
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
          <p data-testid="contract-try-result" style={{ margin: 0 }}>
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
          <input id="contract-address" value={address} onChange={(e) => setAddress(e.target.value)} placeholder="SQ:…" />
        </div>
        <div className="pg-field">
          <label htmlFor="contract-fn-sig">Function signature</label>
          <input id="contract-fn-sig" value={fnSig} onChange={(e) => setFnSig(e.target.value)} placeholder="get()" />
        </div>
        <div className="pg-field">
          <label htmlFor="contract-fn-kind">Kind</label>
          <select id="contract-fn-kind" value={fnKind} onChange={(e) => setFnKind(e.target.value as 'read' | 'write')}>
            <option value="read">Read</option>
            <option value="write">Write</option>
          </select>
        </div>
        <div className="pg-field">
          <label htmlFor="contract-fn-json">Or functions JSON</label>
          <textarea id="contract-fn-json" rows={3} value={fnJson} onChange={(e) => setFnJson(e.target.value)} placeholder='[{"name":"get","signature":"get()","params":[],"kind":"read"}]' />
        </div>
        <button type="button" className="pg-btn pg-btn-sm" onClick={addContract}>
          Add contract
        </button>
      </div>
    </div>
  );
}
