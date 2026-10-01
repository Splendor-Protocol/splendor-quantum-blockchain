import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

const VERIFIED_KEY = 'sqc.lab.verified';

type Status = 'idle' | 'pending' | 'success' | 'error';

/** Guide section (slug of its "## N. Title" heading) for each lab card, keyed by card id. */
const CARD_GUIDE: Record<string, { slug: string; title: string }> = {
  helpers: { slug: 'drop-in-widgets', title: 'Drop-in widgets' },
  toasts: { slug: 'toasts-and-the-wrong-network-banner', title: 'Toasts and the wrong-network banner' },
  connect: { slug: 'connect-and-read-the-account', title: 'Connect and read the account' },
  network: { slug: 'connect-and-read-the-account', title: 'Connect and read the account' },
  account: { slug: 'send-spld', title: 'Send SPLD' },
  transfer: { slug: 'send-spld', title: 'Send SPLD' },
  confirm: { slug: 'send-spld', title: 'Send SPLD' },
  signin: { slug: 'sign-in-with-the-wallet', title: 'Sign-In with the wallet' },
  qrc20: { slug: 'qrc-20-tokens', title: 'QRC-20 tokens' },
  compose: { slug: 'build-your-own-widget', title: 'Build your own widget' },
  qrc1155: { slug: 'qrc-1155-collections', title: 'QRC-1155 collections' },
  contract: { slug: 'bridge-and-bytecode-contracts', title: 'Bridge and bytecode contracts' },
  modules: { slug: 'chain-modules', title: 'Chain modules' },
  bridge: { slug: 'bridge-and-bytecode-contracts', title: 'Bridge and bytecode contracts' },
};

type LabView = 'tests' | 'guide';

type LabContextValue = {
  verified: Record<string, boolean>;
  toggle: (id: string) => void;
  resetAll: () => void;
  register: (id: string) => void;
  ids: string[];
  view: LabView;
  /** Guide heading to scroll to when the guide opens; null opens it at the top. */
  guideSection: string | null;
  openGuide: (section?: string | null, fromCard?: string) => void;
  /** Back to the tests, scrolled to the card the guide was opened from. */
  openTests: () => void;
};

const LabContext = createContext<LabContextValue | null>(null);

function readVerified(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(VERIFIED_KEY);
    return raw ? (JSON.parse(raw) as Record<string, boolean>) : {};
  } catch {
    return {};
  }
}

export function LabProvider({ children }: { children: ReactNode }) {
  const [verified, setVerified] = useState<Record<string, boolean>>(readVerified);
  const [ids, setIds] = useState<string[]>([]);

  const persist = (next: Record<string, boolean>) => {
    try {
      localStorage.setItem(VERIFIED_KEY, JSON.stringify(next));
    } catch {
      /* ignore */
    }
    return next;
  };

  const toggle = useCallback((id: string) => {
    setVerified((prev) => persist({ ...prev, [id]: !prev[id] }));
  }, []);

  const resetAll = useCallback(() => setVerified(persist({})), []);

  const register = useCallback((id: string) => {
    setIds((prev) => (prev.includes(id) ? prev : [...prev, id]));
  }, []);

  const [view, setView] = useState<LabView>('tests');
  const [guideSection, setGuideSection] = useState<string | null>(null);
  const [returnTo, setReturnTo] = useState<string | null>(null);

  const openGuide = useCallback((section: string | null = null, fromCard?: string) => {
    setGuideSection(section);
    setReturnTo(fromCard ?? null);
    setView('guide');
  }, []);

  const openTests = useCallback(() => {
    setView('tests');
    requestAnimationFrame(() => {
      if (returnTo) document.getElementById(returnTo)?.scrollIntoView({ block: 'start' });
      else window.scrollTo({ top: 0 });
    });
  }, [returnTo]);

  const value = useMemo(
    () => ({ verified, toggle, resetAll, register, ids, view, guideSection, openGuide, openTests }),
    [verified, toggle, resetAll, register, ids, view, guideSection, openGuide, openTests],
  );
  return <LabContext.Provider value={value}>{children}</LabContext.Provider>;
}

export function LabViewTabs() {
  const { view, openGuide, openTests } = useLab();
  return (
    <div className="lab-tabs" role="tablist">
      <button
        type="button"
        role="tab"
        aria-selected={view === 'tests'}
        className={view === 'tests' ? 'is-active' : undefined}
        onClick={openTests}
      >
        Tests
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={view === 'guide'}
        className={view === 'guide' ? 'is-active' : undefined}
        onClick={() => openGuide(null)}
      >
        SDK guide
      </button>
    </div>
  );
}

export function useLab(): LabContextValue {
  const ctx = useContext(LabContext);
  if (!ctx) throw new Error('LabProvider missing');
  return ctx;
}

export function LabProgress() {
  const { verified, ids, resetAll } = useLab();
  const done = ids.filter((id) => verified[id]).length;
  return (
    <div className="lab-progress">
      <span>
        Verified <strong>{done}</strong> / {ids.length}
      </span>
      <div className="lab-progress__bar" aria-hidden>
        <div style={{ width: ids.length ? `${(done / ids.length) * 100}%` : 0 }} />
      </div>
      <button type="button" className="lab-link" onClick={resetAll}>
        Reset ticks
      </button>
    </div>
  );
}

export function Card({
  id,
  step,
  title,
  tag,
  checks,
  children,
}: {
  id: string;
  step: number;
  title: string;
  /** What the card needs, e.g. "No wallet", "Wallet", "Wallet + SPLD". */
  tag?: string;
  checks: string[];
  children: ReactNode;
}) {
  const { verified, toggle, register, openGuide } = useLab();
  useEffect(() => register(id), [id, register]);
  const ok = !!verified[id];
  const guide = CARD_GUIDE[id];
  return (
    <section id={id} className={['lab-card', ok ? 'lab-card--ok' : ''].join(' ')}>
      <header className="lab-card__head">
        <h2>
          <span className="lab-step">{step}</span>
          {title}
          {tag ? <span className="lab-tag">{tag}</span> : null}
        </h2>
        <label className="lab-verify">
          <input type="checkbox" checked={ok} onChange={() => toggle(id)} />
          Verified
        </label>
      </header>
      <details className="lab-checks" open={!ok}>
        <summary>What to check</summary>
        <ol>
          {checks.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ol>
      </details>
      {guide ? (
        <p className="lab-guide-link">
          Using this in your app?{' '}
          <button type="button" className="lab-link" onClick={() => openGuide(guide.slug, id)}>
            Read "{guide.title}" in the SDK guide
          </button>
        </p>
      ) : null}
      <div className="lab-card__body">{children}</div>
    </section>
  );
}

export function Field({
  label,
  value,
  onChange,
  placeholder,
  mono = true,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
}) {
  return (
    <label className="lab-field">
      <span>{label}</span>
      <input
        className={mono ? 'mono' : undefined}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        autoComplete="off"
        spellCheck={false}
      />
    </label>
  );
}

export function Btn({
  children,
  onClick,
  disabled,
  kind = 'primary',
}: {
  children: ReactNode;
  onClick: () => void;
  disabled?: boolean;
  kind?: 'primary' | 'ghost';
}) {
  return (
    <button
      type="button"
      className={`lab-btn lab-btn--${kind}`}
      onClick={onClick}
      disabled={disabled}
    >
      {children}
    </button>
  );
}

export function Row({ children }: { children: ReactNode }) {
  return <div className="lab-row">{children}</div>;
}

export function toJson(v: unknown): string {
  return JSON.stringify(
    v,
    (_k, val) => (typeof val === 'bigint' ? `${val.toString()}n` : val),
    2,
  );
}

/** Tracks one async action: status, data, error. Errors never escape to the console as unhandled. */
export function useAction<T = unknown>() {
  const [status, setStatus] = useState<Status>('idle');
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);

  const run = useCallback(async (fn: () => Promise<T> | T) => {
    setStatus('pending');
    setError(null);
    setData(null);
    try {
      const out = await fn();
      setData(out);
      setStatus('success');
      return out;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setStatus('error');
      return undefined;
    }
  }, []);

  return { status, data, error, run };
}

export function Result({
  status,
  data,
  error,
}: {
  status: Status;
  data?: unknown;
  error?: string | null;
}) {
  if (status === 'idle') return null;
  return (
    <div className={`lab-result lab-result--${status}`} role="status">
      <strong>
        {status === 'pending' ? 'Working…' : status === 'success' ? 'Done' : 'Failed'}
      </strong>
      {error ? <pre>{error}</pre> : null}
      {status === 'success' && data !== undefined && data !== null ? (
        <pre>{typeof data === 'string' ? data : toJson(data)}</pre>
      ) : null}
    </div>
  );
}

export function Hint({ children }: { children: ReactNode }) {
  return <p className="lab-hint">{children}</p>;
}
