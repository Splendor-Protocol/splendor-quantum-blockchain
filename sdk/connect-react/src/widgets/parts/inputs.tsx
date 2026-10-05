import { useId, type ReactNode } from 'react';
import { compareDecimal, fractionDigits, isPositiveAmount, normalizeSqAddress, truncateAddress } from '../utils.js';
import { COMMON_LABELS, formatLabel, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';

function FieldError({ id, message }: { id: string; message: string | null }) {
  if (!message) return null;
  return (
    <p id={id} className="sqc-widget__msg sqc-widget__msg--error" role="alert">
      {message}
    </p>
  );
}

export function RecentRecipients({
  recipients,
  onPick,
  labels,
}: {
  recipients: string[];
  onPick: (address: string) => void;
  labels?: Partial<Pick<CommonWidgetLabels, 'recent' | 'recentRecipients'>>;
}) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  if (recipients.length === 0) return null;
  return (
    <div className="sqc-recent" aria-label={l.recentRecipients}>
      <span className="sqc-widget__hint">{l.recent}</span>
      {recipients.map((addr) => (
        <button key={addr} type="button" className="sqc-copy" title={addr} onClick={() => onPick(addr)}>
          {truncateAddress(addr, 12, 10)}
        </button>
      ))}
    </div>
  );
}

/** Validation message for an `SQ:` recipient field, or null when empty or valid. */
export function addressFieldError(
  value: string,
  self?: string | null,
  labels: Pick<CommonWidgetLabels, 'invalidAddress' | 'selfSend'> = COMMON_LABELS,
): string | null {
  if (!value.trim()) return null;
  const addr = normalizeSqAddress(value);
  if (!addr) return labels.invalidAddress;
  const me = normalizeSqAddress(self);
  if (me && me === addr) return labels.selfSend;
  return null;
}

/** Validation message for an amount field, or null when empty or valid. */
export function amountFieldError(
  value: string,
  balance?: string | null,
  symbol = 'SPLD',
  labels: Pick<CommonWidgetLabels, 'badAmount' | 'insufficient'> & Partial<Pick<CommonWidgetLabels, 'tooPrecise'>> = COMMON_LABELS,
  decimals?: number,
): string | null {
  if (!value.trim()) return null;
  if (!isPositiveAmount(value)) return labels.badAmount;
  if (decimals != null && fractionDigits(value.trim()) > decimals) {
    return formatLabel(labels.tooPrecise ?? COMMON_LABELS.tooPrecise, { decimals });
  }
  if (balance != null && compareDecimal(value, balance) === 1) {
    return formatLabel(labels.insufficient, { symbol });
  }
  return null;
}

export type AddressInputProps = {
  label: ReactNode;
  value: string;
  onChange: (value: string) => void;
  id?: string;
  placeholder?: string;
  /** Explicit error; when omitted the field validates itself (format and `self`). Pass null for no error. */
  error?: string | null;
  /** Connected address, used to block self-sends when validating itself. */
  self?: string | null;
  /** Recent recipients shown as quick picks under the field. */
  recents?: string[];
  labels?: Partial<CommonWidgetLabels>;
};

/** `SQ:` address field with inline validation and recent recipients. */
export function AddressInput({
  label,
  value,
  onChange,
  id,
  placeholder = 'SQ:…',
  error,
  self,
  recents,
  labels,
}: AddressInputProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  const autoId = useId();
  const inputId = id ?? `${autoId}-addr`;
  const errId = `${inputId}-error`;
  const message = error === undefined ? addressFieldError(value, self, l) : error;
  return (
    <div className="sqc-field">
      <label className="sqc-widget__label" htmlFor={inputId}>
        {label}
      </label>
      <input
        id={inputId}
        className="sqc-input"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        aria-invalid={!!message}
        aria-describedby={message ? errId : undefined}
      />
      {recents ? <RecentRecipients recipients={recents} onPick={onChange} labels={l} /> : null}
      <FieldError id={errId} message={message} />
    </div>
  );
}

export type AmountInputProps = {
  label: ReactNode;
  value: string;
  onChange: (value: string) => void;
  id?: string;
  placeholder?: string;
  /** Explicit error; when omitted the field validates itself (format and `balance`). Pass null for no error. */
  error?: string | null;
  /** Spendable balance in the same unit; enables Max and the over-balance check. */
  balance?: string | null;
  symbol?: string;
  /** Maximum fractional digits accepted (18 for SPLD, token decimals for QRC-20). */
  decimals?: number;
  labels?: Partial<CommonWidgetLabels>;
};

/** Decimal amount field with a Max button and inline validation. */
export function AmountInput({
  label,
  value,
  onChange,
  id,
  placeholder,
  error,
  balance,
  symbol = 'SPLD',
  decimals,
  labels,
}: AmountInputProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  const autoId = useId();
  const inputId = id ?? `${autoId}-amt`;
  const errId = `${inputId}-error`;
  const message = error === undefined ? amountFieldError(value, balance, symbol, l, decimals) : error;
  return (
    <div className="sqc-field">
      <label className="sqc-widget__label" htmlFor={inputId}>
        {label}
      </label>
      <div className="sqc-field__row">
        <input
          id={inputId}
          className="sqc-input"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          inputMode="decimal"
          autoComplete="off"
          aria-invalid={!!message}
          aria-describedby={message ? errId : undefined}
        />
        {balance != null ? (
          <button type="button" className="sqc-copy" onClick={() => onChange(balance)}>
            {l.max}
          </button>
        ) : null}
      </div>
      <FieldError id={errId} message={message} />
    </div>
  );
}
