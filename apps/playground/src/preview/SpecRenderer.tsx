import { Component, type MouseEvent, type ReactNode } from 'react';
import { themeOverridesToStyle } from '@splendor/connect-react';
import { bindBlock, blocksFromTree, isContainer, styleToCss, type CatalogEntry, type DappBlock, type DappSpec, type LayoutNode, type NodeStyle } from '@splendor/dapp-spec';
import { AccessGate } from '@splendor/dapp-spec/blocks/AccessGate.tsx';
import { resolveComponent } from './componentMap';

/** Shows a failed widget instead of crashing the page, and tries again when `resetKey` (its props) changes. */
class BlockBoundary extends Component<{ label: string; resetKey: string; children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidUpdate(prev: { resetKey: string }) {
    if (this.state.error && prev.resetKey !== this.props.resetKey) this.setState({ error: null });
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

function renderBlockBody(block: DappBlock, entry: CatalogEntry | undefined, spec: DappSpec): ReactNode {
  const C = entry ? resolveComponent(entry) : null;
  if (C) {
    const { props } = bindBlock(spec, block, entry);
    return (
      <BlockBoundary label={entry!.title} resetKey={`${block.widget}:${JSON.stringify(props)}`}>
        <C {...props} />
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

const hideClass = (style: NodeStyle | undefined) => (style?.hideOn ? ` dapp-hide-${style.hideOn}` : '');

/** Page paths are `/` or `/segment`; strips `#`, query, hash and a trailing slash. */
function hrefToPath(href: string): string | null {
  if (href.startsWith('//')) return null;
  const isHash = href.startsWith('#/');
  if (!isHash && !href.startsWith('/')) return null;
  const path = (isHash ? href.slice(1) : href).split(/[?#]/)[0]!;
  return path.length > 1 ? path.replace(/\/+$/, '') || '/' : path;
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

  /** Leaf blocks carry their own style and hide-on-device class, like containers. */
  const renderLeaf = (block: DappBlock): ReactNode => {
    const entry = byId.get(block.widget);
    const body = renderBlockBody(block, entry, spec);
    return (
      <div key={block.id} className={`dapp-block${hideClass(block.style)}`} data-block-id={block.id} style={styleToCss(block.style)}>
        {wrapSelectable(block.id, body, selectedBlockId, onSelectBlock)}
      </div>
    );
  };

  const renderTreeNode = (node: LayoutNode): ReactNode => {
    if (node.kind === 'block') return renderLeaf(node.block);
    const selected = builder && selectedBlockId === node.id;
    return (
      <div
        key={node.id}
        className={`dapp-${node.kind}${hideClass(node.style)}${selected ? ' pg-node-selected' : ''}`}
        data-node-id={node.id}
        data-node-kind={node.kind}
        style={styleToCss(node.style, node.kind)}
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
  const treeMode = Boolean(page?.root && isContainer(page.root));
  const mainBody = treeMode ? (
    renderTreeNode(page!.root)
  ) : (
    <>
      {flatBlocks.map((block) => renderLeaf(block))}
      {page && flatBlocks.length === 0 ? <p className="dapp-empty">This page has no blocks yet.</p> : null}
    </>
  );

  /**
   * Links to other pages (`#/path`, or plain `/path` when it names one of this dApp's pages) switch
   * the page here instead of navigating the frame away.
   */
  const onRootClick = (e: MouseEvent<HTMLDivElement>) => {
    if (!onNavigate || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const anchor = (e.target as Element).closest?.('a');
    if (!anchor || !e.currentTarget.contains(anchor)) return;
    const target = anchor.getAttribute('target');
    if (target && target !== '_self') return;
    const href = anchor.getAttribute('href');
    if (!href) return;
    const path = hrefToPath(href);
    if (path === null) return;
    const known = spec.pages.some((p) => p.path === path);
    if (known) {
      e.preventDefault();
      onNavigate(path);
    } else if (href.startsWith('#/')) {
      // A hash link to a missing page: do nothing rather than change the frame's URL.
      e.preventDefault();
    }
  };

  /**
   * Protected pages: visitors (preview app, share links, live) meet the connect / sign-in gate.
   * While editing, the content stays visible with a note, so the page can still be built.
   */
  const access = page?.access ?? 'public';
  const gate = (body: ReactNode): ReactNode => {
    if (access === 'public') return body;
    if (builder) {
      return (
        <>
          <p className="pg-access-note" role="note">
            {access === 'wallet' ? 'Visitors must connect a wallet to see this page.' : 'Visitors must sign in with their wallet to see this page.'}
          </p>
          {body}
        </>
      );
    }
    return (
      <AccessGate access={access} title={page?.title}>
        {body}
      </AccessGate>
    );
  };

  // Same header, link order and footer as the exported app (`navModel` in dapp-spec codegen).
  const nav = spec.nav ?? {};
  const brand = nav.brand?.trim() || spec.name;
  const navPages = nav.links
    ? nav.links
        .map((path) => spec.pages.find((p) => p.path === path))
        .filter((p, i, all): p is DappSpec['pages'][number] => !!p && all.indexOf(p) === i)
    : spec.pages;

  return (
    <div data-sqc-theme={spec.theme.base} className="sqc-root dapp-root" style={themeOverridesToStyle(spec.theme.overrides)} onClick={onRootClick}>
      <div className="dapp-shell">
        <header className="dapp-header">
          <span className="dapp-brand">{brand}</span>
          {nav.showConnect !== false ? headerExtra : null}
        </header>
        {navPages.length > 1 ? (
          <nav className="dapp-nav" aria-label="dApp pages">
            {navPages.map((p) => (
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
        {treeMode ? (
          <main>
            <div className="dapp-page">{gate(mainBody)}</div>
          </main>
        ) : (
          <main className="dapp-grid">{gate(mainBody)}</main>
        )}
        {nav.footer?.trim() ? <footer className="dapp-footer">{nav.footer}</footer> : null}
      </div>
    </div>
  );
}
