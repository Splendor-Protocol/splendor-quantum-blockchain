import { useEffect, useId, useRef, useState } from 'react';
import type { NetworkId } from '@splendor/sqc-sdk';
import { useNetwork } from '../react/hooks.js';

export type NetworkBadgeProps = {
  className?: string;
  /** When true (default), click opens a network picker. */
  switchable?: boolean;
};

export function NetworkBadge({ className, switchable = true }: NetworkBadgeProps) {
  const { network, networks, switchNetwork, networkId, switching } = useNetwork();
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const listId = useId();

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false);
        return;
      }
      if (!networks.length) return;
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setActiveIndex((i) => (i + 1) % networks.length);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setActiveIndex((i) => (i - 1 + networks.length) % networks.length);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        const n = networks[activeIndex];
        if (n) void onPick(n.id as NetworkId);
      }
    };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, networks, activeIndex]);

  if (!network && !networkId) return null;

  const label = switching ? 'Switching…' : network?.name || String(networkId);

  const onPick = async (id: NetworkId) => {
    setOpen(false);
    if (id === networkId) return;
    await switchNetwork(id);
  };

  return (
    <div ref={rootRef} className="sqc-network-badge-wrap">
      <button
        type="button"
        className={['sqc-network-badge', className].filter(Boolean).join(' ')}
        aria-haspopup={switchable ? 'listbox' : undefined}
        aria-expanded={switchable ? open : undefined}
        aria-controls={switchable ? listId : undefined}
        disabled={!switchable || networks.length < 2 || switching}
        onClick={() => switchable && setOpen((v) => !v)}
      >
        <span className="sqc-network-badge__dot" aria-hidden />
        {label}
      </button>
      {open && networks.length > 0 ? (
        <ul id={listId} role="listbox" className="sqc-network-menu">
          {networks.map((n, i) => (
            <li key={n.id}>
              <button
                type="button"
                role="option"
                aria-selected={n.id === networkId}
                className={[
                  'sqc-connector-item',
                  i === activeIndex ? 'sqc-connector-item--active' : '',
                ]
                  .filter(Boolean)
                  .join(' ')}
                onMouseEnter={() => setActiveIndex(i)}
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
