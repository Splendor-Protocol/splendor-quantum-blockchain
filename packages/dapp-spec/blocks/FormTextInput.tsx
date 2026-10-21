import { useState } from 'react';

export type FormTextInputProps = {
  label?: string;
  placeholder?: string;
  /** Allow several lines. */
  multiline?: boolean;
};

/** Labelled text field (local state only; style it, or copy the file to wire it up). */
export function FormTextInput({ label = 'Text', placeholder = '', multiline = false }: FormTextInputProps) {
  const [value, setValue] = useState('');
  return (
    <label className="sqc-field" data-sqc-widget="form-text">
      <span className="sqc-widget__label">{label}</span>
      {multiline ? (
        <textarea className="sqc-input" rows={4} value={value} placeholder={placeholder} onChange={(e) => setValue(e.target.value)} />
      ) : (
        <input className="sqc-input" value={value} placeholder={placeholder} onChange={(e) => setValue(e.target.value)} />
      )}
    </label>
  );
}
