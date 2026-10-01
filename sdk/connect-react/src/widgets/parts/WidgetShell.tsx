import type { CSSProperties, ReactNode } from 'react';
import { useAccount } from '../../react/hooks.js';
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
  gateLabels?: WalletGateProps['labels'];
  children: ReactNode;
};

/** Common widget frame: `sqc-widget` root with data attributes, heading, and the wallet gate. */
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
  const heading = !hideTitle && title != null ? <h3 className="sqc-widget__title">{title}</h3> : null;
  const extra = headerExtra && isConnected ? headerExtra : null;
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
      {gate ? <WalletGate labels={gateLabels}>{children}</WalletGate> : children}
    </div>
  );
}
