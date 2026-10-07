import { useMemo, useState } from 'react';
import { MODULE_ROUTER_ADDRESS, encodeModuleCallHex, fromUnits, toUnits } from '@splendor/sqc-sdk';
import { useAccount, useEstimateFee, useNetwork, useQRC20 } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import { normalizeSqAddress } from './utils.js';
import { TOKEN_LABELS, formatLabel, useWidgetLabels, type TokenPanelLabels } from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressInput, AmountInput } from './parts/inputs.js';
import { ReviewPanel } from './parts/ReviewPanel.js';
import { FeeRow } from './parts/FeeRow.js';
import { TxResult } from './parts/TxResult.js';
import { useTransferForm } from './parts/useTransferForm.js';
import { toError, useStatusChange, useWidgetToast } from './parts/useWidgetEffects.js';

export type TokenSentResult = {
  token: string;
  to: string;
  /** Token amount as entered (decimal, before applying decimals). */
  amount: string;
  hash?: string;
};

export type TokenPanelProps = BaseWidgetProps<TokenSentResult, TokenPanelLabels> & {
  /** QRC-20 token address. Optional — panel shows an address field when omitted. */
  tokenAddress?: string;
  /** Default transfer amount (decimal). Default "1". */
  defaultAmount?: string;
  /** @deprecated Use `onSuccess`. */
  onSent?: (result: TokenSentResult) => void;
  /** Show the node's fee estimate in the review step (default true). */
  showFee?: boolean;
};

/**
 * Drop-in QRC-20 balance + human-amount transfer panel.
 */
export function TokenPanel({
  tokenAddress: tokenAddressProp,
  className,
  style,
  title,
  hideTitle,
  labels,
  defaultAmount = '1',
  onSuccess,
  onSent,
  onError,
  onStatusChange,
  toast = true,
  showFee = true,
}: TokenPanelProps) {
  const l = useWidgetLabels(TOKEN_LABELS, labels, 'token');
  const { address } = useAccount();
  const { network, walletMismatch } = useNetwork();
  const [addrInput, setAddrInput] = useState(tokenAddressProp?.trim() ?? '');
  const typed = addrInput.trim();
  const typedValid = !!normalizeSqAddress(typed);
  const tokenAddress = tokenAddressProp?.trim() || (typedValid ? typed : '') || null;
  const { balance, tokenInfo, decimals, transferHuman, status, hash, explorerUrl, error, reset, loading } =
    useQRC20(tokenAddress);

  const knownSymbol = tokenInfo && typeof tokenInfo.symbol === 'string' ? tokenInfo.symbol : null;
  const symbol = knownSymbol ?? 'TOKEN';

  // No human balance until the token's decimals are known: guessing would misstate amounts.
  let humanBalance: string | null = null;
  if (balance != null && decimals != null) {
    try {
      humanBalance = toUnits(balance, decimals);
    } catch {
      humanBalance = null;
    }
  }

  const form = useTransferForm({
    balance: humanBalance,
    symbol,
    self: address,
    defaultAmount,
    decimals: decimals ?? undefined,
    contextKey: `${network?.chainId ?? ''}|${tokenAddress ?? ''}`,
    labels: l,
  });
  const feeRequest = useMemo(() => {
    if (!showFee || !form.reviewing || !form.review.ok || !address || !tokenAddress || decimals == null) return null;
    try {
      const data = encodeModuleCallHex({
        module: 'qrc20',
        action: 'transfer',
        params: { token: tokenAddress, to: form.review.to, amount: fromUnits(form.review.amount, decimals) },
      });
      return { from: address, to: MODULE_ROUTER_ADDRESS, data };
    } catch {
      return null;
    }
  }, [showFee, form.reviewing, form.review, address, tokenAddress, decimals]);
  const fee = useEstimateFee(feeRequest);
  const toasts = useWidgetToast(toast);
  useStatusChange(status, onStatusChange);

  const loadError = status !== 'error' && error ? error : null;
  const hint = !tokenAddress
    ? l.enterToken
    : loading
      ? l.loading
      : humanBalance != null
        ? formatLabel(l.balance, { balance: humanBalance, symbol })
        : loadError
          ? formatLabel(l.loadFailed, { error: connectErrorMessage(loadError) })
          : '—';

  const onConfirm = async () => {
    const review = form.review;
    if (!tokenAddress || !review.ok || walletMismatch) return;
    reset();
    toasts.pending(formatLabel(l.toastPending, { symbol }));
    let res: { hash?: string };
    try {
      res = await transferHuman(review.to, review.amount);
    } catch (e) {
      toasts.error(formatLabel(l.toastError, { symbol }), e);
      onError?.(toError(e));
      return;
    }
    toasts.success(formatLabel(l.toastSuccess, { symbol }), { hash: res.hash });
    form.remember(review.to);
    form.cancelReview();
    const result: TokenSentResult = { token: tokenAddress, to: review.to, amount: review.amount, hash: res.hash };
    onSuccess?.(result);
    onSent?.(result);
  };

  return (
    <WidgetShell
      kind="token"
      status={status}
      className={className}
      style={style}
      title={title ?? knownSymbol ?? l.title}
      hideTitle={hideTitle}
      gateLabels={l}
    >
      {!tokenAddressProp && !form.reviewing ? (
        <AddressInput
          label={l.tokenLabel}
          placeholder={l.tokenPlaceholder}
          value={addrInput}
          onChange={setAddrInput}
          error={typed && !typedValid ? l.invalidToken : null}
          labels={l}
        />
      ) : null}
      <p className="sqc-widget__hint">{hint}</p>
      {form.reviewing && form.review.ok ? (
        <ReviewPanel
          from={address}
          to={form.review.to}
          amount={form.review.amount}
          symbol={symbol}
          contract={tokenAddress}
          networkName={network?.name}
          pending={status === 'pending'}
          confirmDisabled={walletMismatch || (showFee && feeRequest != null && (fee.loading || fee.reverted))}
          onBack={form.cancelReview}
          onConfirm={() => void onConfirm()}
          labels={l}
        >
          {showFee && feeRequest ? <FeeRow estimate={fee} labels={l} /> : null}
        </ReviewPanel>
      ) : (
        <>
          <AddressInput
            label={l.toLabel}
            placeholder={l.toPlaceholder}
            value={form.to}
            onChange={form.setTo}
            error={form.errors.to}
            recents={form.recents}
            labels={l}
          />
          <AmountInput
            label={l.amountLabel}
            value={form.amount}
            onChange={form.setAmount}
            error={form.errors.amount}
            balance={humanBalance}
            symbol={symbol}
            decimals={decimals ?? undefined}
            labels={l}
          />
          <button
            type="button"
            className="sqc-btn sqc-btn--primary sqc-btn--block"
            disabled={!tokenAddress || decimals == null || !form.canReview || status === 'pending'}
            onClick={form.startReview}
          >
            {status === 'pending' ? l.sending : l.review}
          </button>
        </>
      )}
      <TxResult hash={hash} explorerUrl={explorerUrl} labels={l} />
      {status === 'error' && error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(error)}
        </p>
      ) : null}
    </WidgetShell>
  );
}
