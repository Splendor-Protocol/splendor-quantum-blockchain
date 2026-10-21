import { useEffect, useState } from 'react';
import { WidgetShell, useAccount, useQRC1155 } from '@splendor/connect-react';

export type NftGalleryProps = {
  title?: string;
  /** QRC-1155 collection address. */
  collection?: string;
  /** Token ids to show, comma-separated. */
  tokenIds?: string;
};

const MAX_IDS = 24;

/** Grid of QRC-1155 token ids with the connected wallet's balance of each. */
export function NftGallery({ title = 'My collection', collection = '', tokenIds = '1,2,3' }: NftGalleryProps) {
  const { address } = useAccount();
  const nft = useQRC1155(collection || null);
  const ids = tokenIds
    .split(',')
    .map((s) => s.trim())
    .filter((s) => /^\d+$/.test(s))
    .slice(0, MAX_IDS);
  const key = ids.join(',');
  const [owned, setOwned] = useState<Record<string, string>>({});
  const { balanceOf } = nft;

  useEffect(() => {
    setOwned({});
    if (!collection || !address) return;
    let live = true;
    ids.forEach((id) => {
      balanceOf(address, id)
        .then((b) => live && setOwned((o) => ({ ...o, [id]: b })))
        .catch(() => undefined);
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [collection, address, key, balanceOf]);

  if (!collection) {
    return (
      <WidgetShell kind="nft-gallery" title={title} gate={false}>
        <p className="sqc-widget__msg">Set a QRC-1155 collection address.</p>
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
            <span className="sqc-widget__msg">You own {owned[id] ?? '…'}</span>
          </div>
        ))}
      </div>
      {ids.length === 0 ? <p className="sqc-widget__msg">Add token ids, e.g. 1,2,3.</p> : null}
    </WidgetShell>
  );
}
