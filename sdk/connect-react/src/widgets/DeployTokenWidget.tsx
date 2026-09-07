import { useState } from 'react';
import { useAccount, useDeployToken } from '../react/hooks.js';

export type DeployTokenWidgetProps = {
  className?: string;
  defaultName?: string;
  defaultSymbol?: string;
  defaultSupply?: string;
};

/**
 * Drop-in QRC-20 deploy form (uses useDeployToken).
 */
export function DeployTokenWidget({
  className,
  defaultName = 'Demo',
  defaultSymbol = 'DMO',
  defaultSupply = '1000000',
}: DeployTokenWidgetProps) {
  const { isConnected } = useAccount();
  const { deploy, status, address, hash, error, reset } = useDeployToken();
  const [name, setName] = useState(defaultName);
  const [symbol, setSymbol] = useState(defaultSymbol);
  const [supply, setSupply] = useState(defaultSupply);

  if (!isConnected) {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Deploy QRC-20</h3>
        <p className="sqc-widget__hint">Connect a wallet to deploy a token.</p>
      </div>
    );
  }

  const onDeploy = async () => {
    reset();
    try {
      await deploy({
        name: name.trim(),
        symbol: symbol.trim(),
        decimals: 18,
        totalSupply: supply.trim(),
        waitForConfirmation: true,
      });
    } catch {
      /* error state */
    }
  };

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
        Total supply (units)
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
        disabled={status === 'pending'}
        onClick={() => void onDeploy()}
      >
        {status === 'pending' ? 'Deploying…' : 'Deploy token'}
      </button>
      {address ? <p className="sqc-widget__msg">Token: {address}</p> : null}
      {hash ? <p className="sqc-widget__msg">Tx: {hash}</p> : null}
      {error ? <p className="sqc-widget__msg sqc-widget__msg--error">{error.message}</p> : null}
    </div>
  );
}
