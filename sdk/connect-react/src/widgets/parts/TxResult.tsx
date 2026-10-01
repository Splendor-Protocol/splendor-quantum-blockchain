import { truncateAddress } from '../utils.js';
import { COMMON_LABELS, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';
import { CopyButton } from './AddressDisplay.js';

export type TxResultProps = {
  hash?: string | null;
  /** true: "Confirmed"; otherwise "Submitted". */
  confirmed?: boolean;
  explorerUrl?: string | null;
  /** Extra line above the hash, e.g. the new token address. */
  extra?: { label: string; value: string } | null;
  labels?: Partial<Pick<CommonWidgetLabels, 'submitted' | 'confirmed' | 'copy' | 'copied' | 'explorer'>>;
};

const shorten = (v: string) => (v.length > 24 ? truncateAddress(v, 10, 8) : v);

/** Result of a widget action: status word, hash with copy, explorer link. */
export function TxResult({ hash, confirmed, explorerUrl, extra, labels }: TxResultProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  if (!hash && !extra) return null;
  return (
    <div className="sqc-tx-result" role="status" aria-live="polite">
      {extra ? (
        <p className="sqc-widget__msg sqc-tx-result__row">
          <span title={extra.value}>
            {extra.label}: {shorten(extra.value)}
          </span>
          <CopyButton text={extra.value} labels={l} />
        </p>
      ) : null}
      {hash ? (
        <p className="sqc-widget__msg sqc-tx-result__row">
          <span title={hash}>
            {confirmed ? l.confirmed : l.submitted}: {shorten(hash)}
          </span>
          <CopyButton text={hash} labels={l} />
          {explorerUrl ? (
            <a href={explorerUrl} target="_blank" rel="noreferrer" className="sqc-account-link">
              {l.explorer}
            </a>
          ) : null}
        </p>
      ) : null}
    </div>
  );
}
