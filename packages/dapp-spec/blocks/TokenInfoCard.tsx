import { useEffect, useState } from 'react';
import { WidgetShell, connectErrorMessage, useTokenInfo } from '@splendor/connect-react';

export type TokenInfoCardProps = {
  title?: string;
  tokenAddress?: string;
};

/** Shows name, symbol and decimals for a QRC-20. */
export function TokenInfoCard({ title = 'Token', tokenAddress = '' }: TokenInfoCardProps) {
  const info = useTokenInfo(tokenAddress || null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setError(null);
  }, [tokenAddress]);

  if (!tokenAddress) {
    return (
      <WidgetShell kind="token-info" title={title}>
        <p className="sqc-widget__msg">Set a token address.</p>
      </WidgetShell>
    );
  }

  if (info.error) {
    return (
      <WidgetShell kind="token-info" title={title}>
        <p className="sqc-widget__error">{connectErrorMessage(info.error)}</p>
      </WidgetShell>
    );
  }

  if (!info.data && info.loading) {
    return (
      <WidgetShell kind="token-info" title={title}>
        <p className="sqc-widget__msg">Loading…</p>
      </WidgetShell>
    );
  }

  const d = info.data;
  return (
    <WidgetShell kind="token-info" title={title}>
      {d ? (
        <dl className="dapp-stats">
          <div>
            <dt>Name</dt>
            <dd>{d.name}</dd>
          </div>
          <div>
            <dt>Symbol</dt>
            <dd>{d.symbol}</dd>
          </div>
          <div>
            <dt>Decimals</dt>
            <dd>{d.decimals}</dd>
          </div>
        </dl>
      ) : (
        <p className="sqc-widget__msg">{error ?? 'No data'}</p>
      )}
    </WidgetShell>
  );
}
