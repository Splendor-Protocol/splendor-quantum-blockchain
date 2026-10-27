import { useEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent, type ReactNode } from 'react';

export type StudioRail = 'pages' | 'components' | 'theme' | 'contracts' | 'flows';
export type DeviceSize = 'desktop' | 'tablet' | 'mobile';
/** Below the narrow breakpoint the side panel and the inspector are drawers over the canvas. */
export type StudioDrawer = 'side' | 'inspector' | null;

/** Same width as the `max-width: 1100px` rules in styles.css. */
export const NARROW_STUDIO = '(max-width: 1100px)';

export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      if (typeof window.matchMedia !== 'function') return () => undefined;
      const m = window.matchMedia(query);
      m.addEventListener('change', onChange);
      return () => m.removeEventListener('change', onChange);
    },
    () => typeof window.matchMedia === 'function' && window.matchMedia(query).matches,
    () => false,
  );
}

const stroke = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.7, strokeLinecap: 'round', strokeLinejoin: 'round' } as const;

const RAIL: { id: StudioRail; label: string; icon: ReactNode }[] = [
  {
    id: 'pages',
    label: 'Pages',
    icon: (
      <>
        <path d="M6 3.5h8l4 4V20a.5.5 0 0 1-.5.5h-11.5a.5.5 0 0 1-.5-.5V4a.5.5 0 0 1 .5-.5Z" {...stroke} />
        <path d="M14 3.5V8h4M8.5 12.5h7M8.5 16h5" {...stroke} />
      </>
    ),
  },
  {
    id: 'components',
    label: 'Components',
    icon: (
      <>
        <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" {...stroke} />
        <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" {...stroke} />
        <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" {...stroke} />
        <path d="M17 13.5v7M13.5 17h7" {...stroke} />
      </>
    ),
  },
  {
    id: 'theme',
    label: 'Theme',
    icon: (
      <>
        <path d="M12 3.5a8.5 8.5 0 1 0 0 17c1.2 0 1.8-.8 1.8-1.7 0-1.2-1-1.6-1-2.6 0-.9.7-1.6 1.6-1.6h2.1a3.9 3.9 0 0 0 3.9-3.9C20.4 6.9 16.6 3.5 12 3.5Z" {...stroke} />
        <circle cx="7.6" cy="11" r="1.2" fill="currentColor" />
        <circle cx="10.5" cy="7.2" r="1.2" fill="currentColor" />
        <circle cx="15" cy="7.6" r="1.2" fill="currentColor" />
      </>
    ),
  },
  {
    id: 'contracts',
    label: 'Contracts',
    icon: (
      <>
        <path d="M8.5 7 4 12l4.5 5M15.5 7 20 12l-4.5 5M13.5 5l-3 14" {...stroke} />
      </>
    ),
  },
  {
    id: 'flows',
    label: 'Flows',
    icon: (
      <>
        <rect x="3" y="4" width="6.5" height="5" rx="1.2" {...stroke} />
        <rect x="14.5" y="4" width="6.5" height="5" rx="1.2" {...stroke} />
        <rect x="8.75" y="15" width="6.5" height="5" rx="1.2" {...stroke} />
        <path d="M6.25 9v2.5a1 1 0 0 0 1 1H12m5.75-3.5v2.5a1 1 0 0 1-1 1H12m0 0V15" {...stroke} />
      </>
    ),
  },
];

const tabId = (id: StudioRail) => `studio-rail-${id}`;
const PANEL_ID = 'studio-rail-panel';
const DOCK_ID = 'studio-layers-dock';

export function StudioShell({
  topBar,
  rail,
  onRailChange,
  sidebar,
  canvas,
  inspector,
  overlay,
  dock,
  dockTitle = 'Layers',
  drawer: drawerProp,
  onDrawerChange,
}: {
  topBar: ReactNode;
  rail: StudioRail;
  onRailChange: (rail: StudioRail) => void;
  sidebar: ReactNode;
  canvas: ReactNode;
  inspector: ReactNode;
  overlay?: ReactNode;
  /** A collapsible panel under the rail panel that stays put while the rail changes (the page's layers). */
  dock?: ReactNode;
  dockTitle?: string;
  /** Open drawer on a narrow screen (controlled); the shell keeps its own when not given. */
  drawer?: StudioDrawer;
  onDrawerChange?: (drawer: StudioDrawer) => void;
}) {
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);
  const [dockOpen, setDockOpen] = useState(true);
  const narrow = useMediaQuery(NARROW_STUDIO);
  const [ownDrawer, setOwnDrawer] = useState<StudioDrawer>(null);
  const drawer = narrow ? (drawerProp !== undefined ? drawerProp : ownDrawer) : null;
  const setDrawer = (next: StudioDrawer) => {
    setOwnDrawer(next);
    onDrawerChange?.(next);
  };
  const setDrawerRef = useRef(setDrawer);
  setDrawerRef.current = setDrawer;
  useEffect(() => {
    if (!drawer) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      // A dialog over the drawer handles its own Escape.
      if (e.key !== 'Escape' || e.defaultPrevented || document.querySelector('[aria-modal="true"]')) return;
      setDrawerRef.current(null);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [drawer]);
  const pickRail = (id: StudioRail) => {
    onRailChange(id);
    if (narrow) setDrawer(drawer === 'side' && rail === id ? null : 'side');
  };

  // ARIA tabs pattern (vertical): arrows move and select, Home/End jump to the ends.
  const onKeyDown = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const last = RAIL.length - 1;
    const next =
      e.key === 'ArrowDown' || e.key === 'ArrowRight'
        ? (index + 1) % RAIL.length
        : e.key === 'ArrowUp' || e.key === 'ArrowLeft'
          ? (index - 1 + RAIL.length) % RAIL.length
          : e.key === 'Home'
            ? 0
            : e.key === 'End'
              ? last
              : -1;
    if (next < 0) return;
    e.preventDefault();
    onRailChange(RAIL[next]!.id);
    tabs.current[next]?.focus();
  };

  const closeDrawer = (
    <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm pg-drawer-close" onClick={() => setDrawer(null)}>
      Close
    </button>
  );

  return (
    <div className={`pg-studio pg-studio-shell${narrow ? ' is-narrow' : ''}`}>
      {topBar}
      <div className="pg-studio-body pg-studio-shell-body">
        {drawer ? <div className="pg-drawer-backdrop" aria-hidden="true" onClick={() => setDrawer(null)} /> : null}
        <div className="pg-rail-icons" role="tablist" aria-label="Studio panels" aria-orientation="vertical">
          {RAIL.map((item, i) => {
            const active = rail === item.id;
            return (
              <button
                key={item.id}
                ref={(el) => {
                  tabs.current[i] = el;
                }}
                id={tabId(item.id)}
                type="button"
                role="tab"
                className={`pg-rail-icon${active ? ' is-active' : ''}`}
                aria-label={item.label}
                aria-selected={active}
                aria-controls={PANEL_ID}
                tabIndex={active ? 0 : -1}
                data-tip={item.label}
                onClick={() => pickRail(item.id)}
                onKeyDown={(e) => onKeyDown(e, i)}
              >
                <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
                  {item.icon}
                </svg>
              </button>
            );
          })}
        </div>
        {narrow ? (
          <div className="pg-drawer-toggles">
            <button
              type="button"
              className="pg-btn pg-btn-ghost pg-btn-sm"
              aria-expanded={drawer === 'inspector'}
              data-testid="drawer-inspector"
              onClick={() => setDrawer(drawer === 'inspector' ? null : 'inspector')}
            >
              Edit selection
            </button>
          </div>
        ) : null}
        <div className={`pg-studio-side${narrow ? ' is-drawer' : ''}`} hidden={narrow && drawer !== 'side'} data-testid="studio-side">
          {narrow ? closeDrawer : null}
          <aside id={PANEL_ID} className="pg-toybox pg-studio-sidebar" role="tabpanel" aria-labelledby={tabId(rail)}>
            {sidebar}
          </aside>
          {dock ? (
            <section className={`pg-layers-dock${dockOpen ? ' is-open' : ''}`} aria-label="Layers">
              <button
                type="button"
                className="pg-layers-toggle"
                aria-expanded={dockOpen}
                aria-controls={DOCK_ID}
                onClick={() => setDockOpen((open) => !open)}
              >
                <span className="pg-layers-caret" aria-hidden="true">
                  {dockOpen ? '▾' : '▸'}
                </span>
                {dockTitle}
              </button>
              <div id={DOCK_ID} className="pg-layers-body" hidden={!dockOpen}>
                {dock}
              </div>
            </section>
          ) : null}
        </div>
        <section className="pg-stage pg-studio-canvas" aria-label="Your page">
          {canvas}
        </section>
        <aside
          className={`pg-inspector pg-studio-inspector${narrow ? ' is-drawer' : ''}`}
          aria-label="Inspector"
          hidden={narrow && drawer !== 'inspector'}
          data-testid="studio-inspector"
        >
          {narrow ? closeDrawer : null}
          {inspector}
        </aside>
      </div>
      {overlay}
    </div>
  );
}

export function StudioTopBar({ children }: { children: ReactNode }) {
  return <header className="pg-studio-bar">{children}</header>;
}

export function DeviceSwitch({
  value,
  onChange,
}: {
  value: DeviceSize;
  onChange: (size: DeviceSize) => void;
}) {
  return (
    <div className="pg-segmented" role="group" aria-label="Preview device">
      {(['desktop', 'tablet', 'mobile'] as const).map((d) => (
        <button key={d} type="button" aria-pressed={value === d} onClick={() => onChange(d)}>
          {d === 'desktop' ? 'Desktop' : d === 'tablet' ? 'Tablet' : 'Mobile'}
        </button>
      ))}
    </div>
  );
}
