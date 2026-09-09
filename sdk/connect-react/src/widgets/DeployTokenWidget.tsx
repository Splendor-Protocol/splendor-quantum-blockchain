import { useState } from 'react';
import { fromUnits } from '@splendor/sqc-sdk/units';
import { useAccount, useConnect, useDeployToken, useNetwork } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';

export type DeployTokenWidgetProps = {
  className?: string;
  defaultName?: string;
  defaultSymbol?: string;
  /** Human total supply (converted with decimals before deploy). Default 1_000_000. */
  defaultSupply?: string;
  decimals?: number;
};

function isValidSupply(v: string): boolean {
  const t = v.trim();
  if (!t) return false;
  const n = Number(t);
  return Number.isFinite(n) && n > 0;
}

/**
 * Drop-in QRC-20 deploy form — supply is human decimals (not raw units).
 */
export function DeployTokenWidget({
  className,
  defaultName = 'Demo',
  defaultSymbol = 'DMO',
  defaultSupply = '1000000',
  decimals = 18,
}: DeployTokenWidgetProps) {
  const { isConnected } = useAccount();
  const { status: connStatus, openModal } = useConnect();
  const { network } = useNetwork();
  const { deploy, status, address, hash, error, reset } = useDeployToken();
  const [name, setName] = useState(defaultName);
  const [symbol, setSymbol] = useState(defaultSymbol);
  const [supply, setSupply] = useState(defaultSupply);

  if (connStatus === 'locked') {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Deploy QRC-20</h3>
        <p className="sqc-widget__hint">Wallet locked — unlock to deploy.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Unlock
        </button>
      </div>
    );
  }

  if (!isConnected) {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Deploy QRC-20</h3>
        <p className="sqc-widget__hint">Connect a wallet to deploy a token.</p>
      </div>
    );
  }

  const canDeploy =
    !!name.trim() && !!symbol.trim() && isValidSupply(supply) && status !== 'pending';

  const onDeploy = async () => {
    if (!canDeploy) return;
    reset();
    try {
      const totalSupply = fromUnits(supply.trim(), decimals);
      await deploy({
        name: name.trim(),
        symbol: symbol.trim(),
        decimals,
        totalSupply,
        waitForConfirmation: true,
      });
    } catch {
      /* error state */
    }
  };

  const explorer = network && hash ? txExplorerUrl(network, hash) : null;

  return (
    <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
      <h3 className="sqc-widget__title">Deploy QRC-20</h3>
      <label className="sqc-widget__label" htmlFor="sqc-dep-name">
        Name
      </label>
      <input
        id="sqc-dep-name"
        className="sqc-input"
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
      <label className="sqc-widget__label" htmlFor="sqc-dep-sym">
        Symbol
      </label>
      <input
        id="sqc-dep-sym"
        className="sqc-input"
        value={symbol}
        onChange={(e) => setSymbol(e.target.value)}
      />
      <label className="sqc-widget__label" htmlFor="sqc-dep-sup">
        Total supply (human, {decimals} decimals)
      </label>
      <input
        id="sqc-dep-sup"
        className="sqc-input"
        value={supply}
        onChange={(e) => setSupply(e.target.value)}
      />
      <button
        type="button"
        className="sqc-btn sqc-btn--primary"
        style={{ marginTop: '0.75rem', width: '100%' }}
        disabled={!canDeploy}
        onClick={() => void onDeploy()}
      >
        {status === 'pending' ? 'Deploying…' : 'Deploy token'}
      </button>
      {address ? <p className="sqc-widget__msg">Token: {address}</p> : null}
      {hash ? (
        <p className="sqc-widget__msg">
          Tx: {hash}
          {explorer ? (
            <>
              {' · '}
              <a href={explorer} target="_blank" rel="noreferrer" className="sqc-account-link">
                Explorer
              </a>
            </>
          ) : null}
        </p>
      ) : null}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error">{connectErrorMessage(error)}</p>
      ) : null}
    </div>
  );
}
