import { useId, useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { cleanContactLabel } from '../core/addressBook.js';
import { useAddressBook, useNetwork } from '../react/hooks.js';
import { addressExplorerUrl, normalizeSqAddress } from './utils.js';
import { ADDRESS_BOOK_LABELS, formatLabel, useWidgetLabels, type AddressBookLabels } from './labels.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressDisplay } from './parts/AddressDisplay.js';
import { AddressInput } from './parts/inputs.js';

export type AddressBookProps = {
  className?: string;
  style?: CSSProperties;
  title?: ReactNode;
  hideTitle?: boolean;
  labels?: Partial<AddressBookLabels>;
  /** Show the add / remove controls. Default true. */
  allowEdit?: boolean;
  /** Called when a contact's name is clicked, e.g. to fill a recipient field. */
  onSelect?: (contact: { address: string; label: string }) => void;
};

/**
 * Labelled `SQ:` contacts for the current network, saved in this browser only. Transfer
 * widgets on the same page offer them as quick picks under the recipient field.
 */
export function AddressBook({ className, style, title, hideTitle, labels, allowEdit = true, onSelect }: AddressBookProps) {
  const l = useWidgetLabels(ADDRESS_BOOK_LABELS, labels, 'addressBook');
  const { network } = useNetwork();
  const { contacts, save, remove } = useAddressBook();
  const [name, setName] = useState('');
  const [addr, setAddr] = useState('');
  const [query, setQuery] = useState('');
  const [message, setMessage] = useState<string | null>(null);
  const nameId = `${useId()}-name`;

  const nameError = name.trim() && !cleanContactLabel(name) ? l.invalidLabel : null;
  const canSave = !!cleanContactLabel(name) && !!normalizeSqAddress(addr);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? contacts.filter((c) => c.label.toLowerCase().includes(q) || c.address.includes(q)) : contacts;
  }, [contacts, query]);

  const onSave = () => {
    const res = save(addr, name);
    if (!res.ok) {
      setMessage(res.reason === 'full' ? l.full : l.invalidAddress);
      return;
    }
    setMessage(null);
    setName('');
    setAddr('');
  };

  return (
    <WidgetShell
      kind="address-book"
      className={className}
      style={style}
      title={title ?? l.title}
      hideTitle={hideTitle}
      gate={false}
    >
      {contacts.length > 5 ? (
        <input
          className="sqc-input"
          aria-label={l.search}
          placeholder={l.search}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      ) : null}
      {shown.length ? (
        <ul className="sqc-list" aria-label={l.title}>
          {shown.map((c) => (
            <li key={c.address} className="sqc-list__item" data-testid="sqc-contact">
              {onSelect ? (
                <button type="button" className="sqc-copy sqc-list__label" onClick={() => onSelect(c)}>
                  {c.label}
                </button>
              ) : (
                <span className="sqc-list__label">{c.label}</span>
              )}
              <AddressDisplay value={c.address} href={addressExplorerUrl(network?.explorerUrl, c.address)} labels={l} />
              {allowEdit ? (
                <button
                  type="button"
                  className="sqc-copy"
                  aria-label={formatLabel(l.removeAria, { label: c.label })}
                  onClick={() => remove(c.address)}
                >
                  {l.remove}
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="sqc-widget__hint">{l.empty}</p>
      )}
      {allowEdit ? (
        <>
          <div className="sqc-field">
            <label className="sqc-widget__label" htmlFor={nameId}>
              {l.labelLabel}
            </label>
            <input
              id={nameId}
              className="sqc-input"
              value={name}
              maxLength={60}
              placeholder={l.labelPlaceholder}
              onChange={(e) => setName(e.target.value)}
              aria-invalid={!!nameError}
            />
            {nameError ? (
              <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
                {nameError}
              </p>
            ) : null}
          </div>
          <AddressInput label={l.addressLabel} value={addr} onChange={setAddr} labels={l} />
          <button type="button" className="sqc-btn sqc-btn--primary sqc-btn--block" disabled={!canSave || !network} onClick={onSave}>
            {l.add}
          </button>
          {message ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {message}
            </p>
          ) : null}
          <p className="sqc-widget__hint">{l.stored}</p>
        </>
      ) : null}
    </WidgetShell>
  );
}
