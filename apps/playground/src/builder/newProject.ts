import {
  DEMO_CONTRACT_ADDRESS,
  DEMO_TOKEN_ADDRESS,
  applyThemePreset,
  emptySpec,
  getTemplate,
  instantiateTemplate,
  type ContractFunction,
  type DappSpec,
  type SlotAnswer,
  type SlotAnswers,
  type TemplateSlot,
} from '@splendor/dapp-spec';
import { isValidAddress, parseAbiInput, parseFunctionsJson } from '../studio/abi';

export const MAX_PROJECT_NAME = 80;

export type WizardChoice = {
  /** Template id, or `blank`/undefined for an empty page. */
  template?: string | null;
  /** Theme preset id (see THEME_PRESETS). Unknown ids are ignored. */
  preset?: string | null;
  /** Project name. Empty keeps the template's own name. */
  name?: string | null;
  /** The kit's setup answers by slot id (token address, contract + ABI…). Empty keeps the demo values. */
  answers?: SlotAnswers | null;
};

/** Trim and cap a project name; empty in, empty out. */
export function cleanProjectName(name: string | null | undefined): string {
  return (name ?? '').replace(/\s+/g, ' ').trim().slice(0, MAX_PROJECT_NAME);
}

/**
 * Build the starting spec for the new-project wizard (kit -> setup -> theme preset -> name).
 * Returns null when `template` is set but unknown, so callers can fall back to the picker.
 */
export function specFromWizard(choice: WizardChoice): DappSpec | null {
  const wanted = choice.template && choice.template !== 'blank' ? choice.template : null;
  const tpl = wanted ? getTemplate(wanted) : undefined;
  if (wanted && !tpl) return null;
  const spec = tpl ? instantiateTemplate(tpl, choice.answers ?? {}) : emptySpec();
  const name = cleanProjectName(choice.name);
  if (name) {
    spec.name = name;
    spec.nav = { ...(spec.nav ?? { showConnect: true }), brand: name };
  }
  const applied = choice.preset ? applyThemePreset(choice.preset, spec.theme) : null;
  if (applied) spec.theme = applied;
  return spec;
}

// --- Kit setup (the wizard's Setup step) -------------------------------------------------------

/** What the demo contract's ABI looks like in the textarea. Matches the kit's built-in demo functions. */
export const DEMO_ABI_TEXT = 'get() read\nset(uint256 value) write\nincrement() write';

/** The wizard's inputs for one slot. */
export type SlotDraft = {
  /** Keep the kit's demo token / contract. Its fields are then read-only. */
  useDemo: boolean;
  /** Address (token / contract / address slots) or the text itself (text slots). */
  address: string;
  /** ABI JSON or one signature per line (contract slots). */
  abi: string;
  /** Display name for a contract. */
  name: string;
};

export type SlotErrors = { address?: string; abi?: string };

/** Starting inputs: token and contract slots begin on the demo values. */
export function initialSlotDraft(slot: TemplateSlot): SlotDraft {
  if (slot.kind === 'token') return { useDemo: true, address: DEMO_TOKEN_ADDRESS, abi: '', name: '' };
  if (slot.kind === 'contract') return { useDemo: true, address: DEMO_CONTRACT_ADDRESS, abi: DEMO_ABI_TEXT, name: '' };
  return { useDemo: false, address: '', abi: '', name: '' };
}

/** Switching the demo checkbox: demo values fill the fields, switching off empties them for the real ones. */
export function toggleSlotDemo(slot: TemplateSlot, draft: SlotDraft, useDemo: boolean): SlotDraft {
  if (useDemo) return { ...initialSlotDraft(slot), name: draft.name, useDemo: true };
  const demo = initialSlotDraft(slot);
  return {
    ...draft,
    useDemo: false,
    address: draft.address === demo.address ? '' : draft.address,
    abi: draft.abi === demo.abi ? '' : draft.abi,
  };
}

/**
 * Checks one slot's inputs. `answer` is undefined when the slot keeps the kit's own value (demo
 * chosen, or an optional slot left empty); `errors` is non-empty when the inputs cannot be used.
 */
export function answerFromDraft(slot: TemplateSlot, draft: SlotDraft): { answer?: SlotAnswer; errors: SlotErrors } {
  const required = slot.required === true;
  const value = draft.address.trim();

  if (slot.kind === 'text') {
    if (!value) return required ? { errors: { address: `${slot.label} is required.` } } : { errors: {} };
    return { answer: value.slice(0, 200), errors: {} };
  }

  if (draft.useDemo && (slot.kind === 'token' || slot.kind === 'contract')) return { errors: {} };

  const noun = slot.kind === 'token' ? 'token' : slot.kind === 'contract' ? 'contract' : 'address';
  if (!value) {
    // An optional slot left empty is skipped, unless an ABI was pasted without its address.
    if (slot.kind === 'contract' && draft.abi.trim()) return { errors: { address: 'Enter the contract address that goes with this ABI.' } };
    if (!required) return { errors: {} };
    return { errors: { address: `Enter your ${noun} address${slot.kind === 'address' ? '' : `, or use the demo ${noun}`}.` } };
  }
  if (!isValidAddress(value)) return { errors: { address: 'Enter an address like SQ: followed by 64 hex characters.' } };

  if (slot.kind !== 'contract') return { answer: value, errors: {} };

  if (!draft.abi.trim()) return { errors: { abi: 'Paste the ABI (or upload it) so the app knows what to read and write.' } };
  const parsed = parseAbiInput(draft.abi);
  if ('error' in parsed) return { errors: { abi: parsed.error } };
  const name = draft.name.trim().slice(0, 80);
  return { answer: { address: value, ...(name ? { name } : {}), functions: parsed.functions }, errors: {} };
}

/** Answers for every slot at once; `ok` is false when any slot has errors. */
export function answersFromDrafts(
  slots: readonly TemplateSlot[],
  drafts: Readonly<Record<string, SlotDraft>>,
): { ok: boolean; answers: Record<string, SlotAnswer>; errors: Record<string, SlotErrors> } {
  const answers: Record<string, SlotAnswer> = {};
  const errors: Record<string, SlotErrors> = {};
  for (const slot of slots) {
    const r = answerFromDraft(slot, drafts[slot.id] ?? initialSlotDraft(slot));
    if (r.answer !== undefined) answers[slot.id] = r.answer;
    if (r.errors.address || r.errors.abi) errors[slot.id] = r.errors;
  }
  return { ok: Object.keys(errors).length === 0, answers, errors };
}

// --- Carrying answers to /build ----------------------------------------------------------------

const MAX_ANSWERS_JSON = 60_000;
const MAX_ANSWER_FUNCTIONS = 60;

/** Answers as the `answers` query value for `/build`. Empty answers give an empty string. */
export function encodeAnswers(answers: SlotAnswers | null | undefined): string {
  const clean = Object.fromEntries(Object.entries(answers ?? {}).filter(([, v]) => v !== undefined && v !== ''));
  return Object.keys(clean).length ? JSON.stringify(clean) : '';
}

/**
 * Reads the `answers` query value back. It comes from a URL, so it is treated as untrusted: only
 * short strings and `{address, name?, functions?}` objects survive, and ABI functions are re-parsed
 * and re-validated rather than trusted.
 */
export function decodeAnswers(raw: string | null | undefined): SlotAnswers {
  if (!raw || raw.length > MAX_ANSWERS_JSON) return {};
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return {};
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return {};
  const out: Record<string, SlotAnswer> = {};
  for (const [id, value] of Object.entries(parsed as Record<string, unknown>).slice(0, 20)) {
    if (!/^[A-Za-z0-9_-]{1,40}$/.test(id)) continue;
    if (typeof value === 'string') {
      if (value.trim()) out[id] = value.slice(0, 200);
      continue;
    }
    if (typeof value !== 'object' || value === null || Array.isArray(value)) continue;
    const o = value as Record<string, unknown>;
    if (typeof o.address !== 'string' || !o.address.trim()) continue;
    let functions: ContractFunction[] | undefined;
    if (Array.isArray(o.functions) && o.functions.length) {
      const r = parseFunctionsJson(JSON.stringify(o.functions.slice(0, MAX_ANSWER_FUNCTIONS)));
      if (!r.error) functions = r.functions;
    }
    out[id] = {
      address: o.address.slice(0, 200),
      ...(typeof o.name === 'string' && o.name.trim() ? { name: o.name.trim().slice(0, 80) } : {}),
      ...(functions ? { functions } : {}),
    };
  }
  return out;
}
