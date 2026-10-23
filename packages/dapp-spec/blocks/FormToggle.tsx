import { useEffect, useState } from 'react';

export type FormToggleProps = {
  label?: string;
  /** Initial state; a new value replaces what the visitor toggled. */
  checked?: boolean;
};

/** Labelled on/off switch (local state only; the value is not wired to any action). */
export function FormToggle({ label = 'Enable', checked = false }: FormToggleProps) {
  const [on, setOn] = useState(checked);
  useEffect(() => setOn(checked), [checked]);
  return (
    <label className="sqc-field" data-sqc-widget="form-toggle" style={{ display: 'flex', alignItems: 'center', gap: '0.6rem', cursor: 'pointer' }}>
      <input type="checkbox" role="switch" checked={on} onChange={(e) => setOn(e.target.checked)} />
      <span className="sqc-widget__label">{label}</span>
    </label>
  );
}
