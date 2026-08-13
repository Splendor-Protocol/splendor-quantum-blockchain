import { useEffect, useId, useRef, useState } from 'react';
import type { NetworkId } from '@splendor/pq-bridge';
import { useNetwork } from '../react/hooks.js';

export type NetworkBadgeProps = {
  className?: string;
  /** When true (default), click opens a network picker. */
  switchable?: boolean;
};

export function NetworkBadge({ className, switchable = true }: NetworkBadgeProps) {
  const { network, networks, switchNetwork, networkId } = useNetwork();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const listId = useId();

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [open]);

  if (!network && !networkId) return null;

  const label = network?.name || String(networkId);

  const onPick = async (id: NetworkId) => {
    setOpen(false);
    if (id === networkId) return;
    await switchNetwork(id);
  };

  return (
    <div ref={rootRef} style={{ position: 'relative', display: 'inline-block' }}>
      <button
        type="button"
        className={['sqc-network-badge', className].filter(Boolean).join(' ')}
        aria-haspopup={switchable ? 'listbox' : undefined}
        aria-expanded={switchable ? open : undefined}
        aria-controls={switchable ? listId : undefined}
        disabled={!switchable || networks.length < 2}
        onClick={() => switchable && setOpen((v) => !v)}
        style={{ cursor: switchable && networks.length > 1 ? 'pointer' : 'default' }}
      >
        <span className="sqc-network-badge__dot" aria-hidden />
        {label}
      </button>
      {open && networks.length > 0 ? (
        <ul
          id={listId}
          role="listbox"
          className="sqc-connector-list"
          style={{
            position: 'absolute',
            top: 'calc(100% + 6px)',
            left: 0,
            minWidth: 200,
            zIndex: 20,
            background: 'var(--sqc-bg-elevated)',
            border: '1px solid var(--sqc-border)',
            borderRadius: 'var(--sqc-radius-sm)',
            padding: '0.35rem',
            margin: 0,
          }}
        >
          {networks.map((n) => (
            <li key={n.id}>
              <button
                type="button"
                role="option"
                aria-selected={n.id === networkId}
                className="sqc-connector-item"
                style={{ padding: '0.55rem 0.75rem' }}
                onClick={() => void onPick(n.id as NetworkId)}
              >
                {n.name || n.id}
                {n.id === networkId ? (
                  <span className="sqc-connector-item__hint">Current</span>
                ) : null}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
