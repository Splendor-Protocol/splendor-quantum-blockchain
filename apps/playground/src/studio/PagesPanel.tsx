import { PAGE_TEMPLATES, type DappSpec } from '@splendor/dapp-spec';
import type { Dispatch } from 'react';
import { MAX_PAGES, type BuilderAction } from '../builder/state';

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
  return (
    <div className="pg-stack">
      <p className="pg-toybox-lead">Pages</p>
      <ul className="pg-pages-list">
        {spec.pages.map((p) => (
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
              <input
                aria-label="Page title"
                className="pg-pages-rename"
                value={p.title}
                maxLength={60}
                onChange={(e) => dispatch({ type: 'renamePage', path: p.path, title: e.target.value })}
              />
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
                onClick={() => dispatch({ type: 'removePage', path: p.path })}
              >
                ×
              </button>
            ) : null}
            </div>
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
            onKeyDown={(e) => e.key === 'Escape' && setNewPageTitle(null)}
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
