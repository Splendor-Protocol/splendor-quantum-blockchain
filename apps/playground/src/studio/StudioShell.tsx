import type { ReactNode } from 'react';

export type StudioRail = 'pages' | 'components' | 'theme' | 'contracts' | 'flows';
export type DeviceSize = 'desktop' | 'tablet' | 'mobile';

const RAIL: { id: StudioRail; label: string; icon: string }[] = [
  { id: 'pages', label: 'Pages', icon: 'P' },
  { id: 'components', label: 'Components', icon: 'C' },
  { id: 'theme', label: 'Theme', icon: 'T' },
  { id: 'contracts', label: 'Contracts', icon: 'K' },
  { id: 'flows', label: 'Flows', icon: 'F' },
];

export function StudioShell({
  topBar,
  rail,
  onRailChange,
  sidebar,
  canvas,
  inspector,
  overlay,
}: {
  topBar: ReactNode;
  rail: StudioRail;
  onRailChange: (rail: StudioRail) => void;
  sidebar: ReactNode;
  canvas: ReactNode;
  inspector: ReactNode;
  overlay?: ReactNode;
}) {
  return (
    <div className="pg-studio pg-studio-shell">
      {topBar}
      <div className="pg-studio-body pg-studio-shell-body">
        <nav className="pg-rail-icons" aria-label="Studio panels">
          {RAIL.map((item) => (
            <button
              key={item.id}
              type="button"
              className={`pg-rail-icon${rail === item.id ? ' is-active' : ''}`}
              aria-label={item.label}
              aria-current={rail === item.id ? 'true' : undefined}
              title={item.label}
              onClick={() => onRailChange(item.id)}
            >
              <span aria-hidden="true">{item.icon}</span>
            </button>
          ))}
        </nav>
        <aside className="pg-toybox pg-studio-sidebar" aria-label={RAIL.find((r) => r.id === rail)?.label}>
          {sidebar}
        </aside>
        <section className="pg-stage pg-studio-canvas" aria-label="Your page">
          {canvas}
        </section>
        <aside className="pg-inspector pg-studio-inspector" aria-label="Inspector">
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
