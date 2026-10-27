import { useSyncExternalStore } from 'react';
import type { PreviewRects } from '../preview/protocol';

/**
 * What the preview frame reports many times a second (pointer hover, measured rects). Only the
 * canvas overlay reads it, so these updates re-render the overlay and not the whole studio.
 */
export type CanvasStore = {
  getHover: () => string | null;
  getRects: () => PreviewRects | null;
  setHover: (id: string | null) => void;
  setRects: (rects: PreviewRects | null) => void;
  subscribe: (listener: () => void) => () => void;
};

export function createCanvasStore(): CanvasStore {
  let hover: string | null = null;
  let rects: PreviewRects | null = null;
  const listeners = new Set<() => void>();
  const emit = () => listeners.forEach((l) => l());
  return {
    getHover: () => hover,
    getRects: () => rects,
    setHover: (id) => {
      if (id === hover) return;
      hover = id;
      emit();
    },
    setRects: (next) => {
      if (next === rects) return;
      rects = next;
      emit();
    },
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}

export function useCanvasHover(store: CanvasStore): string | null {
  return useSyncExternalStore(store.subscribe, store.getHover, store.getHover);
}

export function useCanvasRects(store: CanvasStore): PreviewRects | null {
  return useSyncExternalStore(store.subscribe, store.getRects, store.getRects);
}
