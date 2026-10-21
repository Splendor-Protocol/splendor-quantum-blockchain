import { useEffect, useState } from 'react';
import type { StudioRail } from './StudioShell';

/** localStorage flag: set once the tour was finished or skipped. */
export const ONBOARDING_KEY = 'sqc-studio-onboarded';
/** Automation (Playwright sets `navigator.webdriver`) skips the tour unless this is `1`. */
export const ONBOARDING_FORCE_KEY = 'sqc-studio-onboarding-force';

type Step = { title: string; text: string; target: string; rail?: StudioRail };

export const ONBOARDING_STEPS: readonly Step[] = [
  {
    title: '1. Pick pieces',
    text: 'The left rail switches between pages, components, theme, contracts and flows. Click or drag a component onto the page.',
    target: '.pg-rail-icons',
    rail: 'components',
  },
  {
    title: '2. Edit on the page',
    text: 'Click anything in the live preview to select it. Drag it to rearrange, or use the handles to duplicate and remove.',
    target: '.pg-studio-canvas',
  },
  {
    title: '3. Tune it, then export',
    text: 'The inspector on the right holds props, style and actions for the selected piece. When you are happy, use Export for a Vite or Next.js project.',
    target: '.pg-studio-inspector',
  },
];

function readFlag(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

/** True when the tour should open: not finished before, and not under browser automation. */
export function shouldShowOnboarding(): boolean {
  if (readFlag(ONBOARDING_KEY)) return false;
  if (typeof navigator !== 'undefined' && navigator.webdriver && readFlag(ONBOARDING_FORCE_KEY) !== '1') return false;
  return true;
}

export function markOnboarded(): void {
  try {
    localStorage.setItem(ONBOARDING_KEY, '1');
  } catch {
    /* storage disabled: the tour may show again next time */
  }
}

/**
 * Three coach marks over the studio. They are a small card plus an outline on the area they
 * talk about: nothing is dimmed or blocked, so the studio stays usable while the tour is open.
 */
export function Onboarding({ onRailChange }: { onRailChange?: (rail: StudioRail) => void }) {
  const [open, setOpen] = useState(shouldShowOnboarding);
  const [index, setIndex] = useState(0);
  const step = ONBOARDING_STEPS[index]!;

  useEffect(() => {
    if (!open) return;
    if (step.rail) onRailChange?.(step.rail);
    const el = document.querySelector(step.target);
    el?.classList.add('pg-coach-target');
    return () => el?.classList.remove('pg-coach-target');
    // `onRailChange` is a state setter; re-running on its identity would only repeat the same call.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, index]);

  if (!open) return null;

  const finish = () => {
    markOnboarded();
    setOpen(false);
  };
  const last = index === ONBOARDING_STEPS.length - 1;

  return (
    <div className="pg-coach" role="dialog" aria-label="Studio tour" aria-live="polite" data-testid="onboarding">
      <p className="pg-coach-step pg-muted" data-testid="onboarding-step">
        Step {index + 1} of {ONBOARDING_STEPS.length}
      </p>
      <strong>{step.title}</strong>
      <p style={{ margin: 0 }}>{step.text}</p>
      <div className="pg-row">
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="onboarding-skip" onClick={finish}>
          Skip
        </button>
        <span className="pg-spacer" />
        {index > 0 ? (
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setIndex(index - 1)}>
            Back
          </button>
        ) : null}
        <button type="button" className="pg-btn pg-btn-sm" data-testid="onboarding-next" onClick={() => (last ? finish() : setIndex(index + 1))}>
          {last ? 'Done' : 'Next'}
        </button>
      </div>
    </div>
  );
}
