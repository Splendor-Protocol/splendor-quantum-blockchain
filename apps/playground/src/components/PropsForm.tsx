import type { CatalogEntry, ContractRef, DappPage, PropSchema, PropValue } from '@splendor/dapp-spec';
import { ListEditor, listColumns } from '../builder/ListEditor';

export type PropsFormProps = {
  entry: CatalogEntry;
  values: Record<string, PropValue>;
  onChange: (name: string, value: PropValue | undefined) => void;
  /** Validation messages keyed by prop name. */
  errors?: Record<string, string>;
  /**
   * The dApp's contracts. With this, `contract` and `function` props become pickers (the studio passes
   * it; the widget gallery does not and keeps plain text fields).
   */
  contracts?: readonly ContractRef[];
  /** Pages a link column can point to. */
  pages?: readonly Pick<DappPage, 'path' | 'title'>[];
  /** Opens the studio's Contracts panel (the "+ Add contract" button is hidden without it). */
  onOpenContracts?: () => void;
  /** Addresses an `address` field can be filled with (your wallet, contacts, contracts). */
  addressSuggestions?: readonly AddressSuggestion[];
};

export type AddressSuggestion = { label: string; address: string };

const isEmpty = (v: PropValue | undefined) => v === undefined || (typeof v === 'string' && v.trim() === '');

/** Inline message for a number outside the manifest's range, or null. */
export function numberFieldError(p: Pick<PropSchema, 'min' | 'max'>, value: PropValue | undefined): string | null {
  if (typeof value !== 'number') return null;
  if (!Number.isFinite(value)) return 'Enter a number';
  if (p.min != null && value < p.min) return `At least ${p.min}`;
  if (p.max != null && value > p.max) return `At most ${p.max}`;
  return null;
}

/** "Between 1 and 200" style hint for a number prop, or null when it has no range. */
export function numberRangeHint(p: Pick<PropSchema, 'min' | 'max'>): string | null {
  if (p.min != null && p.max != null) return `Between ${p.min} and ${p.max}`;
  if (p.min != null) return `At least ${p.min}`;
  if (p.max != null) return `At most ${p.max}`;
  return null;
}

const isInt = (n: number | undefined) => n == null || Number.isInteger(n);

/** Contracts a `format: 'contract'` prop may pick. */
export function contractChoices(contracts: readonly ContractRef[], kind: PropSchema['contractKind']): ContractRef[] {
  if (kind === 'token') return contracts.filter((c) => c.kind === 'token');
  if (kind === 'contract') return contracts.filter((c) => c.kind !== 'token');
  return [...contracts];
}

/** `name(types)` for a function: its signature already carries the parameter types. */
export const functionLabel = (f: ContractRef['functions'][number]) => `${f.signature || f.name} · ${f.kind}`;

/** Form generated from a manifest's `PropSchema[]` — new widgets get an editor for free. */
export function PropsForm({ entry, values, onChange, errors = {}, contracts, pages = [], onOpenContracts, addressSuggestions = [] }: PropsFormProps) {
  if (!entry.props.length) return <p className="pg-muted">This item has no settings.</p>;

  // A picked contract supplies its own address: hide the raw address field while it is empty.
  const hidden = new Set<string>();
  if (contracts) {
    for (const p of entry.props) {
      if (p.format !== 'contract') continue;
      const addressProp = p.addressProp ?? 'contract';
      if (contracts.some((c) => c.id === values[p.name]) && isEmpty(values[addressProp])) hidden.add(addressProp);
    }
  }

  return (
    <div className="pg-form">
      {entry.props.map((p) => {
        if (hidden.has(p.name)) return null;
        const id = `prop-${entry.id}-${p.name}`;
        const value = values[p.name];
        const err = errors[p.name] ?? (p.type === 'number' ? numberFieldError(p, value) : null) ?? undefined;
        const describedBy = [p.description ? `${id}-help` : null, err ? `${id}-error` : null].filter(Boolean).join(' ') || undefined;
        const a11y = { 'aria-describedby': describedBy, 'aria-invalid': err ? true : undefined, 'aria-required': p.required || undefined };
        const label = (
          <label htmlFor={id}>
            {p.label}
            {p.required ? (
              <span className="pg-required" aria-hidden="true">
                {' '}
                *
              </span>
            ) : null}
          </label>
        );
        const help = p.description ? (
          <small id={`${id}-help`} className="pg-muted">
            {p.description}
          </small>
        ) : null;
        const error = err ? (
          <small id={`${id}-error`} className="pg-error">
            {err}
          </small>
        ) : null;
        if (p.type === 'boolean') {
          return (
            <div key={p.name} className="pg-field pg-field-inline">
              <input id={id} type="checkbox" checked={value === true} onChange={(e) => onChange(p.name, e.target.checked)} {...a11y} />
              {label}
              {help}
            </div>
          );
        }

        const list = p.type === 'string' ? listColumns(entry, p) : null;
        if (list) {
          return (
            <div key={p.name} className="pg-field">
              <ListEditor
                id={id}
                label={p.label}
                value={typeof value === 'string' ? value : ''}
                columns={list.columns}
                rowSeparator={list.rowSeparator}
                pages={pages}
                required={p.required}
                describedBy={describedBy}
                invalid={Boolean(err)}
                onChange={(v) => onChange(p.name, v)}
              />
              {help}
              {error}
            </div>
          );
        }

        if (contracts && p.type === 'string' && p.format === 'contract') {
          const choices = contractChoices(contracts, p.contractKind);
          const current = typeof value === 'string' ? value : '';
          const chosen = contracts.find((c) => c.id === current);
          const addressProp = p.addressProp ?? 'contract';
          return (
            <div key={p.name} className="pg-field">
              {label}
              <div className="pg-picker-row">
                <select id={id} value={current} onChange={(e) => onChange(p.name, e.target.value || undefined)} {...a11y}>
                  <option value="">— none —</option>
                  {choices.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                  {current && !choices.some((c) => c.id === current) ? <option value={current}>{chosen ? chosen.name : `${current} (missing)`}</option> : null}
                </select>
                {onOpenContracts ? (
                  <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={onOpenContracts}>
                    + Add contract
                  </button>
                ) : null}
              </div>
              {chosen && isEmpty(values[addressProp]) ? (
                <small className="pg-muted" data-testid={`${id}-address-note`}>
                  Address comes from {chosen.name}
                  {chosen.address.trim() ? '' : '. Add its address in Contracts.'}
                </small>
              ) : null}
              {help}
              {error}
            </div>
          );
        }

        if (contracts && p.type === 'string' && p.format === 'function') {
          const contract = contracts.find((c) => c.id === values[p.contractProp ?? 'contractId']);
          const fns = (contract?.functions ?? []).filter((f) => !p.functionKind || f.kind === p.functionKind);
          if (contract && fns.length) {
            const current = typeof value === 'string' ? value : '';
            return (
              <div key={p.name} className="pg-field">
                {label}
                <select id={id} value={current} onChange={(e) => onChange(p.name, e.target.value || undefined)} {...a11y}>
                  <option value="">— pick a function —</option>
                  {fns.map((f) => (
                    <option key={f.signature} value={f.signature}>
                      {functionLabel(f)}
                    </option>
                  ))}
                  {current && !fns.some((f) => f.signature === current) ? <option value={current}>{current} (custom)</option> : null}
                </select>
                {help}
                {error}
              </div>
            );
          }
        }

        const fallback = entry.defaults[p.name];
        let control;
        let hint: string | null = null;
        if (p.type === 'select') {
          const def = p.options?.find((o) => o.value === fallback)?.label;
          control = (
            <select id={id} value={typeof value === 'string' ? value : ''} onChange={(e) => onChange(p.name, e.target.value || undefined)} {...a11y}>
              <option value="">{def ? `Default (${def})` : 'Default'}</option>
              {p.options?.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          );
        } else if (p.type === 'number') {
          hint = numberRangeHint(p);
          control = (
            <input
              id={id}
              type="number"
              min={p.min}
              max={p.max}
              step={isInt(p.min) && isInt(p.max) ? 1 : 'any'}
              inputMode={isInt(p.min) && isInt(p.max) ? 'numeric' : 'decimal'}
              value={typeof value === 'number' ? value : ''}
              placeholder={p.placeholder ?? (typeof fallback === 'number' ? String(fallback) : undefined)}
              onChange={(e) => onChange(p.name, e.target.value === '' ? undefined : Number(e.target.value))}
              {...a11y}
            />
          );
        } else if (p.multiline) {
          const text = typeof value === 'string' ? value : '';
          control = (
            <textarea
              id={id}
              className="pg-textarea-auto"
              rows={Math.min(8, Math.max(3, text.split('\n').length + 1))}
              value={text}
              placeholder={p.placeholder ?? (typeof fallback === 'string' ? fallback : undefined)}
              onChange={(e) => onChange(p.name, e.target.value === '' ? undefined : e.target.value)}
              {...a11y}
            />
          );
        } else {
          const input = (
            <input
              id={id}
              type="text"
              spellCheck={p.format === 'text'}
              autoComplete="off"
              className={p.format === 'address' ? 'pg-mono' : undefined}
              inputMode={p.format === 'amount' ? 'decimal' : p.format === 'url' ? 'url' : p.format === 'integer' ? 'numeric' : undefined}
              value={typeof value === 'string' ? value : ''}
              placeholder={p.placeholder}
              onChange={(e) => onChange(p.name, e.target.value === '' ? undefined : e.target.value)}
              {...a11y}
            />
          );
          const picks = p.format === 'address' ? addressSuggestions.filter((s) => s.address !== value) : [];
          control = picks.length ? (
            <div className="pg-picker-row">
              {input}
              <select
                aria-label={`Fill ${p.label} with`}
                className="pg-fill-select"
                data-testid={`${id}-fill`}
                value=""
                onChange={(e) => {
                  if (e.target.value) onChange(p.name, e.target.value);
                }}
              >
                <option value="">Fill with…</option>
                {picks.map((s) => (
                  <option key={`${s.label}:${s.address}`} value={s.address}>
                    {s.label}
                  </option>
                ))}
              </select>
            </div>
          ) : (
            input
          );
        }
        return (
          <div key={p.name} className="pg-field">
            {label}
            {control}
            {hint ? <small className="pg-muted">{hint}</small> : null}
            {help}
            {error}
          </div>
        );
      })}
    </div>
  );
}
