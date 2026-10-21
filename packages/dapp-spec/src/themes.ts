import type { ThemeName, ThemeOverrides } from './types.js';

export type ThemePreset = {
  id: string;
  title: string;
  description: string;
  base: ThemeName;
  overrides: ThemeOverrides;
};

/** Ready looks for the studio Theme panel. Safe CSS tokens only. */
export const THEME_PRESETS: readonly ThemePreset[] = [
  {
    id: 'splendor',
    title: 'Splendor',
    description: 'Default violet accent on deep slate.',
    base: 'splendor',
    overrides: {
      accent: '#7c5cff',
      accentHover: '#6d4cff',
      heroGradient: 'radial-gradient(ellipse at top, #1a1430 0%, transparent 60%)',
      radius: '12px',
    },
  },
  {
    id: 'midnight',
    title: 'Midnight',
    description: 'Near-black with ice blue accents.',
    base: 'dark',
    overrides: {
      bg: '#05070c',
      surface: '#0e121a',
      accent: '#5ec8ff',
      accentHover: '#3bb4f0',
      text: '#e8f1ff',
      textMuted: '#8a9bb0',
      border: '#1c2433',
      heroGradient: 'linear-gradient(160deg, #0a1628 0%, #05070c 55%)',
      radius: '10px',
      fontDisplay: 'Segoe UI, system-ui, sans-serif',
    },
  },
  {
    id: 'paper',
    title: 'Paper',
    description: 'Light surfaces with ink accents.',
    base: 'light',
    overrides: {
      bg: '#f6f4ef',
      surface: '#ffffff',
      accent: '#1f4b99',
      accentHover: '#163a78',
      text: '#1a1c1f',
      textMuted: '#5c6570',
      border: '#ddd6c8',
      heroGradient: 'linear-gradient(180deg, #efe8da 0%, #f6f4ef 70%)',
      radius: '14px',
      fontBody: 'Georgia, "Times New Roman", serif',
      fontDisplay: 'Georgia, serif',
    },
  },
  {
    id: 'mint',
    title: 'Mint',
    description: 'Fresh green on soft dark.',
    base: 'dark',
    overrides: {
      bg: '#0a1210',
      surface: '#101a17',
      accent: '#3ecf8e',
      accentHover: '#2fb978',
      text: '#e6f7ef',
      textMuted: '#8aab9a',
      border: '#1d2e28',
      success: '#3ecf8e',
      heroGradient: 'radial-gradient(circle at 20% 0%, #14352a 0%, transparent 50%)',
      radius: '16px',
    },
  },
  {
    id: 'sunset',
    title: 'Sunset',
    description: 'Warm coral accents.',
    base: 'dark',
    overrides: {
      bg: '#120c0a',
      surface: '#1c1410',
      accent: '#ff7a45',
      accentHover: '#e86530',
      text: '#fff1e8',
      textMuted: '#b89a8a',
      border: '#33241c',
      danger: '#ff6b6b',
      heroGradient: 'linear-gradient(135deg, #3a1a10 0%, #120c0a 55%)',
      radius: '12px',
    },
  },
  {
    id: 'carbon',
    title: 'Carbon',
    description: 'Monochrome studio look.',
    base: 'dark',
    overrides: {
      bg: '#0c0c0c',
      surface: '#161616',
      accent: '#e8e8e8',
      accentHover: '#ffffff',
      text: '#f2f2f2',
      textMuted: '#8f8f8f',
      border: '#2a2a2a',
      heroGradient: 'linear-gradient(180deg, #1a1a1a 0%, #0c0c0c 100%)',
      radius: '6px',
      fontMono: 'ui-monospace, Consolas, monospace',
    },
  },
  {
    id: 'ocean',
    title: 'Ocean',
    description: 'Deep teal with soft cyan.',
    base: 'dark',
    overrides: {
      bg: '#061218',
      surface: '#0c1c24',
      accent: '#2dd4bf',
      accentHover: '#14b8a6',
      text: '#e6fbff',
      textMuted: '#7aa8b5',
      border: '#16333d',
      heroGradient: 'radial-gradient(ellipse at bottom, #0e3a48 0%, transparent 55%)',
      radius: '18px',
    },
  },
  {
    id: 'neon',
    title: 'Neon',
    description: 'High-contrast magenta pop.',
    base: 'dark',
    overrides: {
      bg: '#09040f',
      surface: '#140a1c',
      accent: '#ff4fd8',
      accentHover: '#e63bc0',
      text: '#ffe8fb',
      textMuted: '#b088b0',
      border: '#2e1638',
      heroGradient: 'radial-gradient(circle at 80% 10%, #4a1060 0%, transparent 45%)',
      radius: '20px',
      fontDisplay: 'Impact, Haettenschweiler, sans-serif',
    },
  },
];

export function getThemePreset(id: string): ThemePreset | undefined {
  return THEME_PRESETS.find((p) => p.id === id);
}

/** Apply a preset onto a theme object (preset wins for overlapping keys). */
export function applyThemePreset(
  presetId: string,
  current?: { base: ThemeName; overrides: ThemeOverrides },
): { base: ThemeName; overrides: ThemeOverrides; preset: string } | null {
  const preset = getThemePreset(presetId);
  if (!preset) return null;
  return {
    base: preset.base,
    overrides: { ...(current?.overrides ?? {}), ...preset.overrides },
    preset: preset.id,
  };
}
