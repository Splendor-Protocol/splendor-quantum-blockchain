import { useId, useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { buildPaymentUri } from '../core/paymentUri.js';
import { useAccount, useNetwork } from '../react/hooks.js';
import { addressExplorerUrl, isPositiveAmount } from './utils.js';
import { RECEIVE_LABELS, formatLabel, useWidgetLabels, type ReceiveWidgetLabels } from './labels.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressDisplay, CopyButton } from './parts/AddressDisplay.js';
import { QrCode } from './parts/QrCode.js';

export type ReceiveWidgetProps = {
  className?: string;
  style?: CSSProperties;
  title?: ReactNode;
  hideTitle?: boolean;
  labels?: Partial<ReceiveWidgetLabels>;
  /** Let the user add an amount to the payment link. Default true. */
  showAmount?: boolean;
  /** Pre-filled request amount (SPLD). */
  defaultAmount?: string;
  /** QR code width in px. Default 176. */
  qrSize?: number;
  /** Called after the address or link is copied. */
  onCopy?: (text: string) => void;
};

/**
 * Receive screen: the connected `SQ:` address with copy and explorer link, a QR code, and an
 * optional amount that turns the QR into a `splendor:` payment link bound to this chain.
 */
export function ReceiveWidget({
  className,
  style,
  title,
  hideTitle,
  labels,
  showAmount = true,
  defaultAmount = '',
  qrSize = 176,
  onCopy,
}: ReceiveWidgetProps) {
  const l = useWidgetLabels(RECEIVE_LABELS, labels, 'receive');
  const { address } = useAccount();
  const { network } = useNetwork();
  const amountId = `${useId()}-amount`;
  const [amount, setAmount] = useState(defaultAmount);
  const amountOk = !amount.trim() || isPositiveAmount(amount, 18);
  const link = useMemo(() => {
    if (!address) return null;
    try {
      return buildPaymentUri({
        address,
        amount: amount.trim() && amountOk ? amount.trim() : undefined,
        chainId: network?.chainId,
      });
    } catch {
      return null;
    }
  }, [address, amount, amountOk, network?.chainId]);
  const withAmount = !!amount.trim() && amountOk;
  const qrValue = withAmount ? link : address;

  return (
    <WidgetShell kind="receive" className={className} style={style} title={title ?? l.title} hideTitle={hideTitle} gateLabels={l}>
      {address ? (
        <>
          <div className="sqc-receive__qr">
            {qrValue ? (
              <QrCode value={qrValue} size={qrSize} label={formatLabel(l.qrLabel, { target: withAmount ? link : address })} />
            ) : null}
          </div>
          <span className="sqc-widget__label">{l.addressLabel}</span>
          <AddressDisplay
            value={address}
            truncate={[14, 10]}
            href={addressExplorerUrl(network?.explorerUrl, address)}
            labels={l}
            onCopy={onCopy}
          />
          {showAmount ? (
            <div className="sqc-field">
              <label className="sqc-widget__label" htmlFor={amountId}>
                {l.amountLabel}
              </label>
              <input
                id={amountId}
                className="sqc-input"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                inputMode="decimal"
                autoComplete="off"
                placeholder="0.0"
                aria-invalid={!amountOk}
              />
              {!amountOk ? (
                <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
                  {l.badAmount}
                </p>
              ) : null}
            </div>
          ) : null}
          {withAmount && link ? (
            <p className="sqc-widget__msg sqc-tx-result__row">
              <code className="sqc-receive__link" title={link}>
                {link.length > 48 ? `${link.slice(0, 30)}…${link.slice(-14)}` : link}
              </code>
              <CopyButton text={link} labels={{ copy: l.copyLink, copied: l.copied }} onCopied={onCopy} />
            </p>
          ) : null}
          <p className="sqc-widget__hint">{formatLabel(l.hint, { network: network?.name ?? 'this network' })}</p>
        </>
      ) : null}
    </WidgetShell>
  );
}
