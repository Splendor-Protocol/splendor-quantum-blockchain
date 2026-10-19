import { useEffect, useRef, useState } from 'react';
import type { DappSpec, NetworkId } from '@splendor/dapp-spec';
import type { MockWalletState } from './mock';
import { PREVIEW_SOURCE, isPreviewMessage, type ParentToPreview, type PreviewToParent } from './protocol';

export type PreviewFrameProps = {
  spec: DappSpec;
  pagePath: string;
  network: NetworkId;
  walletState: MockWalletState;
  selectedBlockId?: string | null;
  onNavigate?: (path: string) => void;
  onSelectBlock?: (id: string) => void;
  onCalls?: (calls: { method: string; args: string }[]) => void;
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

/** Mock-wallet preview in an isolated iframe, fed the spec via postMessage. */
export function PreviewFrame({
  spec,
  pagePath,
  network,
  walletState,
  selectedBlockId = null,
  onNavigate,
  onSelectBlock,
  onCalls,
  title = 'dApp preview',
  height = 640,
}: PreviewFrameProps) {
  const ref = useRef<HTMLIFrameElement>(null);
  const [ready, setReady] = useState(false);
  const handlers = useRef({ onNavigate, onSelectBlock, onCalls });
  handlers.current = { onNavigate, onSelectBlock, onCalls };

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      // The frame's window identity is the check that matters; its origin is "null" when isolated.
      if (e.source !== ref.current?.contentWindow) return;
      if (e.origin !== (ISOLATED ? 'null' : window.location.origin)) return;
      if (!isPreviewMessage<PreviewToParent>(e.data)) return;
      const msg = e.data;
      if (msg.type === 'ready') setReady(true);
      else if (msg.type === 'navigate') handlers.current.onNavigate?.(msg.pagePath);
      else if (msg.type === 'select') handlers.current.onSelectBlock?.(msg.blockId);
      else if (msg.type === 'calls') handlers.current.onCalls?.(msg.calls);
    };
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, []);

  useEffect(() => {
    if (!ready) return;
    const msg: ParentToPreview = { source: PREVIEW_SOURCE, type: 'render', spec, pagePath, network, walletState, selectedBlockId };
    // An opaque origin cannot be named as a target. The spec is the user's own draft, and the
    // frame is addressed by its window, not broadcast.
    ref.current?.contentWindow?.postMessage(msg, ISOLATED ? '*' : window.location.origin);
  }, [ready, spec, pagePath, network, walletState, selectedBlockId]);

  return (
    <iframe
      ref={ref}
      src="/preview"
      title={title}
      className="pg-preview-frame"
      style={{ height }}
      sandbox={SANDBOX}
      data-testid="preview-frame"
      data-isolated={ISOLATED ? 'true' : 'false'}
    />
  );
}
