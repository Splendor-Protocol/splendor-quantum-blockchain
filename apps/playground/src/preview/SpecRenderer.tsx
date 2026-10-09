import { Component, type ReactNode } from 'react';
import { themeOverridesToStyle } from '@splendor/connect-react';
import type { CatalogEntry, DappSpec } from '@splendor/dapp-spec';
import { resolveComponent } from './componentMap';

class BlockBoundary extends Component<{ label: string; children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  render() {
    if (this.state.error) {
      return (
        <div className="pg-block-error" role="alert">
          <strong>{this.props.label}</strong> failed to render: {this.state.error.message}
        </div>
      );
    }
    return this.props.children;
  }
}

export type SpecRendererProps = {
  spec: DappSpec;
  catalog: readonly CatalogEntry[];
  pagePath: string;
  onNavigate?: (path: string) => void;
  /** Rendered in the header (e.g. ConnectKit in live mode). */
  headerExtra?: ReactNode;
  /** Builder selection: outline a block and report clicks. */
  selectedBlockId?: string | null;
  onSelectBlock?: (id: string) => void;
};

/**
 * Renders a validated DappSpec with the real widgets and blocks. Shared by the builder preview
 * (mock and live), the widget gallery, share links and — in phase 2 — hosted builds.
 */
export function SpecRenderer({ spec, catalog, pagePath, onNavigate, headerExtra, selectedBlockId, onSelectBlock }: SpecRendererProps) {
  const byId = new Map(catalog.map((c) => [c.id, c]));
  const page = spec.pages.find((p) => p.path === pagePath) ?? spec.pages[0];
  return (
    <div data-sqc-theme={spec.theme.base} className="sqc-root dapp-root" style={themeOverridesToStyle(spec.theme.overrides)}>
      <div className="dapp-shell">
        <header className="dapp-header">
          <p className="dapp-brand">{spec.name}</p>
          {headerExtra}
        </header>
        {spec.pages.length > 1 ? (
          <nav className="dapp-nav" aria-label="dApp pages">
            {spec.pages.map((p) => (
              <a
                key={p.path}
                href={`#${p.path}`}
                aria-current={p === page ? 'page' : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  onNavigate?.(p.path);
                }}
              >
                {p.title}
              </a>
            ))}
          </nav>
        ) : null}
        <main className="dapp-grid">
          {page?.blocks.map((block) => {
            const entry = byId.get(block.widget);
            const C = entry ? resolveComponent(entry) : null;
            const body = C ? (
              <BlockBoundary label={entry!.title}>
                <C {...block.props} />
              </BlockBoundary>
            ) : (
              <div className="pg-block-error">Unknown widget “{block.widget}”</div>
            );
            if (!onSelectBlock) return <div key={block.id}>{body}</div>;
            return (
              <div
                key={block.id}
                className={`pg-preview-block${selectedBlockId === block.id ? ' is-selected' : ''}`}
                data-block-id={block.id}
                onClickCapture={(e) => {
                  if (e.altKey) {
                    e.preventDefault();
                    e.stopPropagation();
                    onSelectBlock(block.id);
                  }
                }}
              >
                {body}
              </div>
            );
          })}
          {page && page.blocks.length === 0 ? <p className="dapp-empty">This page has no blocks yet.</p> : null}
        </main>
      </div>
    </div>
  );
}
