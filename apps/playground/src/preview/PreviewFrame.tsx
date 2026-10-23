import { useEffect, useRef, useState } from 'react';
import type { DappSpec, NetworkId } from '@splendor/dapp-spec';
import type { MockWalletState } from './mock';
import { PREVIEW_SOURCE, isPreviewMessage, sanitizeRects, type ParentToPreview, type PreviewMode, type PreviewRects, type PreviewToParent } from './protocol';

export type PreviewFrameProps = {
  /** `edit` (default) is the builder canvas; `play` runs the app like a visitor (gates enforced, no selection). */
  mode?: PreviewMode;
  /** Bump to restart the app in the frame (clears the test wallet's signed-in state). */
  resetKey?: number;
  spec: DappSpec;
  pagePath: string;
  network: NetworkId;
  walletState: MockWalletState;
  selectedBlockId?: string | null;
  onNavigate?: (path: string) => void;
  onSelectBlock?: (id: string) => void;
  onCalls?: (calls: { method: string; args: string }[]) => void;
  /** Measured node rects + insertion points, in the frame's viewport coordinates. */
  onRects?: (rects: PreviewRects) => void;
  /** Node (block or container id) under the pointer inside the frame, or null. */
  onHover?: (id: string | null) => void;
  /** A native drag was dropped inside the frame (fallback when no overlay captures drags). */
  onDrop?: (drop: { parentId: string; index: number; contractFn?: string }) => void;
  title?: string;
  height?: number | string;
};

/**
 * Production builds give the preview an opaque origin (no allow-same-origin), so a shared spec
 * rendered inside it cannot reach the playground's storage, cookies or DOM. That needs CORS on
 * /assets (see public/_headers). The dev server is not opened up with CORS, so in dev the frame
 * stays same-origin.
 */
const ISOLATED = !import.meta.env.DEV;
const SANDBOX = ISOLATED ? 'allow-scripts allow-forms allow-popups' : 'allow-scripts allow-same-origin allow-forms allow-popups';

/** /preview unless the app is hosted under a base path. */
const PREVIEW_SRC = `${import.meta.env.BASE_URL.replace(/\/?$/, '/')}preview`;

/** Mock-wallet preview in an isolated iframe, fed the spec via postMessage. */
export function PreviewFrame({
  mode = 'edit',
  resetKey = 0,
  spec,
  pagePath,
  network,
  walletState,
  selectedBlockId = null,
  onNavigate,
  onSelectBlock,
  onCalls,
  onRects,
  onHover,
  onDrop,
  title = 'dApp preview',
  height = 640,
}: PreviewFrameProps) {
  const ref = useRef<HTMLIFrameElement>(null);
  /** Counts every 
eady message: a reloaded frame says it again and needs the spec re-sent. */
  const [readyCount, setReadyCount] = useState(0);
  const ready = readyCount > 0;
  const handlers = useRef({ onNavigate, onSelectBlock, onCalls, onRects, onHover, onDrop });
  handlers.current = { onNavigate, onSelectBlock, onCalls, onRects, onHover, onDrop };

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      // The frame's window identity is the check that matters; its origin is "null" when isolated.
      if (e.source !== ref.current?.contentWindow) return;
      if (e.origin !== (ISOLATED ? 'null' : window.location.origin)) return;
      if (!isPreviewMessage<PreviewToParent>(e.data)) return;
      const msg = e.data;
      if (msg.type === 'ready') setReadyCount((n) => n + 1);
      else if (msg.type === 'navigate') {
        if (typeof msg.pagePath === 'string' && /^\/[a-z0-9-]{0,40}$/.test(msg.pagePath)) handlers.current.onNavigate?.(msg.pagePath);
      } else if (msg.type === 'select') {
        if (typeof msg.blockId === 'string' && /^[A-Za-z0-9_-]{1,120}$/.test(msg.blockId)) handlers.current.onSelectBlock?.(msg.blockId);
      } else if (msg.type === 'calls') {
        if (!Array.isArray(msg.calls)) return;
        handlers.current.onCalls?.(
          msg.calls
            .slice(-200)
            .filter((c) => c && typeof c.method === 'string' && typeof c.args === 'string')
            .map((c) => ({ method: c.method.slice(0, 80), args: c.args.slice(0, 2000) })),
        );
      }
      else if (msg.type === 'rects') handlers.current.onRects?.(sanitizeRects(msg));
      else if (msg.type === 'hover') handlers.current.onHover?.(typeof msg.id === 'string' && msg.id.length <= 120 ? msg.id : null);
      else if (msg.type === 'drop') {
        if (typeof msg.parentId !== 'string' || !Number.isInteger(msg.index) || msg.index < 0) return;
        handlers.current.onDrop?.({
          parentId: msg.parentId.slice(0, 120),
          index: msg.index,
          ...(typeof msg.contractFn === 'string' ? { contractFn: msg.contractFn.slice(0, 8000) } : {}),
        });
      }
    };
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, []);

  useEffect(() => {
    if (!readyCount) return;
    const msg: ParentToPreview = { source: PREVIEW_SOURCE, type: 'render', spec, pagePath, network, walletState, selectedBlockId, mode, resetKey };
    // An opaque origin cannot be named as a target. The spec is the user's own draft, and the
    // frame is addressed by its window, not broadcast.
    ref.current?.contentWindow?.postMessage(msg, ISOLATED ? '*' : window.location.origin);
  }, [readyCount, spec, pagePath, network, walletState, selectedBlockId, mode, resetKey]);

  // The frame is resized by the device switch and the window; ask it to re-measure.
  useEffect(() => {
    const el = ref.current;
    if (!ready || !el || typeof ResizeObserver !== 'function') return;
    const ro = new ResizeObserver(() => {
      const msg: ParentToPreview = { source: PREVIEW_SOURCE, type: 'requestRects' };
      el.contentWindow?.postMessage(msg, ISOLATED ? '*' : window.location.origin);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [ready, readyCount]);

  return (
    <iframe
      ref={ref}
      src={PREVIEW_SRC}
      title={title}
      className="pg-preview-frame"
      style={{ height }}
      sandbox={SANDBOX}
      data-testid="preview-frame"
      data-isolated={ISOLATED ? 'true' : 'false'}
    />
  );
}
