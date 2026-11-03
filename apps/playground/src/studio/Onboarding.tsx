import { useEffect, useRef, useState } from 'react';
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

/** Same tour for narrow screens, where the panels are drawers. */
export const ONBOARDING_STEPS_NARROW: readonly Step[] = [
  { ...ONBOARDING_STEPS[0]!, text: 'Open Components to add pieces: tap one and it lands on the page (after the selected piece, if any).' },
  { ...ONBOARDING_STEPS[1]!, text: 'Tap anything in the live preview to select it and open its settings.' },
  {
    ...ONBOARDING_STEPS[2]!,
    text: 'Settings for the selected piece open in a drawer (also from “Edit selection”). When you are happy, use Export for a Vite or Next.js project.',
    target: '[data-testid="drawer-inspector"]',
  },
];

const narrowNow = () => typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 1100px)').matches;

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
export function Onboarding({ onRailChange, replayKey = 0 }: { onRailChange?: (rail: StudioRail) => void; replayKey?: number }) {
  const [open, setOpen] = useState(shouldShowOnboarding);
  const [index, setIndex] = useState(0);
  // A new replayKey ("Replay the studio tour") starts the tour again from the first step.
  const lastReplay = useRef(replayKey);
  useEffect(() => {
    if (replayKey === lastReplay.current) return;
    lastReplay.current = replayKey;
    setIndex(0);
    setOpen(true);
  }, [replayKey]);
  const steps = narrowNow() ? ONBOARDING_STEPS_NARROW : ONBOARDING_STEPS;
  const step = steps[index]!;
  const card = useRef<HTMLDivElement>(null);

  // Keyboard and screen-reader users start in the card; Escape (while focus is inside it) skips the tour.
  useEffect(() => {
    if (open) card.current?.focus();
  }, [open]);

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
  const last = index === steps.length - 1;

  return (
    <div
      ref={card}
      className="pg-coach"
      role="dialog"
      aria-label="Studio tour"
      aria-live="polite"
      tabIndex={-1}
      data-testid="onboarding"
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          finish();
        }
      }}
    >
      <p className="pg-coach-step pg-muted" data-testid="onboarding-step">
        Step {index + 1} of {steps.length}
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
