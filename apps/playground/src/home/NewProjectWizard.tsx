import { useState } from 'react';
import { THEME_PRESETS, getTemplate, type DappTemplate } from '@splendor/dapp-spec';
import { MAX_PROJECT_NAME, cleanProjectName } from '../builder/newProject';

export type WizardStep = 'template' | 'theme' | 'name';
export type WizardResult = { template: string; preset: string; name: string };

const STEPS: { id: WizardStep; label: string }[] = [
  { id: 'template', label: 'Template' },
  { id: 'theme', label: 'Theme' },
  { id: 'name', label: 'Name' },
];

/** Query string for `/build` that BuilderPage turns into the starting spec. */
export function wizardQuery(result: WizardResult, workspaceId?: string | null): string {
  const q = new URLSearchParams({ template: result.template, preset: result.preset });
  const name = cleanProjectName(result.name);
  if (name) q.set('name', name);
  if (workspaceId) q.set('workspace', workspaceId);
  return q.toString();
}

/**
 * New project: template -> theme preset -> name. `onCreate` gets the choices; the caller navigates.
 * `initialTemplate` skips ahead to the theme step.
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
  const [step, setStep] = useState<WizardStep>(initialTemplate ? 'theme' : 'template');
  const [template, setTemplate] = useState<string>(initialTemplate ?? '');
  const [preset, setPreset] = useState<string>('splendor');
  const [name, setName] = useState('');

  const tpl = template && template !== 'blank' ? (templates.find((t) => t.id === template) ?? getTemplate(template)) : undefined;
  const suggested = tpl?.spec.name ?? 'My Splendor dApp';
  const index = STEPS.findIndex((s) => s.id === step);
  const cleaned = cleanProjectName(name);

  const next = () => {
    if (step === 'template' && template) setStep('theme');
    else if (step === 'theme') setStep('name');
  };
  const back = () => {
    if (step === 'name') setStep('theme');
    else if (step === 'theme') setStep('template');
  };
  const create = () => onCreate({ template: template || 'blank', preset, name: cleaned || suggested });

  return (
    <div className="pg-modal" role="dialog" aria-modal="true" aria-label="New project" data-testid="wizard">
      <form
        className="pg-modal-panel"
        onSubmit={(e) => {
          e.preventDefault();
          if (step === 'name') create();
          else next();
        }}
      >
        <h3 style={{ margin: 0 }}>New project{workspaceName ? ` in ${workspaceName}` : ''}</h3>
        <ol className="pg-wizard-steps" aria-label="Steps">
          {STEPS.map((s, i) => (
            <li key={s.id} className={i === index ? 'is-active' : i < index ? 'is-done' : ''} aria-current={i === index ? 'step' : undefined}>
              {i + 1}. {s.label}
            </li>
          ))}
        </ol>

        {step === 'template' ? (
          <div className="pg-cards" data-testid="wizard-templates">
            {[...templates, null].map((t) => {
              const id = t?.id ?? 'blank';
              return (
                <button
                  key={id}
                  type="button"
                  className={`pg-card${template === id ? ' is-selected' : ''}`}
                  style={{ textAlign: 'left', font: 'inherit', cursor: 'pointer' }}
                  aria-pressed={template === id}
                  data-testid={`wizard-template-${id}`}
                  onClick={() => setTemplate(id)}
                >
                  <h3>{t?.title ?? 'Blank page'}</h3>
                  <p>{t?.description ?? 'A heading, then an empty page.'}</p>
                </button>
              );
            })}
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
          {step !== 'template' ? (
            <button type="button" className="pg-btn pg-btn-ghost" data-testid="wizard-back" onClick={back}>
              Back
            </button>
          ) : null}
          {step === 'name' ? (
            <button type="submit" className="pg-btn" data-testid="wizard-create">
              Create project
            </button>
          ) : (
            <button type="submit" className="pg-btn" data-testid="wizard-next" disabled={step === 'template' && !template}>
              Next
            </button>
          )}
        </div>
      </form>
    </div>
  );
}
