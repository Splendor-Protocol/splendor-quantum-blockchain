import type { DappSpec, NetworkId } from '@splendor/dapp-spec';
import type { MockWalletState } from './mock';

export const PREVIEW_SOURCE = 'splendor-playground-preview';

export type ParentToPreview = {
  source: typeof PREVIEW_SOURCE;
  type: 'render';
  spec: unknown;
  pagePath: string;
  network: NetworkId;
  walletState: MockWalletState;
  selectedBlockId: string | null;
};

export type PreviewToParent =
  | { source: typeof PREVIEW_SOURCE; type: 'ready' }
  | { source: typeof PREVIEW_SOURCE; type: 'navigate'; pagePath: string }
  | { source: typeof PREVIEW_SOURCE; type: 'select'; blockId: string }
  | { source: typeof PREVIEW_SOURCE; type: 'invalid'; issues: { path: string; message: string }[] }
  | { source: typeof PREVIEW_SOURCE; type: 'calls'; calls: { method: string; args: string }[] };

export function isPreviewMessage<T extends { source: string }>(data: unknown): data is T {
  return typeof data === 'object' && data !== null && (data as { source?: unknown }).source === PREVIEW_SOURCE;
}

export type RenderRequest = Omit<ParentToPreview, 'source' | 'type' | 'spec'> & { spec: DappSpec };
