import { useState } from 'react';
import { toUnits } from '@splendor/sqc-sdk/units';
import { useAccount, useNetwork, useQRC20 } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import { normalizeSqAddress } from './utils.js';
import { TOKEN_LABELS, formatLabel, useWidgetLabels, type TokenPanelLabels } from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressInput, AmountInput } from './parts/inputs.js';
import { ReviewPanel } from './parts/ReviewPanel.js';
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
}: TokenPanelProps) {
  const l = useWidgetLabels(TOKEN_LABELS, labels, 'token');
  const { address } = useAccount();
  const { network } = useNetwork();
  const [addrInput, setAddrInput] = useState(tokenAddressProp?.trim() ?? '');
  const typed = addrInput.trim();
  const typedValid = !!normalizeSqAddress(typed);
  const tokenAddress = tokenAddressProp?.trim() || (typedValid ? typed : '') || null;
  const { balance, tokenInfo, transferHuman, status, hash, explorerUrl, error, reset, loading } =
    useQRC20(tokenAddress);

  const knownSymbol = tokenInfo && typeof tokenInfo.symbol === 'string' ? tokenInfo.symbol : null;
  const symbol = knownSymbol ?? 'TOKEN';
  const decimals = tokenInfo && typeof tokenInfo.decimals === 'number' ? tokenInfo.decimals : 18;

  let humanBalance: string | null = null;
  if (balance != null) {
    try {
      humanBalance = toUnits(balance, decimals);
    } catch {
      humanBalance = balance;
    }
  }

  const form = useTransferForm({ balance: humanBalance, symbol, self: address, defaultAmount, labels: l });
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
    if (!tokenAddress || !review.ok) return;
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
      {!tokenAddressProp ? (
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
          to={form.review.to}
          amount={form.review.amount}
          symbol={symbol}
          networkName={network?.name}
          pending={status === 'pending'}
          onBack={form.cancelReview}
          onConfirm={() => void onConfirm()}
          labels={l}
        />
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
            labels={l}
          />
          <button
            type="button"
            className="sqc-btn sqc-btn--primary sqc-btn--block"
            disabled={!tokenAddress || !form.canReview || status === 'pending'}
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
