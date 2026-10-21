import { useState } from 'react';

export type FormSelectProps = {
  label?: string;
  /** Options, comma-separated. */
  options?: string;
};

/** Labelled dropdown (local state only). */
export function FormSelect({ label = 'Choose', options = 'Option A,Option B,Option C' }: FormSelectProps) {
  const list = options
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  const [value, setValue] = useState(list[0] ?? '');
  return (
    <label className="sqc-field" data-sqc-widget="form-select">
      <span className="sqc-widget__label">{label}</span>
      <select className="sqc-input" value={value} onChange={(e) => setValue(e.target.value)}>
        {list.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </label>
  );
}
