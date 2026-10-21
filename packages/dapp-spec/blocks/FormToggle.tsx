import { useState } from 'react';

export type FormToggleProps = {
  label?: string;
  /** Initial state. */
  checked?: boolean;
};

/** Labelled on/off switch (local state only). */
export function FormToggle({ label = 'Enable', checked = false }: FormToggleProps) {
  const [on, setOn] = useState(checked);
  return (
    <label className="sqc-field" data-sqc-widget="form-toggle" style={{ display: 'flex', alignItems: 'center', gap: '0.6rem', cursor: 'pointer' }}>
      <input type="checkbox" role="switch" checked={on} onChange={(e) => setOn(e.target.checked)} />
      <span className="sqc-widget__label">{label}</span>
    </label>
  );
}
