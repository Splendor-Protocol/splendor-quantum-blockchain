import { PAGE_TEMPLATES, type DappPage, type DappSpec, type PageAccess } from '@splendor/dapp-spec';
import { useEffect, useState, type Dispatch } from 'react';
import { MAX_PAGES, type BuilderAction } from '../builder/state';
import { collectLinks } from './FlowsPanel';
import { useConfirm } from '../components/ConfirmDialog';

/** Page title field that never commits an empty title: an empty field reverts on blur. */
export function PageTitleInput({ title, onRename, id, ariaLabel, className }: { title: string; onRename: (title: string) => void; id?: string; ariaLabel?: string; className?: string }) {
  const [draft, setDraft] = useState(title);
  useEffect(() => setDraft(title), [title]);
  const empty = !draft.trim();
  return (
    <input
      id={id}
      aria-label={ariaLabel}
      className={className}
      value={draft}
      maxLength={60}
      aria-invalid={empty ? true : undefined}
      onChange={(e) => {
        setDraft(e.target.value);
        if (e.target.value.trim()) onRename(e.target.value);
      }}
      onBlur={() => {
        if (empty) setDraft(title);
      }}
    />
  );
}

const ACCESS_LABEL: Record<PageAccess, string> = { public: 'Public', wallet: 'Wallet connected', signin: 'Signed in' };

/** True when `spec.nav.links` (an explicit list of shown pages) includes the page, or there is no list. */
export function isInNav(spec: Pick<DappSpec, 'nav'>, path: string): boolean {
  return !spec.nav?.links || spec.nav.links.includes(path);
}

/** Who sees a page, whether it is in the navigation, and where it sits in the page order. */
export function PageSettings({
  page,
  index,
  count,
  inNav,
  idPrefix = 'page',
  onAccess,
  onNavVisible,
  onMove,
}: {
  page: Pick<DappPage, 'path' | 'title' | 'access'>;
  index: number;
  count: number;
  inNav: boolean;
  idPrefix?: string;
  onAccess: (access: PageAccess) => void;
  onNavVisible: (visible: boolean) => void;
  onMove: (delta: -1 | 1) => void;
}) {
  const home = page.path === '/';
  const id = `${idPrefix}-access`;
  return (
    <div className="pg-page-settings" data-testid="page-settings">
      <div className="pg-field">
        <label htmlFor={id}>Who can see this page</label>
        <select id={id} value={home ? 'public' : (page.access ?? 'public')} disabled={home} aria-describedby={home ? `${id}-hint` : undefined} onChange={(e) => onAccess(e.target.value as PageAccess)}>
          {(Object.keys(ACCESS_LABEL) as PageAccess[]).map((a) => (
            <option key={a} value={a}>
              {ACCESS_LABEL[a]}
            </option>
          ))}
        </select>
        {home ? (
          <small id={`${id}-hint`} className="pg-muted">
            The home page is always public
          </small>
        ) : null}
      </div>
      <div className="pg-field pg-field-inline">
        <input id={`${idPrefix}-nav`} type="checkbox" checked={inNav} onChange={(e) => onNavVisible(e.target.checked)} />
        <label htmlFor={`${idPrefix}-nav`}>Show in navigation</label>
      </div>
      <div className="pg-row">
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={index <= 0} aria-label={`Move page ${page.title} earlier`} onClick={() => onMove(-1)}>
          Earlier
        </button>
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={index < 0 || index >= count - 1} aria-label={`Move page ${page.title} later`} onClick={() => onMove(1)}>
          Later
        </button>
      </div>
    </div>
  );
}

export function PagesPanel({
  spec,
  pagePath,
  dispatch,
  newPageTitle,
  setNewPageTitle,
}: {
  spec: DappSpec;
  pagePath: string;
  dispatch: Dispatch<BuilderAction>;
  newPageTitle: string | null;
  setNewPageTitle: (title: string | null) => void;
}) {
  const [confirmDialog, confirm] = useConfirm();
  const removePage = async (p: DappPage) => {
    const links = collectLinks(spec).filter((l) => l.to === p.path && l.fromPath !== p.path).length;
    const warning = links ? `${links} link${links === 1 ? '' : 's'} to it will break.` : 'Any links to it will break.';
    if (await confirm({ title: `Delete page "${p.title}" (${p.path})?`, message: warning, confirmLabel: 'Delete page', danger: true })) {
      dispatch({ type: 'removePage', path: p.path });
    }
  };
  return (
    <div className="pg-stack">
      {confirmDialog}
      <p className="pg-toybox-lead">Pages</p>
      <ul className="pg-pages-list">
        {spec.pages.map((p, pageIndex) => (
          <li key={p.path} className={p.path === pagePath ? 'is-active' : ''}>
            <button
              type="button"
              className="pg-link-btn pg-pages-item"
              aria-current={p.path === pagePath ? 'true' : undefined}
              onClick={() => dispatch({ type: 'selectPage', path: p.path })}
            >
              <strong>{p.title}</strong>
              <small className="pg-muted">{p.path}</small>
            </button>
            {p.path === pagePath ? (
              <PageTitleInput ariaLabel="Page title" className="pg-pages-rename" title={p.title} onRename={(title) => dispatch({ type: 'renamePage', path: p.path, title })} />
            ) : null}
            <div className="pg-row pg-pages-actions">
            <button
              type="button"
              className="pg-icon-btn"
              aria-label={`Duplicate page ${p.title}`}
              title="Duplicate page"
              data-testid={`page-duplicate-${p.path}`}
              disabled={spec.pages.length >= MAX_PAGES}
              onClick={() => dispatch({ type: 'duplicatePage', path: p.path })}
            >
              {'\u29C9'}
            </button>
            {p.path !== '/' ? (
              <button
                type="button"
                className="pg-icon-btn"
                aria-label={`Set ${p.title} as home page`}
                title="Make this the home page (moves it to /)"
                data-testid={`page-home-${p.path}`}
                onClick={() => dispatch({ type: 'setHomePage', path: p.path })}
              >
                {'\u2302'}
              </button>
            ) : (
              <small className="pg-muted" title="This page is served at /" data-testid="page-is-home">
                Home
              </small>
            )}
            {p.path !== '/' ? (
              <button
                type="button"
                className="pg-icon-btn"
                aria-label={`Delete page ${p.title}`}
                title="Delete page"
                onClick={() => void removePage(p)}
              >
                ×
              </button>
            ) : null}
            </div>
            {p.path === pagePath ? (
              <PageSettings
                page={p}
                index={pageIndex}
                count={spec.pages.length}
                inNav={isInNav(spec, p.path)}
                idPrefix="pages-panel"
                onAccess={(access) => dispatch({ type: 'setPageAccess', path: p.path, access })}
                onNavVisible={(visible) => dispatch({ type: 'setNavVisible', path: p.path, visible })}
                onMove={(delta) => dispatch({ type: 'movePage', path: p.path, delta })}
              />
            ) : null}
          </li>
        ))}
      </ul>
      {newPageTitle == null ? (
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={spec.pages.length >= MAX_PAGES} onClick={() => setNewPageTitle('')}>
          + Add page
        </button>
      ) : (
        <form
          className="pg-stack"
          onSubmit={(e) => {
            e.preventDefault();
            if (newPageTitle.trim()) dispatch({ type: 'addPage', title: newPageTitle.trim() });
            setNewPageTitle(null);
          }}
        >
          <input
            aria-label="New page title"
            autoFocus
            maxLength={60}
            placeholder="Page title"
            value={newPageTitle}
            onChange={(e) => setNewPageTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== 'Escape') return;
              // Handled here: the same Escape must not also close the drawer around this panel.
              e.preventDefault();
              setNewPageTitle(null);
            }}
          />
          <div className="pg-row">
            <button type="submit" className="pg-btn pg-btn-sm" disabled={!newPageTitle.trim()}>
              Add blank
            </button>
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setNewPageTitle(null)}>
              Cancel
            </button>
          </div>
        </form>
      )}
      <p className="pg-list-title">Start from template</p>
      <div className="pg-toy-grid">
        {PAGE_TEMPLATES.filter((t) => t.id !== 'blank').map((t) => (
          <button
            key={t.id}
            type="button"
            className="pg-toy pg-palette-card"
            disabled={spec.pages.length >= MAX_PAGES}
            onClick={() => {
              const title = t.title;
              dispatch({ type: 'addPageFromTemplate', title, templateId: t.id });
            }}
          >
            <span className="pg-toy-mark" data-tone="violet" aria-hidden="true">
              {t.title.slice(0, 1)}
            </span>
            <span className="pg-toy-copy">
              <strong>{t.title}</strong>
              <small>{t.description}</small>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
