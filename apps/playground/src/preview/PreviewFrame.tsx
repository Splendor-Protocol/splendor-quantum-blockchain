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
      if (e.origin !== window.location.origin || e.source !== ref.current?.contentWindow) return;
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
    ref.current?.contentWindow?.postMessage(msg, window.location.origin);
  }, [ready, spec, pagePath, network, walletState, selectedBlockId]);

  return (
    <iframe
      ref={ref}
      src="/preview"
      title={title}
      className="pg-preview-frame"
      style={{ height }}
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
      data-testid="preview-frame"
    />
  );
}
