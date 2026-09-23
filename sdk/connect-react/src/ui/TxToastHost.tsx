/**
 * Optional tx toast host — consumes TxResultDto-like events (AppKit pattern).
 */
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import type { TxResultDto } from '../core/dto.js';

export type TxToast = TxResultDto & {
  id: string;
  title?: string;
};

type TxToastApi = {
  push: (toast: Omit<TxToast, 'id'> & { id?: string }) => string;
  dismiss: (id: string) => void;
  clear: () => void;
  toasts: TxToast[];
};

const TxToastContext = createContext<TxToastApi | null>(null);

let toastSeq = 0;

export type TxToastHostProps = {
  children?: ReactNode;
  /** Auto-dismiss success toasts after ms (0 = never). Default 8s. */
  autoDismissMs?: number;
  className?: string;
};

export function TxToastHost({
  children,
  autoDismissMs = 8_000,
  className,
}: TxToastHostProps) {
  const [toasts, setToasts] = useState<TxToast[]>([]);

  const dismiss = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (toast: Omit<TxToast, 'id'> & { id?: string }) => {
      const id = toast.id ?? `tx-${++toastSeq}`;
      const next: TxToast = { ...toast, id };
      setToasts((prev) => [...prev.filter((t) => t.id !== id), next]);
      if (autoDismissMs > 0 && (toast.status === 'success' || toast.status === 'error')) {
        window.setTimeout(() => dismiss(id), autoDismissMs);
      }
      return id;
    },
    [autoDismissMs, dismiss],
  );

  const clear = useCallback(() => setToasts([]), []);

  const api = useMemo(
    () => ({ push, dismiss, clear, toasts }),
    [push, dismiss, clear, toasts],
  );

  return (
    <TxToastContext.Provider value={api}>
      {children}
      <div className={['sqc-tx-toast-host', className].filter(Boolean).join(' ')} aria-live="polite">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={`sqc-tx-toast sqc-tx-toast--${t.status}`}
            role="status"
          >
            <div className="sqc-tx-toast__title">
              {t.title ??
                (t.status === 'pending'
                  ? 'Transaction pending…'
                  : t.status === 'success'
                    ? 'Transaction confirmed'
                    : t.status === 'error'
                      ? 'Transaction failed'
                      : 'Transaction')}
            </div>
            {t.hash ? (
              t.explorerUrl ? (
                <a className="sqc-tx-toast__link" href={t.explorerUrl} target="_blank" rel="noreferrer">
                  {t.hash.slice(0, 10)}…
                </a>
              ) : (
                <code className="sqc-tx-toast__hash">{t.hash.slice(0, 14)}…</code>
              )
            ) : null}
            {t.errorMessage ? <div className="sqc-tx-toast__err">{t.errorMessage}</div> : null}
            <button type="button" className="sqc-tx-toast__close" onClick={() => dismiss(t.id)} aria-label="Dismiss">
              ×
            </button>
          </div>
        ))}
      </div>
    </TxToastContext.Provider>
  );
}

export function useTxToast(): TxToastApi {
  const ctx = useContext(TxToastContext);
  if (!ctx) {
    if (typeof process !== 'undefined' && process.env?.NODE_ENV !== 'production') {
      console.warn(
        '@splendor/connect-react: useTxToast() called without <TxToastHost> — toasts are no-ops. Wrap your app in TxToastHost.',
      );
    }
    return {
      push: () => '',
      dismiss: () => {},
      clear: () => {},
      toasts: [],
    };
  }
  return ctx;
}
