import { describe, expect, it } from 'vitest';
import { DEMO_CONTRACT_ADDRESS, DEMO_TOKEN_ADDRESS, THEME_PRESETS, getTemplate, validateSpec } from '@splendor/dapp-spec';
import {
  answerFromDraft,
  answersFromDrafts,
  cleanProjectName,
  decodeAnswers,
  encodeAnswers,
  initialSlotDraft,
  specFromWizard,
  toggleSlotDemo,
  MAX_PROJECT_NAME,
} from './newProject';
import { wizardQuery } from '../home/NewProjectWizard';
import { parseAbiInput } from '../studio/abi';

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

const ADDR = `SQ:${'ab'.repeat(32)}`;
const kit = (id: string) => getTemplate(id)!;
const slot = (id: string, slotId: string) => kit(id).slots!.find((s) => s.id === slotId)!;

describe('specFromWizard with kit answers', () => {
  it('puts the token address on the token contract and the raw token target', () => {
    const spec = specFromWizard({ template: 'token-project', answers: { token: ADDR } })!;
    expect(spec.contracts!.find((c) => c.id === 'token')!.address).toBe(ADDR);
    const json = JSON.stringify(spec);
    expect(json).toContain(`"tokenAddress":"${ADDR}"`);
    expect(validateSpec(spec).ok).toBe(true);
  });

  it('builds one contract-form per write function of the pasted ABI', () => {
    const parsed = parseAbiInput('get() read\nstake(uint256 amount) write\nclaim() write');
    if ('error' in parsed) throw new Error(parsed.error);
    const spec = specFromWizard({ template: 'contract-app', answers: { contract: { address: ADDR, name: 'Staking', functions: parsed.functions } }, name: '' })!;
    expect(spec.contracts![0]!.address).toBe(ADDR);
    expect(spec.name).toBe('Staking');
    const forms = spec.pages.flatMap((p) => p.blocks ?? []).filter((b) => b.widget === 'contract-form');
    expect(forms.map((b) => b.props.functionSig)).toEqual(['stake(uint256)', 'claim()']);
    expect(validateSpec(spec).ok).toBe(true);
  });

  it('keeps the demo values with no answers', () => {
    const spec = specFromWizard({ template: 'token-project', answers: {} })!;
    expect(spec.contracts!.find((c) => c.id === 'token')!.address).toBe(DEMO_TOKEN_ADDRESS);
    expect(spec.pages.map((p) => p.title)).toEqual(['Home', 'Dashboard']);
  });
});

describe('kit setup drafts', () => {
  it('starts token and contract slots on the demo values and accepts them as "no answer"', () => {
    const token = initialSlotDraft(slot('token-project', 'token'));
    expect(token).toMatchObject({ useDemo: true, address: DEMO_TOKEN_ADDRESS });
    expect(answerFromDraft(slot('token-project', 'token'), token)).toEqual({ errors: {} });
    const contract = initialSlotDraft(slot('contract-app', 'contract'));
    expect(contract).toMatchObject({ useDemo: true, address: DEMO_CONTRACT_ADDRESS });
    expect(parseAbiInput(contract.abi)).toHaveProperty('functions');
    expect(answerFromDraft(slot('contract-app', 'contract'), contract)).toEqual({ errors: {} });
  });

  it('validates a real token address', () => {
    const s = slot('token-project', 'token');
    const off = toggleSlotDemo(s, initialSlotDraft(s), false);
    expect(off.address).toBe('');
    expect(answerFromDraft(s, off).errors.address).toMatch(/Enter your token address/);
    expect(answerFromDraft(s, { ...off, address: 'SQ:12' }).errors.address).toMatch(/SQ:/);
    expect(answerFromDraft(s, { ...off, address: ` ${ADDR} ` })).toEqual({ answer: ADDR, errors: {} });
  });

  it('wants an address and a parseable ABI for a real contract', () => {
    const s = slot('contract-app', 'contract');
    const off = toggleSlotDemo(s, initialSlotDraft(s), false);
    expect(answerFromDraft(s, off).errors.address).toBeTruthy();
    expect(answerFromDraft(s, { ...off, address: ADDR }).errors.abi).toMatch(/ABI/);
    expect(answerFromDraft(s, { ...off, address: ADDR, abi: '{nope' }).errors.abi).toMatch(/Not valid JSON/);
    const r = answerFromDraft(s, { ...off, address: ADDR, abi: 'get() read\nset(uint256) write', name: ' Counter ' });
    expect(r.errors).toEqual({});
    expect(r.answer).toMatchObject({ address: ADDR, name: 'Counter' });
    expect((r.answer as { functions: unknown[] }).functions).toHaveLength(2);
  });

  it('lets an optional slot be skipped, but not half filled', () => {
    const s = slot('token-project', 'contract');
    expect(s.required).toBeFalsy();
    const off = toggleSlotDemo(s, initialSlotDraft(s), false);
    expect(answerFromDraft(s, off)).toEqual({ errors: {} });
    expect(answerFromDraft(s, { ...off, abi: 'get() read' }).errors.address).toMatch(/address/);
  });

  it('collects every slot, and reports every broken one', () => {
    const slots = kit('token-project').slots!;
    const good = answersFromDrafts(slots, { token: { ...initialSlotDraft(slots[0]!), useDemo: false, address: ADDR } });
    expect(good).toMatchObject({ ok: true, answers: { token: ADDR } });
    const bad = answersFromDrafts(slots, { token: { ...initialSlotDraft(slots[0]!), useDemo: false, address: 'nope' } });
    expect(bad.ok).toBe(false);
    expect(Object.keys(bad.errors)).toEqual(['token']);
  });
});

describe('answers in the /build query', () => {
  const answers = { token: ADDR, contract: { address: ADDR, name: 'Staking', functions: [{ name: 'get', signature: 'get()', params: [], kind: 'read' as const }] } };

  it('round-trips through wizardQuery and decodeAnswers', () => {
    const q = new URLSearchParams(wizardQuery({ template: 'contract-app', preset: 'mint', name: 'X', answers }));
    expect(decodeAnswers(q.get('answers'))).toEqual(answers);
  });

  it('omits the param when there is nothing to carry', () => {
    expect(encodeAnswers({})).toBe('');
    expect(new URLSearchParams(wizardQuery({ template: 'tip-jar', preset: 'mint', name: '', answers: {} })).has('answers')).toBe(false);
  });

  it('treats the query as untrusted', () => {
    expect(decodeAnswers(null)).toEqual({});
    expect(decodeAnswers('{nope')).toEqual({});
    expect(decodeAnswers('[1,2]')).toEqual({});
    expect(decodeAnswers('x'.repeat(100_000))).toEqual({});
    expect(
      decodeAnswers(JSON.stringify({ 'bad id!': 'x', token: 5, contract: { address: ADDR, functions: [{ name: 'evil', signature: 'not a signature', kind: 'read' }] }, text: 'y'.repeat(500) })),
    ).toEqual({ contract: { address: ADDR }, text: 'y'.repeat(200) });
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
