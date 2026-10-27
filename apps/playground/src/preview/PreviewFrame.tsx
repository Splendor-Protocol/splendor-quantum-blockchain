import { useEffect, useRef, useState } from 'react';
import type { DappSpec, NetworkId } from '@splendor/dapp-spec';
import type { MockWalletState } from './mock';
import { PREVIEW_SOURCE, PREVIEW_SRC, isPreviewMessage, sanitizeRects, type ParentToPreview, type PreviewMode, type PreviewRects, type PreviewToParent } from './protocol';

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
/** How long a frame may take to say `ready` before the canvas offers a reload. */
export const PREVIEW_READY_TIMEOUT_MS = 8_000;
/** Reloads in a row without a `ready` in between; past this the frame is left alone. */
const MAX_RELOADS = 3;

function newToken(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

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
  /**
   * Each frame document gets a fresh token in its URL fragment and must echo it. A document the
   * frame navigates to (a link in an untrusted spec) cannot know it, so its messages are ignored.
   */
  const [frame, setFrame] = useState(() => ({ key: 0, token: newToken() }));
  const tokenRef = useRef(frame.token);
  tokenRef.current = frame.token;
  /** Counts every `ready` message: a reloaded frame says it again and needs the spec re-sent. */
  const [readyCount, setReadyCount] = useState(0);
  const ready = readyCount > 0;
  const [timedOut, setTimedOut] = useState(false);
  const loads = useRef(0);
  const reloads = useRef(0);
  const handlers = useRef({ onNavigate, onSelectBlock, onCalls, onRects, onHover, onDrop });
  handlers.current = { onNavigate, onSelectBlock, onCalls, onRects, onHover, onDrop };

  const reload = () => {
    loads.current = 0;
    setReadyCount(0);
    setTimedOut(false);
    setFrame((f) => ({ key: f.key + 1, token: newToken() }));
  };

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      // The frame's window identity is the check that matters; its origin is "null" when isolated.
      if (e.source !== ref.current?.contentWindow) return;
      if (e.origin !== (ISOLATED ? 'null' : window.location.origin)) return;
      if (!isPreviewMessage<PreviewToParent>(e.data) || e.data.token !== tokenRef.current) return;
      const msg = e.data;
      if (msg.type === 'ready') {
        reloads.current = 0;
        setTimedOut(false);
        setReadyCount((n) => n + 1);
      } else if (msg.type === 'navigate') {
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
    if (ready) return;
    const t = window.setTimeout(() => setTimedOut(true), PREVIEW_READY_TIMEOUT_MS);
    return () => window.clearTimeout(t);
  }, [ready, frame.key]);

  const post = (msg: ParentToPreview) => {
    // An opaque origin cannot be named as a target. The spec is the user's own draft, and the
    // frame is addressed by its window, not broadcast.
    ref.current?.contentWindow?.postMessage(msg, ISOLATED ? '*' : window.location.origin);
  };

  // Selection travels on its own, so selecting a block does not resend the whole spec.
  const selectedRef = useRef(selectedBlockId);
  selectedRef.current = selectedBlockId;
  useEffect(() => {
    if (!readyCount) return;
    post({ source: PREVIEW_SOURCE, type: 'render', spec, pagePath, network, walletState, selectedBlockId: selectedRef.current, mode, resetKey });
  }, [readyCount, spec, pagePath, network, walletState, mode, resetKey]);
  const firstSelect = useRef(true);
  useEffect(() => {
    if (firstSelect.current) {
      firstSelect.current = false;
      return;
    }
    if (readyCount) post({ source: PREVIEW_SOURCE, type: 'select', selectedBlockId });
    // Only selection changes; a fresh `ready` already got it with the render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedBlockId]);

  // The frame is resized by the device switch and the window; ask it to re-measure.
  useEffect(() => {
    const el = ref.current;
    if (!ready || !el || typeof ResizeObserver !== 'function') return;
    const ro = new ResizeObserver(() => post({ source: PREVIEW_SOURCE, type: 'requestRects' }));
    ro.observe(el);
    return () => ro.disconnect();
  }, [ready, readyCount]);

  return (
    <>
      {timedOut && !ready ? (
        <div className="pg-local-notice" role="alert" data-testid="preview-timeout">
          <span>The preview has not started. Check your connection, or reload it.</span>
          <button type="button" className="pg-btn pg-btn-sm" onClick={reload}>
            Reload preview
          </button>
        </div>
      ) : null}
      <iframe
        key={frame.key}
        ref={ref}
        src={`${PREVIEW_SRC}#t=${frame.token}`}
        title={title}
        className="pg-preview-frame"
        style={{ height }}
        sandbox={SANDBOX}
        data-testid="preview-frame"
        data-isolated={ISOLATED ? 'true' : 'false'}
        onLoad={() => {
          // One document per frame: a second load means it navigated away from the preview.
          loads.current += 1;
          if (loads.current < 2 || reloads.current >= MAX_RELOADS) return;
          reloads.current += 1;
          reload();
        }}
      />
    </>
  );
}
