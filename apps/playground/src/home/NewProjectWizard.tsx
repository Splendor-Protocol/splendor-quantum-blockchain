import { useMemo, useState } from 'react';
import { THEME_PRESETS, getTemplate, kitTemplates, type DappTemplate, type SlotAnswers, type TemplateSlot } from '@splendor/dapp-spec';
import { ModalDialog } from '../components/ModalDialog';
import {
  MAX_PROJECT_NAME,
  answersFromDrafts,
  cleanProjectName,
  encodeAnswers,
  initialSlotDraft,
  toggleSlotDemo,
  type SlotDraft,
  type SlotErrors,
} from '../builder/newProject';
import { KitCards } from './KitCards';
import '../studio/preview-app.css';

export type WizardStep = 'kit' | 'setup' | 'theme' | 'name';
export type WizardResult = {
  template: string;
  preset: string;
  name: string;
  /** The kit's setup answers by slot id. Missing or empty: the kit keeps its demo values. */
  answers?: SlotAnswers;
};

const STEPS: { id: WizardStep; label: string }[] = [
  { id: 'kit', label: 'Kit' },
  { id: 'setup', label: 'Setup' },
  { id: 'theme', label: 'Theme' },
  { id: 'name', label: 'Name' },
];

/** Query string for `/build` that BuilderPage turns into the starting spec. */
export function wizardQuery(result: WizardResult, workspaceId?: string | null): string {
  const q = new URLSearchParams({ template: result.template, preset: result.preset });
  const name = cleanProjectName(result.name);
  if (name) q.set('name', name);
  const answers = encodeAnswers(result.answers);
  if (answers) q.set('answers', answers);
  if (workspaceId) q.set('workspace', workspaceId);
  return q.toString();
}

const MAX_ABI_FILE = 512 * 1024;

function SlotFields({
  slot,
  draft,
  errors,
  onChange,
}: {
  slot: TemplateSlot;
  draft: SlotDraft;
  errors: SlotErrors;
  onChange: (next: SlotDraft) => void;
}) {
  const id = `setup-${slot.id}`;
  const demoable = slot.kind === 'token' || slot.kind === 'contract';
  const noun = slot.kind === 'token' ? 'token' : slot.kind === 'contract' ? 'contract' : 'address';
  const [fileError, setFileError] = useState<string | null>(null);
  const readOnly = demoable && draft.useDemo;

  const upload = async (file: File | undefined) => {
    setFileError(null);
    if (!file) return;
    if (file.size > MAX_ABI_FILE) {
      setFileError('That file is too large for an ABI (512 KB max).');
      return;
    }
    try {
      onChange({ ...draft, abi: await file.text() });
    } catch {
      setFileError('Could not read that file.');
    }
  };

  return (
    <fieldset className="pg-setup-slot" data-testid={id}>
      <legend>
        {slot.label}
        {slot.required ? null : <span className="pg-muted"> (optional)</span>}
      </legend>
      {slot.help ? <p className="pg-muted pg-setup-help">{slot.help}</p> : null}

      {demoable ? (
        <label className="pg-check">
          <input type="checkbox" checked={draft.useDemo} data-testid={`${id}-demo`} onChange={(e) => onChange(toggleSlotDemo(slot, draft, e.target.checked))} />
          Use demo {noun}
        </label>
      ) : null}

      <div className="pg-field">
        <label htmlFor={`${id}-address`}>{slot.kind === 'text' ? slot.label : `${noun[0]!.toUpperCase()}${noun.slice(1)} address`}</label>
        <input
          id={`${id}-address`}
          data-testid={`${id}-address`}
          value={draft.address}
          readOnly={readOnly}
          disabled={readOnly}
          spellCheck={false}
          placeholder={slot.placeholder ?? 'SQ:\u2026'}
          aria-invalid={errors.address ? true : undefined}
          aria-describedby={errors.address ? `${id}-address-error` : undefined}
          onChange={(e) => onChange({ ...draft, address: e.target.value })}
        />
        {errors.address ? (
          <small id={`${id}-address-error`} className="pg-error" role="alert">
            {errors.address}
          </small>
        ) : null}
      </div>

      {slot.kind === 'contract' ? (
        <>
          <div className="pg-field">
            <label htmlFor={`${id}-name`}>Contract name (optional)</label>
            <input id={`${id}-name`} data-testid={`${id}-name`} value={draft.name} maxLength={80} placeholder="My contract" disabled={readOnly} onChange={(e) => onChange({ ...draft, name: e.target.value })} />
          </div>
          <div className="pg-field">
            <label htmlFor={`${id}-abi`}>ABI or function list</label>
            <textarea
              id={`${id}-abi`}
              data-testid={`${id}-abi`}
              rows={5}
              value={draft.abi}
              readOnly={readOnly}
              disabled={readOnly}
              spellCheck={false}
              placeholder={'sqc-solc ABI JSON, or one function per line:\nget() read\nset(uint256) write'}
              aria-invalid={errors.abi ? true : undefined}
              aria-describedby={errors.abi ? `${id}-abi-error` : undefined}
              onChange={(e) => onChange({ ...draft, abi: e.target.value })}
            />
            {errors.abi ? (
              <small id={`${id}-abi-error`} className="pg-error" role="alert">
                {errors.abi}
              </small>
            ) : null}
          </div>
          <div className="pg-field">
            <label htmlFor={`${id}-file`}>Or upload an ABI file (.json)</label>
            <input id={`${id}-file`} data-testid={`${id}-file`} type="file" accept=".json,application/json" disabled={readOnly} onChange={(e) => void upload(e.target.files?.[0])} />
            {fileError ? (
              <small className="pg-error" role="alert">
                {fileError}
              </small>
            ) : null}
          </div>
        </>
      ) : null}
    </fieldset>
  );
}

/**
 * New project: kit -> setup -> theme preset -> name. `onCreate` gets the choices; the caller navigates.
 * `initialTemplate` skips ahead to the kit's setup (or, for templates without questions, the theme).
 */
export function NewProjectWizard({
  templates,
  workspaceName,
  initialTemplate,
  onCreate,
  onCancel,
}: {
  templates: readonly DappTemplate[];
  /** Shown in the header when the project will be filed under a workspace. */
  workspaceName?: string | null;
  initialTemplate?: string | null;
  onCreate: (result: WizardResult) => void;
  onCancel: () => void;
}) {
  const lookup = (id: string) => (id && id !== 'blank' ? (templates.find((t) => t.id === id) ?? getTemplate(id)) : undefined);
  const [template, setTemplate] = useState<string>(initialTemplate ?? '');
  const [step, setStep] = useState<WizardStep>(() => {
    const t = initialTemplate ? lookup(initialTemplate) : undefined;
    if (!initialTemplate) return 'kit';
    return t?.slots?.length ? 'setup' : 'theme';
  });
  const [preset, setPreset] = useState<string>('splendor');
  const [name, setName] = useState('');
  const [drafts, setDrafts] = useState<Record<string, SlotDraft>>({});
  const [errors, setErrors] = useState<Record<string, SlotErrors>>({});

  const tpl = lookup(template);
  const slots = useMemo(() => tpl?.slots ?? [], [tpl]);
  const hasSetup = slots.length > 0;
  const kits = useMemo(() => {
    const own = templates.filter((t) => t.kit);
    return own.length ? own : kitTemplates();
  }, [templates]);
  const others = useMemo(() => templates.filter((t) => !t.kit), [templates]);

  const draftFor = (slot: TemplateSlot): SlotDraft => drafts[slot.id] ?? initialSlotDraft(slot);
  const checked = answersFromDrafts(slots, Object.fromEntries(slots.map((s) => [s.id, draftFor(s)])));
  const contractAnswer = checked.answers.contract;
  const kitName = typeof contractAnswer === 'object' ? contractAnswer.name : undefined;
  const suggested = kitName ?? tpl?.spec.name ?? 'My Splendor dApp';
  const cleaned = cleanProjectName(name);

  // The Setup step only shows for kits with questions; the indicator drops it for the others.
  const steps = template && !hasSetup ? STEPS.filter((s) => s.id !== 'setup') : STEPS;
  const index = steps.findIndex((s) => s.id === step);

  const pickTemplate = (id: string) => {
    if (id !== template) {
      setDrafts({});
      setErrors({});
    }
    setTemplate(id);
  };

  const next = () => {
    if (step === 'kit' && template) setStep(hasSetup ? 'setup' : 'theme');
    else if (step === 'setup') {
      if (!checked.ok) {
        setErrors(checked.errors);
        return;
      }
      setErrors({});
      setStep('theme');
    } else if (step === 'theme') setStep('name');
  };
  const back = () => {
    if (step === 'name') setStep('theme');
    else if (step === 'theme') setStep(hasSetup ? 'setup' : 'kit');
    else if (step === 'setup') setStep('kit');
  };
  const create = () =>
    onCreate({
      template: template || 'blank',
      preset,
      name: cleaned || suggested,
      ...(Object.keys(checked.answers).length ? { answers: checked.answers } : {}),
    });

  return (
    <ModalDialog label="New project" onClose={onCancel} className="pg-modal" testId="wizard">
      <form
        className={`pg-modal-panel${step === 'kit' || step === 'setup' ? ' pg-modal-panel-wide' : ''}`}
        onSubmit={(e) => {
          e.preventDefault();
          if (step === 'name') create();
          else next();
        }}
      >
        <h3 style={{ margin: 0 }}>New project{workspaceName ? ` in ${workspaceName}` : ''}</h3>
        <ol className="pg-wizard-steps" aria-label="Steps">
          {steps.map((s, i) => (
            <li key={s.id} className={i === index ? 'is-active' : i < index ? 'is-done' : ''} aria-current={i === index ? 'step' : undefined}>
              {i + 1}. {s.label}
            </li>
          ))}
        </ol>

        {step === 'kit' ? (
          <div data-testid="wizard-templates">
            <KitCards kits={kits} others={others} includeBlank othersOpen selected={template || null} testIdPrefix="wizard-template" onPick={pickTemplate} />
          </div>
        ) : null}

        {step === 'setup' ? (
          <div className="pg-stack" data-testid="wizard-setup">
            <p className="pg-muted" style={{ margin: 0 }}>
              Point {tpl?.title ?? 'the app'} at your own contracts, or keep the demo ones to try it first. You can change all of this in the studio.
            </p>
            {slots.map((slot) => (
              <SlotFields
                key={slot.id}
                slot={slot}
                draft={draftFor(slot)}
                errors={errors[slot.id] ?? {}}
                onChange={(d) => {
                  setDrafts((prev) => ({ ...prev, [slot.id]: d }));
                  if (errors[slot.id]) setErrors((prev) => ({ ...prev, [slot.id]: {} }));
                }}
              />
            ))}
          </div>
        ) : null}

        {step === 'theme' ? (
          <>
            <p className="pg-muted" style={{ margin: 0 }}>
              Pick a look. You can change it any time in the studio.
            </p>
            <div className="pg-theme-swatches" data-testid="wizard-themes">
              {THEME_PRESETS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  className={`pg-theme-swatch${preset === p.id ? ' is-active' : ''}`}
                  aria-pressed={preset === p.id}
                  title={p.description}
                  data-testid={`wizard-theme-${p.id}`}
                  onClick={() => setPreset(p.id)}
                  style={{ background: p.overrides.bg ?? '#12151d', borderColor: p.overrides.accent ?? '#7c5cff' }}
                >
                  <span style={{ color: p.overrides.accent ?? '#7c5cff' }}>{p.title}</span>
                </button>
              ))}
            </div>
          </>
        ) : null}

        {step === 'name' ? (
          <div className="pg-field">
            <label htmlFor="wizard-name">Project name</label>
            <input
              id="wizard-name"
              data-testid="wizard-name"
              autoFocus
              maxLength={MAX_PROJECT_NAME}
              placeholder={suggested}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <small className="pg-muted">
              {tpl ? tpl.title : 'Blank page'} {'\u00B7'} {THEME_PRESETS.find((p) => p.id === preset)?.title ?? preset} theme. Leave empty to use &ldquo;{suggested}&rdquo;.
            </small>
          </div>
        ) : null}

        <div className="pg-row">
          <button type="button" className="pg-btn pg-btn-ghost" onClick={onCancel}>
            Cancel
          </button>
          <span className="pg-spacer" />
          {step !== 'kit' ? (
            <button type="button" className="pg-btn pg-btn-ghost" data-testid="wizard-back" onClick={back}>
              Back
            </button>
          ) : null}
          {step === 'name' ? (
            <button type="submit" className="pg-btn" data-testid="wizard-create">
              Create project
            </button>
          ) : (
            <button type="submit" className="pg-btn" data-testid="wizard-next" disabled={step === 'kit' && !template}>
              Next
            </button>
          )}
        </div>
      </form>
    </ModalDialog>
  );
}
