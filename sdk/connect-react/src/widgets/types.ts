import type { CSSProperties, ReactNode } from 'react';

/** Lifecycle of the widget's main action (send, deploy, sign). */
export type WidgetStatus = 'idle' | 'pending' | 'success' | 'error';

/** Props every drop-in widget accepts. */
export type BaseWidgetProps<TResult, TLabels> = {
  className?: string;
  style?: CSSProperties;
  /** Replaces the default heading. */
  title?: ReactNode;
  hideTitle?: boolean;
  /** Text overrides for this widget; unset keys fall back to provider `labels.widgets`, then English defaults. */
  labels?: Partial<TLabels>;
  /** Called once the action succeeds. */
  onSuccess?: (result: TResult) => void;
  /** Called when the action fails, including a wallet rejection. */
  onError?: (error: Error) => void;
  /** Called on every status change after mount. */
  onStatusChange?: (status: WidgetStatus) => void;
  /** Push pending / success / error toasts when a `<TxToastHost>` is mounted. Default true. */
  toast?: boolean;
};
