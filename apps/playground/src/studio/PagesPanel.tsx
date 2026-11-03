import { PAGE_TEMPLATES, type DappPage, type DappSpec, type PageAccess } from '@splendor/dapp-spec';
import { useEffect, useState, type Dispatch } from 'react';
import { MAX_PAGES, pagePathError, type BuilderAction } from '../builder/state';
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

/** URL path of a page: edited as text, committed on Enter or blur when valid, explained when not. */
export function PagePathInput({ spec, path, onCommit, id }: { spec: Pick<DappSpec, 'pages'>; path: string; onCommit: (next: string) => void; id: string }) {
  const [draft, setDraft] = useState(path);
  useEffect(() => setDraft(path), [path]);
  const error = draft === path ? null : pagePathError(spec, path, draft.trim());
  const commit = () => {
    const next = draft.trim();
    if (next === path) return;
    if (pagePathError(spec, path, next)) return;
    onCommit(next);
  };
  return (
    <div className="pg-field">
      <label htmlFor={id}>URL path</label>
      <input
        id={id}
        className="pg-mono"
        value={draft}
        maxLength={41}
        spellCheck={false}
        autoComplete="off"
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : `${id}-hint`}
        onChange={(e) => setDraft(e.target.value.toLowerCase().replace(/\s+/g, '-'))}
        onBlur={() => {
          if (error) setDraft(path);
          else commit();
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            commit();
          } else if (e.key === 'Escape') {
            e.preventDefault();
            setDraft(path);
          }
        }}
      />
      {error ? (
        <small id={`${id}-error`} className="pg-error" role="alert">
          {error}
        </small>
      ) : (
        <small id={`${id}-hint`} className="pg-muted">
          Links to this page follow the change.
        </small>
      )}
    </div>
  );
}

/** Search summary of one page; counts toward the 160 characters search engines show. */
export function PageDescriptionInput({ path, description, onChange, id }: { path: string; description?: string; onChange: (d: string) => void; id: string }) {
  const value = description ?? '';
  return (
    <div className="pg-field">
      <label htmlFor={id}>Search description</label>
      <textarea
        id={id}
        rows={2}
        maxLength={160}
        value={value}
        placeholder={path === '/' ? 'What this dApp does, in one sentence' : 'What visitors find on this page'}
        aria-describedby={`${id}-count`}
        onChange={(e) => onChange(e.target.value)}
      />
      <small id={`${id}-count`} className="pg-muted">
        {value.length}/160 · shown by search engines and link previews
      </small>
    </div>
  );
}

/** Site description, favicon, brand, footer and the Connect button of the dApp's header. */
export function NavSettings({ spec, dispatch }: { spec: DappSpec; dispatch: Dispatch<BuilderAction> }) {
  const nav = spec.nav ?? {};
  const favicon = spec.favicon ?? '';
  const faviconBad = favicon !== '' && !/^https:\/\/\S+\.(png|ico|svg|webp|jpe?g)(\?\S*)?$/i.test(favicon);
  return (
    <details className="pg-nav-settings" data-testid="nav-settings">
      <summary>Site settings</summary>
      <div className="pg-form">
        <div className="pg-field">
          <label htmlFor="site-description">Site description</label>
          <textarea
            id="site-description"
            rows={2}
            maxLength={500}
            value={spec.description ?? ''}
            placeholder="Used when a page has no description of its own"
            onChange={(e) => dispatch({ type: 'setDescription', description: e.target.value })}
          />
        </div>
        <div className="pg-field">
          <label htmlFor="site-favicon">Favicon URL</label>
          <input
            id="site-favicon"
            className="pg-mono"
            inputMode="url"
            value={favicon}
            placeholder="https://…/icon.png"
            aria-invalid={faviconBad ? true : undefined}
            aria-describedby={faviconBad ? 'site-favicon-error' : undefined}
            onChange={(e) => dispatch({ type: 'setFavicon', favicon: e.target.value })}
          />
          {faviconBad ? (
            <small id="site-favicon-error" className="pg-error">
              Use an https link to a .png, .ico, .svg, .webp or .jpg file
            </small>
          ) : null}
        </div>
        <div className="pg-field">
          <label htmlFor="nav-brand">Brand name</label>
          <input
            id="nav-brand"
            maxLength={60}
            value={nav.brand ?? ''}
            placeholder={spec.name}
            onChange={(e) => dispatch({ type: 'setNav', nav: { brand: e.target.value || undefined } })}
          />
        </div>
        <div className="pg-field">
          <label htmlFor="nav-footer">Footer text</label>
          <input
            id="nav-footer"
            maxLength={200}
            value={nav.footer ?? ''}
            placeholder="© 2026 My project"
            onChange={(e) => dispatch({ type: 'setNav', nav: { footer: e.target.value || undefined } })}
          />
        </div>
        <div className="pg-field pg-field-inline">
          <input
            id="nav-connect"
            type="checkbox"
            checked={nav.showConnect !== false}
            onChange={(e) => dispatch({ type: 'setNav', nav: { showConnect: e.target.checked ? undefined : false } })}
          />
          <label htmlFor="nav-connect">Show the Connect wallet button</label>
        </div>
      </div>
    </details>
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
  /** Page template waiting for its title (the add form is reused for it). */
  const [templateId, setTemplateId] = useState<string | null>(null);
  const template = templateId ? PAGE_TEMPLATES.find((t) => t.id === templateId) : undefined;
  /** Path of the page being dragged in the list (mouse reorder). */
  const [dragPath, setDragPath] = useState<string | null>(null);
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
          <li
            key={p.path}
            className={[p.path === pagePath ? 'is-active' : '', dragPath && dragPath !== p.path ? 'is-drop-target' : ''].filter(Boolean).join(' ')}
            draggable
            data-testid={`page-row-${p.path}`}
            onDragStart={(e) => {
              setDragPath(p.path);
              e.dataTransfer.effectAllowed = 'move';
              e.dataTransfer.setData('text/plain', p.path);
            }}
            onDragOver={(e) => {
              if (dragPath && dragPath !== p.path) e.preventDefault();
            }}
            onDrop={(e) => {
              e.preventDefault();
              if (dragPath) dispatch({ type: 'reorderPage', path: dragPath, index: pageIndex });
              setDragPath(null);
            }}
            onDragEnd={() => setDragPath(null)}
          >
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
            {p.path === pagePath && p.path !== '/' ? (
              <PagePathInput id="pages-panel-path" spec={spec} path={p.path} onCommit={(nextPath) => dispatch({ type: 'setPagePath', path: p.path, nextPath })} />
            ) : null}
            {p.path === pagePath ? (
              <PageDescriptionInput
                id="pages-panel-description"
                path={p.path}
                description={p.description}
                onChange={(description) => dispatch({ type: 'setPageDescription', path: p.path, description })}
              />
            ) : null}
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
            const title = newPageTitle.trim();
            if (title && template) dispatch({ type: 'addPageFromTemplate', title, templateId: template.id });
            else if (title) dispatch({ type: 'addPage', title });
            setNewPageTitle(null);
            setTemplateId(null);
          }}
        >
          <label className="pg-muted" htmlFor="new-page-title">
            {template ? `Title for the new ${template.title} page` : 'Title for the new page'}
          </label>
          <input
            id="new-page-title"
            aria-label="New page title"
            autoFocus
            maxLength={60}
            placeholder={template?.title ?? 'Page title'}
            value={newPageTitle}
            onChange={(e) => setNewPageTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== 'Escape') return;
              // Handled here: the same Escape must not also close the drawer around this panel.
              e.preventDefault();
              setNewPageTitle(null);
              setTemplateId(null);
            }}
          />
          <div className="pg-row">
            <button type="submit" className="pg-btn pg-btn-sm" disabled={!newPageTitle.trim()}>
              {template ? `Add ${template.title} page` : 'Add blank'}
            </button>
            <button
              type="button"
              className="pg-btn pg-btn-ghost pg-btn-sm"
              onClick={() => {
                setNewPageTitle(null);
                setTemplateId(null);
              }}
            >
              Cancel
            </button>
          </div>
        </form>
      )}
      <NavSettings spec={spec} dispatch={dispatch} />
      <p className="pg-list-title">Start from template</p>
      <div className="pg-toy-grid">
        {PAGE_TEMPLATES.filter((t) => t.id !== 'blank').map((t) => (
          <button
            key={t.id}
            type="button"
            className="pg-toy pg-palette-card"
            disabled={spec.pages.length >= MAX_PAGES}
            data-testid={`page-template-${t.id}`}
            onClick={() => {
              setTemplateId(t.id);
              setNewPageTitle(t.title);
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
