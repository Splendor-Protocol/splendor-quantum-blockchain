import { useState } from 'react';
import type { CatalogEntry, DappBlock, DappPage, NodeStyle, PropValue } from '@splendor/dapp-spec';
import { PropsForm } from '../components/PropsForm';
import { Tabs, type TabItem } from '../components/Tabs';
import { StatusBadge } from '../gallery/GalleryPage';

type InspectorTab = 'props' | 'style' | 'actions';

const TABS: readonly TabItem<InspectorTab>[] = [
  { id: 'props', label: 'Props' },
  { id: 'style', label: 'Style' },
  { id: 'actions', label: 'Actions' },
];

const ACTION_PROPS: Record<string, { name: string; label: string }[]> = {
  'hero-centered': [{ name: 'ctaPath', label: 'CTA path or URL' }, { name: 'ctaLabel', label: 'CTA label' }],
  'cta-banner': [{ name: 'buttonPath', label: 'Button path or URL' }, { name: 'buttonLabel', label: 'Button label' }],
  'action-button': [{ name: 'href', label: 'Path or URL' }, { name: 'label', label: 'Label' }],
};

const STYLE_FIELDS: { key: keyof NodeStyle; label: string; placeholder?: string }[] = [
  { key: 'width', label: 'Width', placeholder: '100%' },
  { key: 'maxWidth', label: 'Max width', placeholder: '720px' },
  { key: 'gap', label: 'Gap', placeholder: '1rem' },
  { key: 'padding', label: 'Padding', placeholder: '1rem' },
  { key: 'background', label: 'Background', placeholder: 'transparent' },
];

export function Inspector({
  page,
  selected,
  selectedEntry,
  selectedIndex,
  blockCount,
  blockErrors,
  compatibilityNote,
  onPropChange,
  onStyleChange,
  onMoveBlock,
  onRenamePage,
}: {
  page: DappPage;
  selected: DappBlock | null;
  selectedEntry: CatalogEntry | undefined;
  selectedIndex: number;
  blockCount: number;
  blockErrors: Record<string, string>;
  compatibilityNote: (entry: CatalogEntry) => string | null;
  onPropChange: (name: string, value: PropValue | undefined) => void;
  onStyleChange: (style: NodeStyle) => void;
  onMoveBlock: (delta: -1 | 1) => void;
  onRenamePage: (title: string) => void;
}) {
  const [tab, setTab] = useState<InspectorTab>('props');

  if (!selected || !selectedEntry) {
    return (
      <div className="pg-stack">
        <p className="pg-muted" style={{ margin: 0 }}>
          Click a piece in the row to change its words, addresses and amounts.
        </p>
        <div className="pg-field">
          <label htmlFor="page-title">Page title</label>
          <input id="page-title" value={page.title} maxLength={60} onChange={(e) => onRenamePage(e.target.value)} />
        </div>
      </div>
    );
  }

  const actionFields = ACTION_PROPS[selected.widget] ?? [];
  const style = selected.style ?? {};

  return (
    <Tabs idPrefix="inspector" label="Inspector" tabs={TABS} value={tab} onChange={setTab}>
      {tab === 'props' ? (
        <div className="pg-stack">
          <div className="pg-row">
            <h3 style={{ margin: 0 }}>{selectedEntry.title}</h3>
            <StatusBadge status={selectedEntry.status} />
          </div>
          <p className="pg-muted" style={{ margin: 0 }}>
            {selectedEntry.description}
          </p>
          <div className="pg-row">
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={selectedIndex <= 0} onClick={() => onMoveBlock(-1)}>
              Earlier
            </button>
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={selectedIndex < 0 || selectedIndex >= blockCount - 1} onClick={() => onMoveBlock(1)}>
              Later
            </button>
          </div>
          {compatibilityNote(selectedEntry) ? <p className="pg-error">{compatibilityNote(selectedEntry)}</p> : null}
          <PropsForm entry={selectedEntry} values={selected.props} errors={blockErrors} onChange={onPropChange} />
        </div>
      ) : null}
      {tab === 'style' ? (
        <div className="pg-form">
          {STYLE_FIELDS.map(({ key, label, placeholder }) => (
            <div key={key} className="pg-field">
              <label htmlFor={`style-${key}`}>{label}</label>
              <input
                id={`style-${key}`}
                value={style[key] ?? ''}
                placeholder={placeholder}
                onChange={(e) => {
                  const next: Record<string, string | undefined> = { ...style };
                  if (e.target.value.trim()) next[key] = e.target.value;
                  else delete next[key];
                  onStyleChange(next as NodeStyle);
                }}
              />
            </div>
          ))}
          <div className="pg-field">
            <label htmlFor="style-align">Align</label>
            <select
              id="style-align"
              value={style.align ?? ''}
              onChange={(e) => {
                const next = { ...style };
                if (e.target.value) next.align = e.target.value as NodeStyle['align'];
                else delete next.align;
                onStyleChange(next);
              }}
            >
              <option value="">Default</option>
              <option value="start">Start</option>
              <option value="center">Center</option>
              <option value="end">End</option>
              <option value="stretch">Stretch</option>
              <option value="between">Space between</option>
            </select>
          </div>
          <div className="pg-field">
            <label htmlFor="style-hide">Hide on</label>
            <select
              id="style-hide"
              value={style.hideOn ?? ''}
              onChange={(e) => {
                const next = { ...style };
                if (e.target.value === 'mobile' || e.target.value === 'desktop') next.hideOn = e.target.value;
                else delete next.hideOn;
                onStyleChange(next);
              }}
            >
              <option value="">Always show</option>
              <option value="mobile">Mobile</option>
              <option value="desktop">Desktop</option>
            </select>
          </div>
        </div>
      ) : null}
      {tab === 'actions' ? (
        actionFields.length ? (
          <div className="pg-form">
            {actionFields.map((f) => (
              <div key={f.name} className="pg-field">
                <label htmlFor={`action-${f.name}`}>{f.label}</label>
                <input
                  id={`action-${f.name}`}
                  value={String(selected.props[f.name] ?? '')}
                  onChange={(e) => onPropChange(f.name, e.target.value || undefined)}
                  placeholder="/page or https://…"
                />
              </div>
            ))}
          </div>
        ) : (
          <p className="pg-muted">This block has no navigation actions.</p>
        )
      ) : null}
    </Tabs>
  );
}
