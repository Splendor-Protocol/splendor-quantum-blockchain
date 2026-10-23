import { useEffect, useRef, useState } from 'react';

function visibleNow(): boolean {
  return typeof document === 'undefined' || document.visibilityState !== 'hidden';
}

/** False while the browser tab is hidden (always true during SSR). */
export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(visibleNow);
  useEffect(() => {
    if (typeof document === 'undefined') return;
    const onChange = () => setVisible(visibleNow());
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);
  return visible;
}

/**
 * Calls `tick` every `pollMs` while `enabled` and the tab is visible, and once when the tab
 * becomes visible again. Hidden tabs make no requests. The first load is the caller's job.
 */
export function useVisiblePolling(tick: () => void, pollMs: number, enabled: boolean): void {
  const visible = usePageVisible();
  const tickRef = useRef(tick);
  tickRef.current = tick;
  const wasVisible = useRef(visible);
  useEffect(() => {
    const cameBack = visible && !wasVisible.current;
    wasVisible.current = visible;
    if (!enabled || !pollMs || !visible) return;
    if (cameBack) tickRef.current();
    const id = setInterval(() => tickRef.current(), pollMs);
    return () => clearInterval(id);
  }, [enabled, pollMs, visible]);
}
