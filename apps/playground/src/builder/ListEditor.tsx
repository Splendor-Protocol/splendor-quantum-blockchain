import { useEffect, useRef, useState } from 'react';
import type { CatalogEntry, DappPage, PropSchema } from '@splendor/dapp-spec';

export type ListColumn = {
  key: string;
  label: string;
  placeholder?: string;
  /** `page`: a pick of the dApp's pages, with a custom address option. Default `text`. */
  kind?: 'text' | 'page';
};

type PageRef = Pick<DappPage, 'path' | 'title'>;

export const MAX_LIST_ROWS = 50;

/** Legacy text props that hold one item per row (`a|b` columns, rows split like the blocks' `parseList`). */
const LEGACY_LIST_PROPS = ['items', 'links', 'features', 'tokenIds'] as const;

const TWO_COLUMN: Record<string, ListColumn[]> = {
  'feature-grid': [
    { key: 'title', label: 'Title', placeholder: 'Fast' },
    { key: 'text', label: 'Description', placeholder: 'Ship quickly' },
  ],
  faq: [
    { key: 'question', label: 'Question', placeholder: 'What is this?' },
    { key: 'answer', label: 'Answer', placeholder: 'A Splendor dApp.' },
  ],
  steps: [
    { key: 'title', label: 'Title', placeholder: 'Connect' },
    { key: 'text', label: 'Description', placeholder: 'Link your wallet' },
  ],
};

const SINGLE_LABEL: Record<string, string> = { features: 'Feature', tokenIds: 'Token id', items: 'Item' };

/**
 * The columns a list editor should show for `prop` of `entry`, or null when the prop is a plain field.
 * `format: 'list'` props bring their own columns; the old comma-separated text props get a sensible set.
 */
export function listColumns(entry: Pick<CatalogEntry, 'id'>, prop: PropSchema): { columns: ListColumn[]; rowSeparator: 'newline' | 'auto' } | null {
  if (prop.format === 'list') {
    const columns = prop.columns?.length ? prop.columns : [{ key: 'value', label: 'Value' }];
    return { columns, rowSeparator: 'newline' };
  }
  if (prop.type !== 'string' || !(LEGACY_LIST_PROPS as readonly string[]).includes(prop.name)) return null;
  if (prop.name === 'links') {
    return { columns: [{ key: 'label', label: 'Label', placeholder: 'Home' }, { key: 'path', label: 'Link', placeholder: '/about', kind: 'page' }], rowSeparator: 'auto' };
  }
  const pair = TWO_COLUMN[entry.id] ?? (prop.name === 'items' && prop.placeholder?.includes('|') ? TWO_COLUMN['feature-grid'] : undefined);
  if (pair && prop.name === 'items') return { columns: pair, rowSeparator: 'auto' };
  const label = entry.id === 'logo-row' ? 'Name' : (SINGLE_LABEL[prop.name] ?? 'Item');
  return { columns: [{ key: 'value', label }], rowSeparator: 'auto' };
}

/** Rows of cells from the stored text. `auto` follows the blocks: newline-separated when the text has a newline, else commas. */
export function parseRows(value: string, columnCount: number, rowSeparator: 'newline' | 'auto'): string[][] {
  const parts = value.split(rowSeparator === 'newline' || value.includes('\n') ? /\r?\n/ : ',');
  return parts
    .map((row) => row.trim())
    .filter(Boolean)
    .map((row) => {
      const cells = row.split('|').map((c) => c.trim());
      // Extra separators belong to the last column.
      const head = cells.slice(0, columnCount - 1);
      const tail = cells.slice(columnCount - 1).join('|');
      return [...head, tail].concat(Array.from({ length: Math.max(0, columnCount - head.length - 1) }, () => ''));
    });
}

/** The stored text: blank rows dropped, cells joined with `|`, rows with newlines. */
export function serializeRows(rows: string[][], rowSeparator: 'newline' | 'auto'): string {
  const lines = rows.filter((r) => r.some((c) => c.trim())).map((r) => r.map((c) => c.replace(/[|\r\n]+/g, ' ').trim()).join('|'));
  // A single row that holds a comma would be split by the comma rule: end it with a newline to switch the rule.
  if (rowSeparator === 'auto' && lines.length === 1 && lines[0]!.includes(',')) return `${lines[0]}\n`;
  return lines.join('\n');
}

const CUSTOM = '__custom';

function PageCell({ id, label, value, pages, onChange }: { id: string; label: string; value: string; pages: readonly PageRef[]; onChange: (value: string) => void }) {
  const known = pages.some((p) => p.path === value);
  const [custom, setCustom] = useState(Boolean(value) && !known);
  const mode = custom || (Boolean(value) && !known) ? CUSTOM : value;
  return (
    <span className="pg-list-cell pg-list-cell-page">
      <select
        id={id}
        aria-label={label}
        value={mode}
        onChange={(e) => {
          if (e.target.value === CUSTOM) {
            setCustom(true);
            return;
          }
          setCustom(false);
          onChange(e.target.value);
        }}
      >
        <option value="">No link</option>
        {pages.map((p) => (
          <option key={p.path} value={p.path}>
            {p.title} ({p.path})
          </option>
        ))}
        <option value={CUSTOM}>Custom address…</option>
      </select>
      {mode === CUSTOM ? <input aria-label={`${label} address`} value={value} placeholder="/path or https://…" spellCheck={false} onChange={(e) => onChange(e.target.value)} /> : null}
    </span>
  );
}

/**
 * Row editor for list props: one row per item, one input per column. Rows can be added, removed and
 * moved; the stored value is rows separated by newlines and columns by `|`.
 */
export function ListEditor({
  id,
  label,
  value,
  columns,
  rowSeparator = 'newline',
  pages = [],
  required,
  describedBy,
  invalid,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  columns: readonly ListColumn[];
  rowSeparator?: 'newline' | 'auto';
  pages?: readonly PageRef[];
  required?: boolean;
  describedBy?: string;
  invalid?: boolean;
  onChange: (value: string | undefined) => void;
}) {
  const count = Math.max(1, columns.length);
  const [rows, setRows] = useState<string[][]>(() => parseRows(value, count, rowSeparator));
  const last = useRef(value);

  // Edits made elsewhere (undo, another selection) replace the rows; our own edits echo back unchanged.
  useEffect(() => {
    if (value === last.current) return;
    last.current = value;
    setRows((current) => (serializeRows(current, rowSeparator) === serializeRows(parseRows(value, count, rowSeparator), rowSeparator) ? current : parseRows(value, count, rowSeparator)));
  }, [value, count, rowSeparator]);

  const commit = (next: string[][]) => {
    setRows(next);
    const text = serializeRows(next, rowSeparator);
    last.current = text;
    onChange(text === '' ? undefined : text);
  };
  const setCell = (r: number, c: number, text: string) => commit(rows.map((row, i) => (i === r ? row.map((cell, j) => (j === c ? text : cell)) : row)));
  const move = (r: number, delta: -1 | 1) => {
    const to = r + delta;
    if (to < 0 || to >= rows.length) return;
    const next = [...rows];
    [next[r], next[to]] = [next[to]!, next[r]!];
    commit(next);
  };

  return (
    <fieldset className="pg-list-editor" id={id} aria-describedby={describedBy} aria-invalid={invalid ? true : undefined}>
      <legend>
        {label}
        {required ? (
          <span className="pg-required" aria-hidden="true">
            {' '}
            *
          </span>
        ) : null}
      </legend>
      {rows.length === 0 ? <p className="pg-muted pg-list-empty">No rows yet.</p> : null}
      <ol className="pg-list-rows">
        {rows.map((row, r) => (
          <li key={r} className="pg-list-row">
            <span className="pg-list-cells" style={{ gridTemplateColumns: `repeat(${count}, minmax(0, 1fr))` }}>
              {columns.map((col, c) => {
                const name = `${col.label} row ${r + 1}`;
                return col.kind === 'page' ? (
                  <PageCell key={col.key} id={`${id}-${r}-${col.key}`} label={name} value={row[c] ?? ''} pages={pages} onChange={(v) => setCell(r, c, v)} />
                ) : (
                  <input
                    key={col.key}
                    id={`${id}-${r}-${col.key}`}
                    aria-label={name}
                    value={row[c] ?? ''}
                    placeholder={col.placeholder ?? col.label}
                    autoComplete="off"
                    inputMode={col.key === 'percent' || /id$/i.test(col.key) ? 'decimal' : undefined}
                    onChange={(e) => setCell(r, c, e.target.value)}
                  />
                );
              })}
            </span>
            <span className="pg-list-actions">
              <button type="button" className="pg-icon-btn" aria-label={`Move row ${r + 1} up`} title="Move up" disabled={r === 0} onClick={() => move(r, -1)}>
                ↑
              </button>
              <button type="button" className="pg-icon-btn" aria-label={`Move row ${r + 1} down`} title="Move down" disabled={r === rows.length - 1} onClick={() => move(r, 1)}>
                ↓
              </button>
              <button type="button" className="pg-icon-btn" aria-label={`Remove row ${r + 1}`} title="Remove row" onClick={() => commit(rows.filter((_, i) => i !== r))}>
                ×
              </button>
            </span>
          </li>
        ))}
      </ol>
      <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={rows.length >= MAX_LIST_ROWS} onClick={() => setRows([...rows, Array.from({ length: count }, () => '')])}>
        + Add row
      </button>
    </fieldset>
  );
}
