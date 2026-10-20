import type { CSSProperties } from 'react';

export type ThemeName = 'splendor' | 'light' | 'dark';

export type ThemeOverrides = {
  /** Built-in theme the overrides apply on top of. Default `splendor`. */
  base?: ThemeName;
  accent?: string;
  accentHover?: string;
  bg?: string;
  surface?: string;
  text?: string;
  textMuted?: string;
  border?: string;
  danger?: string;
  success?: string;
  radius?: string;
  /** Inner padding of the drop-in widgets, e.g. "1rem 1.25rem". */
  widgetPadding?: string;
  fontDisplay?: string;
  fontBody?: string;
  fontMono?: string;
};

export type ThemeProp = ThemeName | ThemeOverrides;

const THEME_NAMES: readonly ThemeName[] = ['splendor', 'light', 'dark'];

export function resolveThemeAttr(theme: ThemeProp | undefined): ThemeName {
  if (!theme) return 'splendor';
  if (typeof theme === 'object') return theme.base && THEME_NAMES.includes(theme.base) ? theme.base : 'splendor';
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
  if (theme.border) style['--sqc-border'] = theme.border;
  if (theme.danger) style['--sqc-danger'] = theme.danger;
  if (theme.success) style['--sqc-success'] = theme.success;
  if (theme.radius) style['--sqc-radius'] = theme.radius;
  if (theme.widgetPadding) style['--sqc-widget-padding'] = theme.widgetPadding;
  if (theme.fontDisplay) style['--sqc-font-display'] = theme.fontDisplay;
  if (theme.fontBody) style['--sqc-font-body'] = theme.fontBody;
  if (theme.fontMono) style['--sqc-font-mono'] = theme.fontMono;
  return style as CSSProperties;
}
