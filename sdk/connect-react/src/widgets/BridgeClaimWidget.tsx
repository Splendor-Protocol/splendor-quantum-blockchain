import { useId, useState } from 'react';
import { toUnits } from '@splendor/sqc-sdk';
import { connectErrorMessage } from '../core/controller.js';
import { useAccount, useBridge, useNetwork, useReadConnection } from '../react/hooks.js';
import { usePolledRead } from '../react/hooks/polledRead.js';
import { normalizeSqAddress } from './utils.js';
import { BRIDGE_LABELS, formatLabel, useWidgetLabels, type BridgeClaimLabels } from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { WalletGate } from './parts/WalletGate.js';
import { AddressInput } from './parts/inputs.js';
import { TxResult } from './parts/TxResult.js';
import { toError, useResetOnContextChange, useStatusChange, useWidgetToast } from './parts/useWidgetEffects.js';

export type BridgeClaimResult = { id: number; asset: 'spld' | 'splv'; amount: string; recipient: string; hash?: string };

export type BridgeClaimWidgetProps = BaseWidgetProps<BridgeClaimResult, BridgeClaimLabels> & {
  /** Bridge instance address (`bridge_v1`). */
  bridgeAddress?: string;
  /** Show the burn-id lookup. Default true. */
  showCheck?: boolean;
  /**
   * Show the claim form. Only bridge operators can submit claims (with relayer attestations),
   * so leave this off for public pages. Default false.
   */
  allowClaim?: boolean;
  /** Status refresh interval (ms). Default 30000. */
  pollMs?: number;
};

type BridgeInfo = {
  paused?: boolean;
  threshold?: number;
  evm_chain_id?: number;
  evm_contract?: string;
  max_claim_amount?: string;
  pool_balance?: string;
  claimed_total_spld?: string;
  claimed_total_splv?: string;
};

const UINT_RE = /^(?:0|[1-9]\d*)$/;
const ATTESTATIONS_MAX = 64;

function spld(raw: unknown): string {
  if (typeof raw !== 'string' || !UINT_RE.test(raw)) return '—';
  try {
    return toUnits(BigInt(raw), 18).replace(/\.0+$/, '');
  } catch {
    return raw;
  }
}

/** Parse the attestations field: a non-empty JSON array of { pubkey, signature } strings. */
export function parseAttestations(raw: string): Array<{ pubkey: string; signature: string }> | null {
  try {
    const v: unknown = JSON.parse(raw);
    if (!Array.isArray(v) || v.length === 0 || v.length > ATTESTATIONS_MAX) return null;
    const out: Array<{ pubkey: string; signature: string }> = [];
    for (const a of v) {
      if (!a || typeof a !== 'object') return null;
      const { pubkey, signature } = a as Record<string, unknown>;
      if (typeof pubkey !== 'string' || typeof signature !== 'string' || !pubkey || !signature) return null;
      out.push({ pubkey, signature });
    }
    return out;
  } catch {
    return null;
  }
}

/**
 * Legacy-to-quantum bridge status (pause state, threshold, limits, totals) and a burn-id lookup,
 * readable without a wallet. Operators can turn on `allowClaim` to submit attested claims.
 */
export function BridgeClaimWidget({
  bridgeAddress,
  showCheck = true,
  allowClaim = false,
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
}: BridgeClaimWidgetProps) {
  const l = useWidgetLabels(BRIDGE_LABELS, labels, 'bridge');
  const bridge = normalizeSqAddress(bridgeAddress);
  const conn = useReadConnection();
  const { address } = useAccount();
  const { network, walletMismatch } = useNetwork();
  const b = useBridge(bridge);
  const toasts = useWidgetToast(toast);
  useStatusChange(b.status, onStatusChange);
  useResetOnContextChange(b.reset, b.status === 'pending', address, network?.chainId, bridge);
  const info = usePolledRead<BridgeInfo>(bridge && conn ? bridge : null, () => b.info() as Promise<BridgeInfo>, pollMs);

  const [burnId, setBurnId] = useState('');
  const [lookup, setLookup] = useState<{ id: number; claimed: boolean; height?: number } | null>(null);
  const [lookupError, setLookupError] = useState<string | null>(null);
  const burnValid = UINT_RE.test(burnId.trim()) && Number(burnId) >= 1 && Number.isSafeInteger(Number(burnId));

  const [claim, setClaim] = useState({ id: '', asset: 'spld' as 'spld' | 'splv', amount: '', recipient: '', evmTx: '', attestations: '' });
  const ids = useId();
  const attestations = claim.attestations.trim() ? parseAttestations(claim.attestations) : null;
  const claimId = Number(claim.id);
  const claimReady =
    UINT_RE.test(claim.id) && claimId >= 1 && Number.isSafeInteger(claimId) &&
    UINT_RE.test(claim.amount) && claim.amount !== '0' &&
    !!normalizeSqAddress(claim.recipient) && !!claim.evmTx.trim() && !!attestations;

  const onCheck = async () => {
    if (!burnValid) return;
    setLookupError(null);
    try {
      const res = await b.claimed(Number(burnId));
      setLookup({ id: Number(burnId), claimed: res.claimed === true, height: typeof res.height === 'number' ? res.height : undefined });
    } catch (e) {
      setLookup(null);
      setLookupError(connectErrorMessage(e));
    }
  };

  const onClaim = async () => {
    if (!claimReady || !attestations || walletMismatch || b.status === 'pending') return;
    const params = {
      id: claimId,
      asset: claim.asset,
      amount: claim.amount,
      recipient: normalizeSqAddress(claim.recipient)!,
      evmTx: claim.evmTx.trim(),
      attestations,
    };
    b.reset();
    toasts.pending(l.toastPending);
    let res: { hash?: string };
    try {
      res = await b.claim(params);
    } catch (e) {
      toasts.error(l.toastError, e);
      onError?.(toError(e));
      return;
    }
    toasts.success(l.toastSuccess, { hash: res.hash });
    onSuccess?.({ id: params.id, asset: params.asset, amount: params.amount, recipient: params.recipient, hash: res.hash });
    void info.refresh();
  };

  const d = info.data;
  const field = (key: keyof typeof claim, label: string, extra: Record<string, unknown> = {}) => (
    <div className="sqc-field">
      <label className="sqc-widget__label" htmlFor={`${ids}-${key}`}>
        {label}
      </label>
      <input
        id={`${ids}-${key}`}
        className="sqc-input"
        value={claim[key]}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => setClaim((c) => ({ ...c, [key]: e.target.value }))}
        {...extra}
      />
    </div>
  );

  return (
    <WidgetShell kind="bridge" status={b.status} className={className} style={style} title={title ?? l.title} hideTitle={hideTitle} gate={false}>
      {!bridge ? <p className="sqc-widget__hint">{l.enterBridge}</p> : null}
      {info.error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(info.error)}
        </p>
      ) : null}
      {bridge && !d && info.loading ? <p className="sqc-widget__hint">{l.loading}</p> : null}
      {d ? (
        <dl className="sqc-kv" data-testid="sqc-bridge-info">
          <dt>{l.status}</dt>
          <dd data-paused={d.paused ? 'true' : 'false'}>{d.paused ? l.paused : l.active}</dd>
          <dt>{l.threshold}</dt>
          <dd>{d.threshold ?? '—'}</dd>
          <dt>{l.evmSource}</dt>
          <dd>{d.evm_chain_id != null ? `EVM ${d.evm_chain_id}` : '—'}</dd>
          <dt>{l.maxClaim}</dt>
          <dd>{spld(d.max_claim_amount)}</dd>
          <dt>{l.pool}</dt>
          <dd>{spld(d.pool_balance)}</dd>
          <dt>{l.claimedTotals}</dt>
          <dd>
            {spld(d.claimed_total_spld)} SPLD · {spld(d.claimed_total_splv)} SPLV
          </dd>
        </dl>
      ) : null}
      {showCheck && bridge ? (
        <>
          <span className="sqc-widget__label">{l.checkTitle}</span>
          <div className="sqc-field__row">
            <input
              className="sqc-input"
              aria-label={l.burnId}
              placeholder={l.burnId}
              inputMode="numeric"
              value={burnId}
              onChange={(e) => {
                setBurnId(e.target.value);
                setLookup(null);
              }}
              aria-invalid={!!burnId.trim() && !burnValid}
            />
            <button type="button" className="sqc-btn sqc-btn--ghost" disabled={!burnValid} onClick={() => void onCheck()}>
              {l.check}
            </button>
          </div>
          {burnId.trim() && !burnValid ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {l.invalidBurnId}
            </p>
          ) : null}
          {lookup ? (
            <p className="sqc-widget__msg" role="status" data-testid="sqc-bridge-lookup">
              {lookup.claimed
                ? formatLabel(l.claimedAt, { id: lookup.id, height: lookup.height ?? '?' })
                : formatLabel(l.notClaimed, { id: lookup.id })}
            </p>
          ) : null}
          {lookupError ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {lookupError}
            </p>
          ) : null}
        </>
      ) : null}
      {allowClaim && bridge ? (
        <WalletGate labels={l}>
          <span className="sqc-widget__label">{l.claimTitle}</span>
          {field('id', l.burnId, { inputMode: 'numeric' })}
          <div className="sqc-field">
            <label className="sqc-widget__label" htmlFor={`${ids}-asset`}>
              {l.asset}
            </label>
            <select
              id={`${ids}-asset`}
              className="sqc-input"
              value={claim.asset}
              onChange={(e) => setClaim((c) => ({ ...c, asset: e.target.value === 'splv' ? 'splv' : 'spld' }))}
            >
              <option value="spld">SPLD</option>
              <option value="splv">SPLV</option>
            </select>
          </div>
          {field('amount', l.amount, { inputMode: 'numeric' })}
          {claim.amount && !UINT_RE.test(claim.amount) ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {l.invalidAmount}
            </p>
          ) : null}
          <AddressInput label={l.recipient} value={claim.recipient} onChange={(v) => setClaim((c) => ({ ...c, recipient: v }))} labels={l} />
          {field('evmTx', l.evmTx)}
          <div className="sqc-field">
            <label className="sqc-widget__label" htmlFor={`${ids}-att`}>
              {l.attestations}
            </label>
            <textarea
              id={`${ids}-att`}
              className="sqc-input"
              rows={3}
              spellCheck={false}
              value={claim.attestations}
              onChange={(e) => setClaim((c) => ({ ...c, attestations: e.target.value }))}
              aria-invalid={!!claim.attestations.trim() && !attestations}
            />
            {claim.attestations.trim() && !attestations ? (
              <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
                {l.invalidAttestations}
              </p>
            ) : null}
          </div>
          <button
            type="button"
            className="sqc-btn sqc-btn--primary sqc-btn--block"
            disabled={!claimReady || walletMismatch || d?.paused === true || b.status === 'pending'}
            onClick={() => void onClaim()}
          >
            {b.status === 'pending' ? l.sending : l.submitClaim}
          </button>
          <TxResult hash={b.hash} explorerUrl={b.explorerUrl} labels={l} />
          {b.status === 'error' && b.error ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {connectErrorMessage(b.error, 'tx')}
            </p>
          ) : null}
        </WalletGate>
      ) : null}
    </WidgetShell>
  );
}
