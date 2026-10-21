import { useEffect, useMemo, useState } from 'react';
import { TxToastHost } from '@splendor/connect-react';
import { MockSplendorProvider } from '@splendor/connect-react/testing';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, migrateSpec, type DappSpec } from '@splendor/dapp-spec';
import { createPlaygroundMock } from './mock';
import { validateForPreview } from './validatePreview';
import { PREVIEW_SOURCE, isPreviewMessage, type ParentToPreview, type PreviewRects, type PreviewToParent, type RenderRequest } from './protocol';
import { pickDropTarget } from './dropTarget';
import { measureRects, nodeIdAt } from './measure';
import { SpecRenderer } from './SpecRenderer';

const CONTRACT_FN_MIME = 'application/x-splendor-contract-fn';

let lastRectsKey = '';
let lastRects: PreviewRects = { nodes: [], dropTargets: [] };
let lastHover: string | null = null;

/** Measure the rendered nodes and report them to the builder (only when something moved). */
function postRects(force = false) {
  const rects = measureRects();
  const key = JSON.stringify(rects);
  lastRects = rects;
  if (!force && key === lastRectsKey) return;
  lastRectsKey = key;
  post({ type: 'rects', ...rects });
}

function postHover(id: string | null) {
  if (id === lastHover) return;
  lastHover = id;
  post({ type: 'hover', id });
}

const catalog = createCatalog(WIDGET_MANIFESTS);

type Outgoing = PreviewToParent extends infer T ? (T extends PreviewToParent ? Omit<T, 'source'> : never) : never;

function post(msg: Outgoing) {
  window.parent.postMessage({ source: PREVIEW_SOURCE, ...msg }, window.location.origin);
}

function short(v: unknown): string {
  try {
    const s = JSON.stringify(v, (_k, x) => (typeof x === 'bigint' ? x.toString() : x));
    return s.length > 160 ? `${s.slice(0, 157)}…` : s;
  } catch {
    return String(v);
  }
}

/**
 * Runs inside the preview iframe (`/preview`). Takes specs only from a parent window on the
 * playground's origin (`location.origin` is the URL's origin even when this document's own origin
 * is opaque), validates them like any untrusted input, and renders them against the mock wallet.
 */
export function PreviewRuntime() {
  const [req, setReq] = useState<RenderRequest | null>(null);
  const [issues, setIssues] = useState<{ path: string; message: string }[]>([]);

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin || e.source !== window.parent) return;
      if (!isPreviewMessage<ParentToPreview>(e.data)) return;
      if (e.data.type === 'requestRects') {
        postRects(true);
        return;
      }
      if (e.data.type !== 'render') return;
      let spec: DappSpec;
      try {
        const r = validateForPreview(migrateSpec(e.data.spec), catalog);
        if (r.issues.length) post({ type: 'invalid', issues: r.issues });
        if (!r.spec) {
          setIssues(r.issues);
          return;
        }
        spec = r.spec;
      } catch (err) {
        const list = [{ path: '', message: err instanceof Error ? err.message : String(err) }];
        setIssues(list);
        post({ type: 'invalid', issues: list });
        return;
      }
      setIssues([]);
      const { network, walletState, pagePath, selectedBlockId } = e.data;
      setReq({ spec, network, walletState, pagePath, selectedBlockId });
    };
    window.addEventListener('message', onMessage);
    post({ type: 'ready' });
    return () => window.removeEventListener('message', onMessage);
  }, []);

  // Report node rects + drop targets after every render, and whenever layout can change.
  useEffect(() => {
    if (!req) return;
    let raf = 0;
    const schedule = () => {
      if (raf) return;
      raf = window.requestAnimationFrame(() => {
        raf = 0;
        postRects();
      });
    };
    schedule();
    // Widgets resolve async content (balances, images) shortly after mounting.
    const late = window.setTimeout(schedule, 200);
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(schedule) : null;
    ro?.observe(document.documentElement);
    ro?.observe(document.body);
    window.addEventListener('resize', schedule);
    window.addEventListener('scroll', schedule, true);
    return () => {
      window.clearTimeout(late);
      if (raf) window.cancelAnimationFrame(raf);
      ro?.disconnect();
      window.removeEventListener('resize', schedule);
      window.removeEventListener('scroll', schedule, true);
    };
  }, [req]);

  // Hover reporting + native drops that land inside the frame (the builder normally overlays the
  // frame while dragging, so this is the fallback path).
  useEffect(() => {
    const onOver = (e: PointerEvent) => postHover(nodeIdAt(e.target));
    const onLeave = () => postHover(null);
    const onDragOver = (e: DragEvent) => {
      if (pickDropTarget(lastRects, e.clientX, e.clientY)) {
        e.preventDefault();
        if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
      }
    };
    const onDrop = (e: DragEvent) => {
      const target = pickDropTarget(lastRects, e.clientX, e.clientY);
      if (!target) return;
      e.preventDefault();
      const raw = e.dataTransfer?.getData(CONTRACT_FN_MIME);
      post({ type: 'drop', parentId: target.parentId, index: target.index, ...(raw ? { contractFn: raw.slice(0, 8000) } : {}) });
    };
    document.addEventListener('pointerover', onOver);
    document.documentElement.addEventListener('pointerleave', onLeave);
    document.addEventListener('dragover', onDragOver);
    document.addEventListener('drop', onDrop);
    return () => {
      document.removeEventListener('pointerover', onOver);
      document.documentElement.removeEventListener('pointerleave', onLeave);
      document.removeEventListener('dragover', onDragOver);
      document.removeEventListener('drop', onDrop);
    };
  }, []);

  const network = req?.network ?? 'testnet';
  const walletState = req?.walletState ?? 'connected';
  const controller = useMemo(() => createPlaygroundMock({ network, state: walletState }), [network, walletState]);

  useEffect(() => {
    let seen = 0;
    const t = window.setInterval(() => {
      const calls = controller.wallet.calls;
      if (calls.length === seen) return;
      seen = calls.length;
      post({ type: 'calls', calls: calls.slice(-30).map((c) => ({ method: c.method, args: short(c.args) })) });
    }, 500);
    return () => window.clearInterval(t);
  }, [controller]);

  if (!req) {
    return issues.length ? <PreviewIssues issues={issues} /> : <p className="pg-muted pg-pad">Waiting for the playground…</p>;
  }
  return (
    <MockSplendorProvider controller={controller} theme={req.spec.theme.base}>
      <TxToastHost>
        {issues.length ? <PreviewIssues issues={issues} /> : null}
        <SpecRenderer
          spec={req.spec}
          catalog={catalog}
          pagePath={req.pagePath}
          selectedBlockId={req.selectedBlockId}
          onNavigate={(pagePath) => post({ type: 'navigate', pagePath })}
          onSelectBlock={(blockId) => post({ type: 'select', blockId })}
        />
      </TxToastHost>
    </MockSplendorProvider>
  );
}

function PreviewIssues({ issues }: { issues: { path: string; message: string }[] }) {
  return (
    <div className="pg-issues" role="alert">
      <strong>Preview paused — fix these first:</strong>
      <ul>
        {issues.slice(0, 8).map((i) => (
          <li key={`${i.path}:${i.message}`}>
            <code>{i.path || 'spec'}</code> {i.message}
          </li>
        ))}
      </ul>
    </div>
  );
}
