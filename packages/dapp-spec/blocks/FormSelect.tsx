import { useEffect, useState } from 'react';
import { parseList } from './parseList';

export type FormSelectProps = {
  label?: string;
  /** Options, comma-separated (or one per line when the value contains a newline). */
  options?: string;
};

/** Labelled dropdown (local state only; the value is not wired to any action). */
export function FormSelect({ label = 'Choose', options = 'Option A,Option B,Option C' }: FormSelectProps) {
  const list = parseList(options);
  const [value, setValue] = useState(list[0] ?? '');
  // When the options change, keep the choice if it still exists, else fall back to the first option.
  useEffect(() => {
    setValue((v) => (list.includes(v) ? v : (list[0] ?? '')));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [options]);
  return (
    <label className="sqc-field" data-sqc-widget="form-select">
      <span className="sqc-widget__label">{label}</span>
      <select className="sqc-input" value={list.includes(value) ? value : (list[0] ?? '')} onChange={(e) => setValue(e.target.value)}>
        {list.map((o, i) => (
          <option key={`${i}-${o}`} value={o}>
            {o}
          </option>
        ))}
      </select>
    </label>
  );
}
