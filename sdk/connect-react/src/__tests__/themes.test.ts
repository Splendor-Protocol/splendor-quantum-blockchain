import { describe, expect, it } from 'vitest';
import { resolveThemeAttr, themeOverridesToStyle } from '../theme/themes.js';

describe('themes', () => {
  it('resolves every built-in theme name, including auto', () => {
    for (const name of ['splendor', 'light', 'dark', 'auto'] as const) expect(resolveThemeAttr(name)).toBe(name);
    expect(resolveThemeAttr({ base: 'auto' })).toBe('auto');
    expect(resolveThemeAttr({ base: 'nope' as never })).toBe('splendor');
    expect(resolveThemeAttr(undefined)).toBe('splendor');
  });

  it('a custom accent also drives link text unless accentText is set', () => {
    expect(themeOverridesToStyle({ accent: '#ff0066' })).toMatchObject({ '--sqc-accent': '#ff0066', '--sqc-accent-text': '#ff0066' });
    expect(themeOverridesToStyle({ accent: '#ffd400', accentText: '#7a6500' })).toMatchObject({
      '--sqc-accent': '#ffd400',
      '--sqc-accent-text': '#7a6500',
    });
    expect(themeOverridesToStyle({ base: 'light' })).toEqual({});
  });
});
