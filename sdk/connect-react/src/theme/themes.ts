import type { CSSProperties } from 'react';

export type ThemeName = 'splendor' | 'light' | 'dark';

export type ThemeOverrides = {
  accent?: string;
  accentHover?: string;
  bg?: string;
  surface?: string;
  text?: string;
  textMuted?: string;
  radius?: string;
  fontDisplay?: string;
  fontBody?: string;
  fontMono?: string;
};

export type ThemeProp = ThemeName | ThemeOverrides;

export function resolveThemeAttr(theme: ThemeProp | undefined): ThemeName {
  if (!theme || typeof theme === 'object') return 'splendor';
  return theme;
}

export function themeOverridesToStyle(theme: ThemeProp | undefined): CSSProperties | undefined {
  if (!theme || typeof theme !== 'object') return undefined;
  const style: Record<string, string> = {};
  if (theme.accent) style['--sqc-accent'] = theme.accent;
  if (theme.accentHover) style['--sqc-accent-hover'] = theme.accentHover;
  if (theme.bg) style['--sqc-bg'] = theme.bg;
  if (theme.surface) style['--sqc-surface'] = theme.surface;
  if (theme.text) style['--sqc-text'] = theme.text;
  if (theme.textMuted) style['--sqc-text-muted'] = theme.textMuted;
  if (theme.radius) style['--sqc-radius'] = theme.radius;
  if (theme.fontDisplay) style['--sqc-font-display'] = theme.fontDisplay;
  if (theme.fontBody) style['--sqc-font-body'] = theme.fontBody;
  if (theme.fontMono) style['--sqc-font-mono'] = theme.fontMono;
  return style as CSSProperties;
}
