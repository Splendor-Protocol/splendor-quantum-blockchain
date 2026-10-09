import type { ThemeName, ThemeOverrides } from '@splendor/dapp-spec';

const COLOR_KEYS: { key: keyof ThemeOverrides; label: string }[] = [
  { key: 'accent', label: 'Accent' },
  { key: 'accentHover', label: 'Accent hover' },
  { key: 'bg', label: 'Background' },
  { key: 'surface', label: 'Surface' },
  { key: 'text', label: 'Text' },
  { key: 'textMuted', label: 'Muted text' },
  { key: 'border', label: 'Border' },
  { key: 'danger', label: 'Danger' },
  { key: 'success', label: 'Success' },
];
const TEXT_KEYS: { key: keyof ThemeOverrides; label: string; placeholder: string }[] = [
  { key: 'radius', label: 'Corner radius', placeholder: '12px' },
  { key: 'widgetPadding', label: 'Widget padding', placeholder: '1rem 1.25rem' },
  { key: 'fontBody', label: 'Body font', placeholder: 'Inter, system-ui, sans-serif' },
  { key: 'fontDisplay', label: 'Heading font', placeholder: 'Inter, sans-serif' },
  { key: 'fontMono', label: 'Mono font', placeholder: 'ui-monospace, monospace' },
];

export type ThemeValue = { base: ThemeName; overrides: ThemeOverrides };

const HEX = /^#[0-9a-fA-F]{6}$/;

export function ThemeEditor({ value, onChange }: { value: ThemeValue; onChange: (v: ThemeValue) => void }) {
  const set = (key: keyof ThemeOverrides, v: string) => {
    const overrides = { ...value.overrides };
    if (v.trim()) overrides[key] = v;
    else delete overrides[key];
    onChange({ ...value, overrides });
  };
  return (
    <div className="pg-form">
      <div className="pg-field">
        <label htmlFor="theme-base">Base theme</label>
        <select id="theme-base" value={value.base} onChange={(e) => onChange({ ...value, base: e.target.value as ThemeName })}>
          <option value="splendor">Splendor</option>
          <option value="dark">Dark</option>
          <option value="light">Light</option>
        </select>
      </div>
      <div className="pg-theme-grid">
        {COLOR_KEYS.map(({ key, label }) => {
          const v = value.overrides[key] ?? '';
          return (
            <div key={key} className="pg-field">
              <label htmlFor={`theme-${key}`}>{label}</label>
              <div className="pg-row">
                <input
                  type="color"
                  aria-label={`${label} color picker`}
                  value={HEX.test(v) ? v : '#7c5cff'}
                  onChange={(e) => set(key, e.target.value)}
                />
                <input id={`theme-${key}`} type="text" className="pg-mono" value={v} placeholder="default" onChange={(e) => set(key, e.target.value)} />
              </div>
            </div>
          );
        })}
      </div>
      {TEXT_KEYS.map(({ key, label, placeholder }) => (
        <div key={key} className="pg-field">
          <label htmlFor={`theme-${key}`}>{label}</label>
          <input id={`theme-${key}`} type="text" value={value.overrides[key] ?? ''} placeholder={placeholder} onChange={(e) => set(key, e.target.value)} />
        </div>
      ))}
      {Object.keys(value.overrides).length ? (
        <button type="button" className="pg-btn pg-btn-ghost" onClick={() => onChange({ ...value, overrides: {} })}>
          Reset overrides
        </button>
      ) : null}
    </div>
  );
}
