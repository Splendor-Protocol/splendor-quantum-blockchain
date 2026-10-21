import { describe, expect, it } from 'vitest';
import { THEME_PRESETS, validateSpec } from '@splendor/dapp-spec';
import { cleanProjectName, specFromWizard, MAX_PROJECT_NAME } from './newProject';
import { wizardQuery } from '../home/NewProjectWizard';

describe('specFromWizard', () => {
  it('builds a template spec with the chosen theme preset and name', () => {
    const spec = specFromWizard({ template: 'wallet-dashboard', preset: 'midnight', name: '  My   Wallet ' })!;
    const midnight = THEME_PRESETS.find((p) => p.id === 'midnight')!;
    expect(spec.name).toBe('My Wallet');
    expect(spec.nav?.brand).toBe('My Wallet');
    expect(spec.theme.preset).toBe('midnight');
    expect(spec.theme.base).toBe(midnight.base);
    expect(spec.theme.overrides.accent).toBe(midnight.overrides.accent);
    expect(validateSpec(spec).ok).toBe(true);
  });

  it('keeps the template name when no name is given, and ignores unknown presets', () => {
    const spec = specFromWizard({ template: 'tip-jar', preset: 'nope', name: '' })!;
    expect(spec.name).toBe('Tip Jar');
    expect(spec.theme.preset).toBe('splendor');
  });

  it('starts blank for "blank" or no template, and rejects unknown templates', () => {
    expect(specFromWizard({ template: 'blank', name: 'Blank one' })!.pages[0]!.path).toBe('/');
    expect(specFromWizard({})!.name).toBe('My Splendor dApp');
    expect(specFromWizard({ template: 'does-not-exist' })).toBeNull();
  });

  it('does not share state between calls', () => {
    const a = specFromWizard({ template: 'tip-jar', name: 'A' })!;
    const b = specFromWizard({ template: 'tip-jar', name: 'B' })!;
    a.theme.overrides.accent = '#000000';
    expect(b.theme.overrides.accent).not.toBe('#000000');
  });
});

describe('cleanProjectName', () => {
  it('collapses whitespace and caps the length', () => {
    expect(cleanProjectName('  a \n b  ')).toBe('a b');
    expect(cleanProjectName('x'.repeat(200))).toHaveLength(MAX_PROJECT_NAME);
    expect(cleanProjectName(null)).toBe('');
  });
});

describe('wizardQuery', () => {
  it('encodes template, preset, name and workspace for /build', () => {
    const q = new URLSearchParams(wizardQuery({ template: 'tip-jar', preset: 'mint', name: 'Coffee & Tips' }, 'local-abc'));
    expect(Object.fromEntries(q)).toEqual({ template: 'tip-jar', preset: 'mint', name: 'Coffee & Tips', workspace: 'local-abc' });
    expect(new URLSearchParams(wizardQuery({ template: 'blank', preset: 'splendor', name: '' })).has('name')).toBe(false);
  });
});
