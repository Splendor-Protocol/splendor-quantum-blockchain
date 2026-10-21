import { Component, type ReactNode } from 'react';
import { themeOverridesToStyle } from '@splendor/connect-react';
import { blocksFromTree, isContainer, styleToCss, type CatalogEntry, type DappBlock, type DappSpec, type LayoutNode } from '@splendor/dapp-spec';
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

function renderBlockBody(block: DappBlock, entry: CatalogEntry | undefined): ReactNode {
  const C = entry ? resolveComponent(entry) : null;
  if (C) {
    return (
      <BlockBoundary label={entry!.title}>
        <C {...block.props} />
      </BlockBoundary>
    );
  }
  return <div className="pg-block-error">Unknown widget “{block.widget}”</div>;
}

function wrapSelectable(
  blockId: string,
  body: ReactNode,
  selectedBlockId: string | null | undefined,
  onSelectBlock: ((id: string) => void) | undefined,
) {
  if (!onSelectBlock) return body;
  return (
    <div
      className={`pg-preview-block${selectedBlockId === blockId ? ' is-selected' : ''}`}
      onClickCapture={(e) => {
        // Plain clicks select but still reach the widget (the mock wallet stays usable);
        // Alt-click also swallows the click so the widget stays inert.
        if (e.altKey) {
          e.preventDefault();
          e.stopPropagation();
        }
        onSelectBlock(blockId);
      }}
    >
      {body}
    </div>
  );
}

/**
 * Renders a validated DappSpec with the real widgets and blocks. Shared by the builder preview
 * (mock and live), the widget gallery, share links and — in phase 2 — hosted builds.
 */
export function SpecRenderer({ spec, catalog, pagePath, onNavigate, headerExtra, selectedBlockId, onSelectBlock }: SpecRendererProps) {
  const byId = new Map(catalog.map((c) => [c.id, c]));
  const page = spec.pages.find((p) => p.path === pagePath) ?? spec.pages[0];
  /** Builder mode: the caller wants selection + drop targets (the preview frame). */
  const builder = Boolean(onSelectBlock);

  const renderTreeNode = (node: LayoutNode): ReactNode => {
    if (node.kind === 'block') {
      const block = node.block;
      const entry = byId.get(block.widget);
      const body = renderBlockBody(block, entry);
      return (
        <div key={block.id} data-block-id={block.id}>
          {wrapSelectable(block.id, body, selectedBlockId, onSelectBlock)}
        </div>
      );
    }
    const hide = node.style?.hideOn ? ` dapp-hide-${node.style.hideOn}` : '';
    const selected = builder && selectedBlockId === node.id;
    return (
      <div
        key={node.id}
        className={`dapp-${node.kind}${hide}${selected ? ' pg-node-selected' : ''}`}
        data-node-id={node.id}
        data-node-kind={node.kind}
        style={styleToCss(node.style)}
        onClick={
          onSelectBlock
            ? (e) => {
                // A click on the container's own space (not on a child block/container) selects it.
                if ((e.target as Element).closest('[data-block-id],[data-node-id]') === e.currentTarget) onSelectBlock(node.id);
              }
            : undefined
        }
      >
        {node.children.map((child) => renderTreeNode(child))}
        {builder && node.children.length === 0 ? (
          <div className="pg-empty-drop" data-drop-placeholder="">
            Drop a piece here
          </div>
        ) : null}
      </div>
    );
  };

  const flatBlocks = page ? (page.blocks ?? blocksFromTree(page.root)) : [];
  const mainBody =
    page?.root && isContainer(page.root) ? (
      renderTreeNode(page.root)
    ) : (
      <>
        {flatBlocks.map((block) => {
          const entry = byId.get(block.widget);
          const body = renderBlockBody(block, entry);
          return (
            <div key={block.id} data-block-id={block.id}>
              {wrapSelectable(block.id, body, selectedBlockId, onSelectBlock)}
            </div>
          );
        })}
        {page && flatBlocks.length === 0 ? <p className="dapp-empty">This page has no blocks yet.</p> : null}
      </>
    );

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
        <main className="dapp-grid">{mainBody}</main>
      </div>
    </div>
  );
}
