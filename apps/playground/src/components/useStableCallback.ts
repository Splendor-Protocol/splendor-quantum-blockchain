import { useCallback, useRef } from 'react';

/**
 * A callback whose identity never changes but always runs the latest `fn`, so a memoized child
 * that receives it does not re-render just because its parent did. Call it from events, not render.
 */
export function useStableCallback<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
  const ref = useRef(fn);
  ref.current = fn;
  return useCallback((...args: A) => ref.current(...args), []);
}
