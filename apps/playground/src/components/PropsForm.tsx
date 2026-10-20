import type { CatalogEntry, PropValue } from '@splendor/dapp-spec';

export type PropsFormProps = {
  entry: CatalogEntry;
  values: Record<string, PropValue>;
  onChange: (name: string, value: PropValue | undefined) => void;
  /** Validation messages keyed by prop name. */
  errors?: Record<string, string>;
};

/** Form generated from a manifest's `PropSchema[]` — new widgets get an editor for free. */
export function PropsForm({ entry, values, onChange, errors = {} }: PropsFormProps) {
  if (!entry.props.length) return <p className="pg-muted">This item has no settings.</p>;
  return (
    <div className="pg-form">
      {entry.props.map((p) => {
        const id = `prop-${entry.id}-${p.name}`;
        const value = values[p.name];
        const err = errors[p.name];
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
        if (p.type === 'boolean') {
          return (
            <div key={p.name} className="pg-field pg-field-inline">
              <input id={id} type="checkbox" checked={value === true} onChange={(e) => onChange(p.name, e.target.checked)} {...a11y} />
              {label}
              {help}
            </div>
          );
        }
        let control;
        if (p.type === 'select') {
          control = (
            <select id={id} value={typeof value === 'string' ? value : ''} onChange={(e) => onChange(p.name, e.target.value || undefined)} {...a11y}>
              <option value="">Default</option>
              {p.options?.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          );
        } else if (p.type === 'number') {
          control = (
            <input
              id={id}
              type="number"
              min={p.min}
              max={p.max}
              value={typeof value === 'number' ? value : ''}
              placeholder={p.placeholder}
              onChange={(e) => onChange(p.name, e.target.value === '' ? undefined : Number(e.target.value))}
              {...a11y}
            />
          );
        } else {
          control = (
            <input
              id={id}
              type="text"
              spellCheck={p.format === 'text'}
              autoComplete="off"
              className={p.format === 'address' ? 'pg-mono' : undefined}
              inputMode={p.format === 'amount' ? 'decimal' : p.format === 'url' ? 'url' : undefined}
              value={typeof value === 'string' ? value : ''}
              placeholder={p.placeholder}
              onChange={(e) => onChange(p.name, e.target.value === '' ? undefined : e.target.value)}
              {...a11y}
            />
          );
        }
        return (
          <div key={p.name} className="pg-field">
            {label}
            {control}
            {help}
            {err ? (
              <small id={`${id}-error`} className="pg-error">
                {err}
              </small>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
