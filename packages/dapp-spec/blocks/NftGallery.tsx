import { useEffect, useState } from 'react';
import { WidgetShell, connectErrorMessage, useAccount, useQRC1155 } from '@splendor/connect-react';
import { parseList } from './parseList';

export type NftGalleryProps = {
  title?: string;
  /** QRC-1155 collection address. */
  collection?: string;
  /** Token ids to show (whole numbers), comma-separated (or one per line). Duplicates are shown once. */
  tokenIds?: string;
};

const MAX_IDS = 24;

/** Grid of QRC-1155 token ids with the connected wallet's balance of each. */
export function NftGallery({ title = 'My collection', collection = '', tokenIds = '1,2,3' }: NftGalleryProps) {
  const { address } = useAccount();
  const nft = useQRC1155(collection || null);
  const ids = [...new Set(parseList(tokenIds).filter((s) => /^(0|[1-9]\d*)$/.test(s)))].slice(0, MAX_IDS);
  const key = ids.join(',');
  const [owned, setOwned] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const { balanceOf } = nft;

  useEffect(() => {
    setOwned({});
    setError(null);
    if (!collection || !address) return;
    let live = true;
    ids.forEach((id) => {
      balanceOf(address, id)
        .then((b) => live && setOwned((o) => ({ ...o, [id]: b })))
        .catch((e) => live && setError(connectErrorMessage(e)));
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [collection, address, key, balanceOf]);

  if (!collection) {
    return (
      <WidgetShell kind="nft-gallery" title={title} gate={false}>
        <p className="sqc-widget__msg">Pick a collection in Content, or paste a QRC-1155 collection address.</p>
      </WidgetShell>
    );
  }

  return (
    <WidgetShell kind="nft-gallery" title={title}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))', gap: '0.75rem' }}>
        {ids.map((id) => (
          <div key={id} className="sqc-widget" style={{ padding: '0.75rem', textAlign: 'center' }}>
            <div
              aria-hidden="true"
              style={{
                aspectRatio: '1',
                borderRadius: 8,
                marginBottom: '0.5rem',
                background: 'var(--sqc-hero-gradient, var(--sqc-surface, #12151d))',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontWeight: 700,
                fontSize: '1.4rem',
              }}
            >
              #{id}
            </div>
            <span className="sqc-widget__msg">You own {owned[id] ?? (error ? '?' : '…')}</span>
          </div>
        ))}
      </div>
      {ids.length === 0 ? <p className="sqc-widget__msg">No tokens to show yet. Add token ids in Content, e.g. 1,2,3.</p> : null}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          Could not load balances: {error}
        </p>
      ) : null}
    </WidgetShell>
  );
}
