import { useState, type ComponentType } from 'react';
import { FeeRow, WidgetShell, type FeeEstimate } from '@splendor/connect-react';
import { MockSplendorProvider } from '@splendor/connect-react/testing';
import type { ThemeValue } from '../components/ThemeEditor';

type Estimate = Pick<FeeEstimate, 'feeSpld' | 'feeIsFloor' | 'reverted' | 'revertReason' | 'loading' | 'error'>;

const FEE_STATES: { id: string; label: string; estimate: Estimate }[] = [
  { id: 'ok', label: 'Estimated', estimate: { feeSpld: '0.000021', feeIsFloor: false, reverted: false, revertReason: null, loading: false, error: null } },
  { id: 'floor', label: 'Minimum only', estimate: { feeSpld: '0.000021', feeIsFloor: true, reverted: false, revertReason: null, loading: false, error: null } },
  { id: 'revert', label: 'Will revert', estimate: { feeSpld: '0.000021', feeIsFloor: false, reverted: true, revertReason: 'insufficient balance', loading: false, error: null } },
  { id: 'loading', label: 'Estimating', estimate: { feeSpld: null, feeIsFloor: false, reverted: false, revertReason: null, loading: true, error: null } },
];

function FeeRowPreview() {
  const [state, setState] = useState(FEE_STATES[0]!.id);
  const current = FEE_STATES.find((s) => s.id === state)!;
  return (
    <>
      <div className="pg-segmented" role="group" aria-label="Fee estimate state">
        {FEE_STATES.map((s) => (
          <button key={s.id} type="button" aria-pressed={state === s.id} onClick={() => setState(s.id)}>
            {s.label}
          </button>
        ))}
      </div>
      <WidgetShell kind="fee-row-demo" title="Review" gate={false}>
        <FeeRow estimate={current.estimate} />
      </WidgetShell>
    </>
  );
}

/** Interactive demos for parts that need runtime data and cannot be placed on a page alone. */
const PART_PREVIEWS: Record<string, ComponentType> = { 'fee-row': FeeRowPreview };

export function hasPartPreview(id: string): boolean {
  return id in PART_PREVIEWS;
}

/** Rendered in the playground itself: the demo data is ours, never a user's spec. */
export function PartPreview({ id, theme }: { id: string; theme: ThemeValue }) {
  const Demo = PART_PREVIEWS[id];
  if (!Demo) return null;
  return (
    <MockSplendorProvider theme={{ base: theme.base, ...theme.overrides }}>
      <div className="dapp-root pg-part-preview" data-testid="part-preview">
        <Demo />
      </div>
    </MockSplendorProvider>
  );
}
