import { useState, type CSSProperties, type ReactNode } from 'react';
import { connectErrorMessage } from '../core/controller.js';
import { useAccount, useNetwork, useValidators, type ValidatorStatus } from '../react/hooks.js';
import { addressExplorerUrl, normalizeSqAddress } from './utils.js';
import { VALIDATOR_LABELS, formatLabel, useWidgetLabels, type ValidatorListLabels } from './labels.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressDisplay } from './parts/AddressDisplay.js';
import { AddressInput } from './parts/inputs.js';

export type ValidatorListProps = {
  className?: string;
  style?: CSSProperties;
  title?: ReactNode;
  hideTitle?: boolean;
  labels?: Partial<ValidatorListLabels>;
  /** Show the admission-approval lookup. Default true. */
  showAdmissionCheck?: boolean;
  /** Rows to show. Default 50. */
  maxRows?: number;
  /** Refresh interval (ms). Default 30000. */
  pollMs?: number;
};

/** Active validator set with bonds, read from the chain without a wallet. Read-only. */
export function ValidatorList({
  className,
  style,
  title,
  hideTitle,
  labels,
  showAdmissionCheck = true,
  maxRows = 50,
  pollMs = 30_000,
}: ValidatorListProps) {
  const l = useWidgetLabels(VALIDATOR_LABELS, labels, 'validators');
  const { address } = useAccount();
  const { network } = useNetwork();
  const { validators, data, loading, error, refresh, isApproved } = useValidators({ pollMs });
  const [query, setQuery] = useState('');
  const [approval, setApproval] = useState<{ address: string; approved: boolean } | null>(null);
  const [checkError, setCheckError] = useState<string | null>(null);
  const statusText: Record<ValidatorStatus, string> = { active: l.active, exiting: l.exiting, inactive: l.inactive };
  const me = normalizeSqAddress(address);
  const bond = data?.bondSpld ?? null;

  const onCheck = async () => {
    const target = normalizeSqAddress(query);
    if (!target) return;
    setCheckError(null);
    try {
      setApproval({ address: target, approved: await isApproved(target) });
    } catch (e) {
      setApproval(null);
      setCheckError(connectErrorMessage(e));
    }
  };

  return (
    <WidgetShell
      kind="validators"
      className={className}
      style={style}
      title={title ?? l.title}
      hideTitle={hideTitle}
      gate={false}
      headerExtra={
        <button type="button" className="sqc-copy" onClick={() => void refresh()} disabled={loading}>
          {loading ? '…' : l.refresh}
        </button>
      }
    >
      {data ? (
        <p className="sqc-widget__hint">
          {bond ? formatLabel(l.count, { count: validators.length, bond }) : formatLabel(l.countNoBond, { count: validators.length })}
        </p>
      ) : null}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {formatLabel(l.loadFailed, { error: connectErrorMessage(error) })}
        </p>
      ) : null}
      {data && !validators.length ? <p className="sqc-widget__hint">{l.empty}</p> : null}
      {!data && loading ? <p className="sqc-widget__hint">{l.loading}</p> : null}
      {validators.length ? (
        <table className="sqc-table">
          <thead>
            <tr>
              <th scope="col">{l.address}</th>
              <th scope="col">{l.bond}</th>
              <th scope="col">{l.since}</th>
              <th scope="col">{l.status}</th>
            </tr>
          </thead>
          <tbody>
            {validators.slice(0, Math.max(1, maxRows)).map((v) => (
              <tr key={v.address} data-testid="sqc-validator">
                <td>
                  <AddressDisplay value={v.address} href={addressExplorerUrl(network?.explorerUrl, v.address)} labels={l} />
                  {me === v.address ? <span className="sqc-connector-item__badge sqc-connector-item__badge--ok">{l.you}</span> : null}
                </td>
                <td>{v.bondSpld != null ? `${v.bondSpld} SPLD` : '—'}</td>
                <td>{v.joinedHeight ?? '—'}</td>
                <td>{statusText[v.status]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {showAdmissionCheck ? (
        <>
          <AddressInput label={l.checkLabel} value={query} onChange={(v) => { setQuery(v); setApproval(null); }} labels={l} />
          <button
            type="button"
            className="sqc-btn sqc-btn--ghost sqc-btn--block"
            disabled={!normalizeSqAddress(query)}
            onClick={() => void onCheck()}
          >
            {l.check}
          </button>
          {approval ? (
            <p className="sqc-widget__msg" role="status">
              {approval.approved ? l.approved : l.notApproved}
            </p>
          ) : null}
          {checkError ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {checkError}
            </p>
          ) : null}
        </>
      ) : null}
    </WidgetShell>
  );
}
