import { useId, useMemo, useState, type CSSProperties } from 'react';
import { connectErrorMessage } from '../core/controller.js';
import { useAccount, useAddressBook, useNetwork, useQRC721, useReadConnection } from '../react/hooks.js';
import { usePolledRead } from '../react/hooks/polledRead.js';
import { normalizeSqAddress } from './utils.js';
import { NFT_LABELS, formatLabel, useWidgetLabels, type NftPanelLabels } from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressInput } from './parts/inputs.js';
import { TxResult } from './parts/TxResult.js';
import { toError, useResetOnContextChange, useStatusChange, useWidgetToast } from './parts/useWidgetEffects.js';

/** Max token ids one panel scans (each is one `owner_of` read). */
export const NFT_PANEL_MAX_IDS = 60;

export type NftActionResult = { action: 'transfer' | 'mint'; collection: string; id: string; to: string; hash?: string };

export type NftPanelProps = BaseWidgetProps<NftActionResult, NftPanelLabels> & {
  /** QRC-721 collection address. When omitted the panel shows an address field. */
  collectionAddress?: string;
  /** Token ids to show: "1-12", "1,5,9" or a mix. Default "1-12". At most 60 ids. */
  tokenIds?: string;
  /** Allow sending owned NFTs. Default true. */
  showTransfer?: boolean;
  /** Show the mint form to the collection owner. Default true. */
  showMint?: boolean;
  /** Refresh interval (ms). Default 30000. */
  pollMs?: number;
};

const UINT_RE = /^(?:0|[1-9]\d*)$/;

/** Expand "1-3, 7" into ["1","2","3","7"], unique, capped at NFT_PANEL_MAX_IDS. */
export function parseTokenIds(spec: string | undefined): string[] {
  const out: string[] = [];
  for (const part of (spec ?? '').split(/[\s,]+/)) {
    if (!part) continue;
    const range = /^(\d+)-(\d+)$/.exec(part);
    if (range) {
      const a = Number(range[1]);
      const b = Number(range[2]);
      if (!Number.isSafeInteger(a) || !Number.isSafeInteger(b) || b < a) continue;
      for (let i = a; i <= b && out.length < NFT_PANEL_MAX_IDS; i++) if (!out.includes(String(i))) out.push(String(i));
    } else if (UINT_RE.test(part) && !out.includes(part)) {
      out.push(part);
    }
    if (out.length >= NFT_PANEL_MAX_IDS) break;
  }
  return out;
}

type Snapshot = {
  info: { name: string; symbol: string; owner: string; total_supply: string } | null;
  owners: Record<string, string | null>;
};

/** QRC-721 collection: token grid with ownership, send for owned tokens, mint for the collection owner. */
export function NftPanel({
  collectionAddress: collectionProp,
  tokenIds = '1-12',
  showTransfer = true,
  showMint = true,
  pollMs = 30_000,
  className,
  style,
  title,
  hideTitle,
  labels,
  onSuccess,
  onError,
  onStatusChange,
  toast = true,
}: NftPanelProps) {
  const l = useWidgetLabels(NFT_LABELS, labels, 'nft');
  const { address } = useAccount();
  const { network, walletMismatch } = useNetwork();
  const { contacts } = useAddressBook();
  const [typed, setTyped] = useState(collectionProp?.trim() ?? '');
  const collection = normalizeSqAddress(collectionProp) ?? normalizeSqAddress(typed);
  const ids = useMemo(() => parseTokenIds(tokenIds), [tokenIds]);
  const nft = useQRC721(collection);
  const me = normalizeSqAddress(address);
  const toasts = useWidgetToast(toast);
  useStatusChange(nft.status, onStatusChange);
  useResetOnContextChange(nft.reset, nft.status === 'pending', address, network?.chainId, collection);

  const conn = useReadConnection();
  const snap = usePolledRead<Snapshot>(
    collection && conn ? `${collection}|${ids.join(',')}` : null,
    async () => {
      const [info, ...owners] = await Promise.all([
        nft.collectionInfo().catch(() => null),
        ...ids.map((id) => nft.ownerOf(id).then((o) => normalizeSqAddress(o), () => null)),
      ]);
      return { info, owners: Object.fromEntries(ids.map((id, i) => [id, owners[i] ?? null])) };
    },
    pollMs,
  );

  const owned = useMemo(
    () => (me && snap.data ? ids.filter((id) => snap.data!.owners[id] === me) : []),
    [ids, me, snap.data],
  );
  const isCollectionOwner = !!me && !!snap.data?.info?.owner && normalizeSqAddress(snap.data.info.owner) === me;
  const firstFree = snap.data ? ids.find((id) => snap.data!.owners[id] == null) ?? '' : '';

  const [selected, setSelected] = useState<string | null>(null);
  const [to, setTo] = useState('');
  const [mintId, setMintId] = useState('');
  const [mintTo, setMintTo] = useState('');
  const [mintUri, setMintUri] = useState('');
  const mintIdField = `${useId()}-mint-id`;
  const mintUriField = `${useId()}-mint-uri`;
  const chosen = selected && owned.includes(selected) ? selected : null;
  const toAddr = normalizeSqAddress(to);
  const effMintId = mintId.trim() || firstFree;
  const effMintTo = normalizeSqAddress(mintTo) ?? me;
  const mintIdError = effMintId && !UINT_RE.test(effMintId)
    ? l.invalidId
    : effMintId && snap.data?.owners[effMintId]
      ? formatLabel(l.alreadyMinted, { id: effMintId })
      : null;

  const run = async (action: 'transfer' | 'mint', id: string, recipient: string) => {
    if (!collection || walletMismatch || nft.status === 'pending') return;
    nft.reset();
    toasts.pending(formatLabel(action === 'mint' ? l.toastMintPending : l.toastPending, { id }));
    let res: { hash?: string };
    try {
      res = action === 'mint' ? await nft.mint(recipient, id, mintUri.trim() || undefined) : await nft.transfer(recipient, id);
    } catch (e) {
      toasts.error(formatLabel(action === 'mint' ? l.toastMintError : l.toastError, { id }), e);
      onError?.(toError(e));
      return;
    }
    toasts.success(formatLabel(action === 'mint' ? l.toastMintSuccess : l.toastSuccess, { id }), { hash: res.hash });
    if (action === 'transfer') {
      setSelected(null);
      setTo('');
    } else {
      setMintId('');
      setMintUri('');
    }
    onSuccess?.({ action, collection, id, to: recipient, hash: res.hash });
    void snap.refresh();
  };

  const info = snap.data?.info;
  return (
    <WidgetShell kind="nft" status={nft.status} className={className} style={style} title={title ?? info?.name ?? l.title} hideTitle={hideTitle} gateLabels={l}>
      {!collectionProp ? (
        <AddressInput
          label={l.collectionLabel}
          value={typed}
          onChange={setTyped}
          error={typed && !normalizeSqAddress(typed) ? l.invalidCollection : null}
          labels={l}
        />
      ) : null}
      {!collection ? <p className="sqc-widget__hint">{l.enterCollection}</p> : null}
      {info ? (
        <p className="sqc-widget__hint">
          {formatLabel(l.summary, { name: info.name, symbol: info.symbol, supply: info.total_supply })}
          {me ? ` · ${formatLabel(l.owned, { count: owned.length })}` : ''}
        </p>
      ) : collection && snap.loading ? (
        <p className="sqc-widget__hint">{l.loading}</p>
      ) : null}
      {collection && snap.data ? (
        ids.length ? (
          <ul className="sqc-nft-grid" aria-label={l.title}>
            {ids.map((id) => {
              const owner = snap.data!.owners[id];
              const mine = !!me && owner === me;
              return (
                <li key={id} className="sqc-nft-grid__item" data-testid="sqc-nft" data-owned={mine ? 'true' : 'false'} data-minted={owner ? 'true' : 'false'}>
                  <button
                    type="button"
                    className="sqc-nft-tile"
                    aria-pressed={chosen === id}
                    disabled={!mine || !showTransfer}
                    onClick={() => setSelected(chosen === id ? null : id)}
                    title={owner ?? l.unminted}
                  >
                    <span className="sqc-nft-tile__id">{formatLabel(l.tokenLabel, { id })}</span>
                    <span className="sqc-nft-tile__meta">{mine ? l.yours : owner ? '' : l.unminted}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="sqc-widget__hint">{l.empty}</p>
        )
      ) : null}
      {showTransfer && collection && owned.length ? (
        chosen ? (
          <>
            <AddressInput label={l.toLabel} placeholder={l.toPlaceholder} value={to} onChange={setTo} self={address} contacts={contacts} labels={l} />
            <button
              type="button"
              className="sqc-btn sqc-btn--primary sqc-btn--block"
              disabled={!toAddr || toAddr === me || nft.status === 'pending'}
              onClick={() => toAddr && void run('transfer', chosen, toAddr)}
            >
              {nft.status === 'pending' ? l.sending : `${l.send} #${chosen}`}
            </button>
          </>
        ) : (
          <p className="sqc-widget__hint">{l.selectToken}</p>
        )
      ) : null}
      {showMint && isCollectionOwner ? (
        <div className="sqc-nft-mint">
          <span className="sqc-widget__label">{l.mintTitle}</span>
          <label className="sqc-widget__label" htmlFor={mintIdField}>
            {l.mintId}
          </label>
          <input id={mintIdField} className="sqc-input" value={mintId} placeholder={firstFree} inputMode="numeric" onChange={(e) => setMintId(e.target.value)} aria-invalid={!!mintIdError} />
          {mintIdError ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {mintIdError}
            </p>
          ) : null}
          <AddressInput label={l.mintTo} value={mintTo} placeholder={me ?? 'SQ:…'} onChange={setMintTo} contacts={contacts} labels={l} />
          <label className="sqc-widget__label" htmlFor={mintUriField}>
            {l.mintUri}
          </label>
          <input id={mintUriField} className="sqc-input" value={mintUri} maxLength={2048} placeholder="ipfs://…" onChange={(e) => setMintUri(e.target.value)} />
          <button
            type="button"
            className="sqc-btn sqc-btn--ghost sqc-btn--block"
            disabled={!effMintId || !!mintIdError || !effMintTo || (!!mintTo.trim() && !normalizeSqAddress(mintTo)) || nft.status === 'pending'}
            onClick={() => effMintTo && void run('mint', effMintId, effMintTo)}
          >
            {l.mint} #{effMintId || '?'}
          </button>
        </div>
      ) : null}
      <TxResult hash={nft.hash} explorerUrl={nft.explorerUrl} labels={l} />
      {nft.status === 'error' && nft.error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(nft.error, 'tx')}
        </p>
      ) : null}
    </WidgetShell>
  );
}
