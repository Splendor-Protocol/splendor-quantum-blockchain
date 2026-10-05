import type { CSSProperties, ReactNode } from 'react';
import { useAccount, useNetwork } from '../../react/hooks.js';
import { COMMON_LABELS, useWidgetLabels } from '../labels.js';
import { WalletGate, type WalletGateProps } from './WalletGate.js';
import type { WidgetStatus } from '../types.js';

export type WidgetShellProps = {
  /** Value of `data-sqc-widget` on the root, e.g. "transfer". */
  kind: string;
  status?: WidgetStatus;
  className?: string;
  style?: CSSProperties;
  title?: ReactNode;
  hideTitle?: boolean;
  /** Rendered next to the title, only while connected. */
  headerExtra?: ReactNode;
  /** Wrap `children` in a WalletGate. Default true. */
  gate?: boolean;
  gateLabels?: WalletGateProps['labels'] & { wrongNetwork?: string };
  children: ReactNode;
};

/**
 * Common widget frame: `sqc-widget` root with data attributes, heading, and the wallet gate.
 * While the wallet is on another chain than the app, every control inside is disabled.
 */
export function WidgetShell({
  kind,
  status = 'idle',
  className,
  style,
  title,
  hideTitle,
  headerExtra,
  gate = true,
  gateLabels,
  children,
}: WidgetShellProps) {
  const { isConnected } = useAccount();
  const { walletMismatch } = useNetwork();
  const l = useWidgetLabels(COMMON_LABELS, gateLabels);
  const heading = !hideTitle && title != null ? <h3 className="sqc-widget__title">{title}</h3> : null;
  const extra = headerExtra && isConnected ? headerExtra : null;
  const body = walletMismatch ? (
    <>
      <p className="sqc-widget__msg sqc-widget__msg--error" role="alert" data-testid="sqc-wrong-network">
        {l.wrongNetwork}
      </p>
      <fieldset className="sqc-widget__fieldset" disabled aria-disabled="true">
        {children}
      </fieldset>
    </>
  ) : (
    children
  );
  return (
    <div
      className={['sqc-widget', `sqc-widget--${kind}`, className].filter(Boolean).join(' ')}
      style={style}
      data-sqc-widget={kind}
      data-status={status}
    >
      {extra ? (
        <div className="sqc-widget__header">
          {heading}
          {extra}
        </div>
      ) : (
        heading
      )}
      {gate ? <WalletGate labels={gateLabels}>{body}</WalletGate> : body}
    </div>
  );
}
