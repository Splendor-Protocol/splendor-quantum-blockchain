import { applyThemePreset, emptySpec, getTemplate, instantiateTemplate, type DappSpec } from '@splendor/dapp-spec';

export const MAX_PROJECT_NAME = 80;

export type WizardChoice = {
  /** Template id, or `blank`/undefined for an empty page. */
  template?: string | null;
  /** Theme preset id (see THEME_PRESETS). Unknown ids are ignored. */
  preset?: string | null;
  /** Project name. Empty keeps the template's own name. */
  name?: string | null;
};

/** Trim and cap a project name; empty in, empty out. */
export function cleanProjectName(name: string | null | undefined): string {
  return (name ?? '').replace(/\s+/g, ' ').trim().slice(0, MAX_PROJECT_NAME);
}

/**
 * Build the starting spec for the new-project wizard (template -> theme preset -> name).
 * Returns null when `template` is set but unknown, so callers can fall back to the picker.
 */
export function specFromWizard(choice: WizardChoice): DappSpec | null {
  const wanted = choice.template && choice.template !== 'blank' ? choice.template : null;
  const tpl = wanted ? getTemplate(wanted) : undefined;
  if (wanted && !tpl) return null;
  const spec = tpl ? instantiateTemplate(tpl) : emptySpec();
  const name = cleanProjectName(choice.name);
  if (name) {
    spec.name = name;
    spec.nav = { ...(spec.nav ?? { showConnect: true }), brand: name };
  }
  const applied = choice.preset ? applyThemePreset(choice.preset, spec.theme) : null;
  if (applied) spec.theme = applied;
  return spec;
}
