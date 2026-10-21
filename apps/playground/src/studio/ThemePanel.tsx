import { THEME_PRESETS, applyThemePreset, type DappSpec } from '@splendor/dapp-spec';
import type { Dispatch } from 'react';
import { ThemeEditor } from '../components/ThemeEditor';
import type { BuilderAction } from '../builder/state';

export function ThemePanel({ spec, dispatch }: { spec: DappSpec; dispatch: Dispatch<BuilderAction> }) {
  const activePreset = spec.theme.preset ?? spec.theme.base;
  return (
    <div className="pg-stack">
      <p className="pg-toybox-lead">Theme presets</p>
      <div className="pg-theme-swatches">
        {THEME_PRESETS.map((p) => {
          const accent = p.overrides.accent ?? '#7c5cff';
          const bg = p.overrides.bg ?? '#12151d';
          return (
            <button
              key={p.id}
              type="button"
              className={`pg-theme-swatch${activePreset === p.id ? ' is-active' : ''}`}
              aria-pressed={activePreset === p.id}
              title={p.description}
              onClick={() => {
                const next = applyThemePreset(p.id, spec.theme);
                if (next) dispatch({ type: 'setTheme', theme: next });
              }}
            >
              <span className="pg-theme-swatch-colors" aria-hidden="true">
                <span style={{ background: bg }} />
                <span style={{ background: accent }} />
              </span>
              <span className="pg-theme-swatch-label">{p.title}</span>
            </button>
          );
        })}
      </div>
      <ThemeEditor value={spec.theme} onChange={(theme) => dispatch({ type: 'setTheme', theme })} />
    </div>
  );
}
