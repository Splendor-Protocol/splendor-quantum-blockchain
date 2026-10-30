import { useEffect, useState } from 'react';
import { WidgetShell, connectErrorMessage, useAccount, useQRC721 } from '@splendor/connect-react';
import { parseList } from './parseList';

export type Nft721GalleryProps = {
  title?: string;
  /** QRC-721 collection address. */
  collection?: string;
  /** Token ids: "1-12", "1,5,9", or one per line. At most 48 are shown. */
  tokenIds?: string;
  /** Show who owns each token. */
  showOwner?: boolean;
};

const MAX_IDS = 48;

function expandIds(raw: string): string[] {
  const out: string[] = [];
  for (const part of parseList(raw)) {
    const range = /^(\d+)\s*-\s*(\d+)$/.exec(part);
    if (range) {
      const a = Number(range[1]);
      const b = Number(range[2]);
      for (let i = a; Number.isSafeInteger(b) && i <= b && out.length < MAX_IDS; i++) if (!out.includes(String(i))) out.push(String(i));
    } else if (/^(0|[1-9]\d*)$/.test(part) && !out.includes(part)) {
      out.push(part);
    }
    if (out.length >= MAX_IDS) break;
  }
  return out;
}

const short = (a: string) => (a.length > 16 ? `${a.slice(0, 9)}…${a.slice(-4)}` : a);

/** Grid of QRC-721 tokens with their owner (yours highlighted). Readable before connecting. */
export function Nft721Gallery({ title = 'Collection', collection = '', tokenIds = '1-12', showOwner = true }: Nft721GalleryProps) {
  const { address } = useAccount();
  const nft = useQRC721(collection || null);
  const ids = expandIds(tokenIds);
  const key = ids.join(',');
  const [owners, setOwners] = useState<Record<string, string | null>>({});
  const [name, setName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { ownerOf, collectionInfo } = nft;

  useEffect(() => {
    setOwners({});
    setName(null);
    setError(null);
    if (!collection) return;
    let live = true;
    collectionInfo()
      .then((info) => live && setName(`${info.name} (${info.symbol})`))
      .catch((e) => live && setError(connectErrorMessage(e)));
    ids.forEach((id) => {
      ownerOf(id)
        .then((o) => live && setOwners((m) => ({ ...m, [id]: o })))
        .catch(() => live && setOwners((m) => ({ ...m, [id]: null })));
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [collection, key, ownerOf, collectionInfo]);

  if (!collection) {
    return (
      <WidgetShell kind="nft721-gallery" title={title} gate={false}>
        <p className="sqc-widget__msg">Paste a QRC-721 collection address in Content.</p>
      </WidgetShell>
    );
  }

  const me = address?.toLowerCase() ?? null;
  return (
    <WidgetShell kind="nft721-gallery" title={title} gate={false}>
      {name ? <p className="sqc-widget__hint">{name}</p> : null}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))', gap: '0.75rem' }}>
        {ids.map((id) => {
          const owner = owners[id];
          const mine = !!owner && !!me && owner.toLowerCase() === me;
          return (
            <div
              key={id}
              className="sqc-widget"
              data-testid="dapp-nft721"
              data-owned={mine ? 'true' : 'false'}
              style={{ padding: '0.75rem', textAlign: 'center', borderColor: mine ? 'var(--sqc-accent)' : undefined }}
            >
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
                  opacity: owner === null ? 0.45 : 1,
                }}
              >
                #{id}
              </div>
              <span className="sqc-widget__msg" title={owner ?? undefined}>
                {owner === undefined ? '…' : owner === null ? 'Not minted' : mine ? 'Yours' : showOwner ? short(owner) : 'Minted'}
              </span>
            </div>
          );
        })}
      </div>
      {ids.length === 0 ? <p className="sqc-widget__msg">No tokens to show yet. Add token ids in Content, e.g. 1-12.</p> : null}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          Could not load the collection: {error}
        </p>
      ) : null}
    </WidgetShell>
  );
}
