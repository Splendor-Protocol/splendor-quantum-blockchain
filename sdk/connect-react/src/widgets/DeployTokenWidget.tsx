import { useEffect, useId, useMemo, useState } from 'react';
import { MODULE_ROUTER_ADDRESS, encodeModuleCallHex, fromUnits } from '@splendor/sqc-sdk';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { useAccount, useBalance, useDeployToken, useEstimateFee, useNetwork } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import {
  DEPLOY_TOKEN_LABELS,
  formatLabel,
  useWidgetLabels,
  type DeployTokenWidgetLabels,
} from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { compareDecimal, exceedsBalanceWithFee } from './utils.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { ReviewPanel } from './parts/ReviewPanel.js';
import { FeeRow } from './parts/FeeRow.js';
import { TxResult } from './parts/TxResult.js';
import { toError, useResetOnContextChange, useStatusChange, useWidgetToast } from './parts/useWidgetEffects.js';

export type TokenDeployedResult = {
  address: string;
  hash?: string;
  explorerUrl?: string | null;
};

export type DeployTokenWidgetProps = BaseWidgetProps<TokenDeployedResult, DeployTokenWidgetLabels> & {
  defaultName?: string;
  defaultSymbol?: string;
  /** Human total supply (converted with decimals before deploy). Default 1_000_000. */
  defaultSupply?: string;
  /** Token decimals. Default 18. */
  decimals?: number;
  /** Show an editable Decimals field (starts at `decimals`). Default false. */
  showDecimals?: boolean;
  /** Optional human max supply (converted with decimals); must be at least the total supply. */
  maxSupply?: string;
  /** Show a read-only summary before asking the wallet to sign. Default true. */
  review?: boolean;
  /** Show the node's fee estimate in the review step (default true). */
  showFee?: boolean;
  /** @deprecated Use `onSuccess`. */
  onDeployed?: (result: { address: string; hash?: string }) => void;
};

export const TOKEN_NAME_MAX = 64;
export const TOKEN_SYMBOL_MAX = 11;

function nameError(v: string): boolean {
  const t = v.trim();
  return !t || t.length > TOKEN_NAME_MAX || /[\u0000-\u001f\u007f]/.test(t);
}

function symbolError(v: string): boolean {
  return !new RegExp(`^[A-Za-z0-9]{1,${TOKEN_SYMBOL_MAX}}$`).test(v.trim());
}

const SUPPLY_RE = /^(?:0|[1-9]\d*)(?:\.\d+)?$/;

function isValidSupply(v: string, decimals: number): boolean {
  const t = v.trim();
  if (!t || !SUPPLY_RE.test(t) || t === '0' || /^0\.0+$/.test(t)) return false;
  try {
    fromUnits(t, decimals);
    return true;
  } catch {
    return false;
  }
}

function parseDecimals(v: string): number | null {
  if (!/^\d{1,2}$/.test(v.trim())) return null;
  const n = Number(v.trim());
  return n >= 0 && n <= 18 ? n : null;
}

/**
 * Drop-in QRC-20 deploy form — supply is human decimals (not raw units).
 */
export function DeployTokenWidget({
  className,
  style,
  title,
  hideTitle,
  labels,
  defaultName = 'Demo',
  defaultSymbol = 'DMO',
  defaultSupply = '1000000',
  decimals: decimalsProp = 18,
  showDecimals = false,
  maxSupply,
  review: reviewStep = true,
  onSuccess,
  onDeployed,
  onError,
  onStatusChange,
  toast = true,
  showFee = true,
}: DeployTokenWidgetProps) {
  const l = useWidgetLabels(DEPLOY_TOKEN_LABELS, labels, 'deploy');
  const uid = useId();
  const { network, walletMismatch } = useNetwork();
  const { address: from } = useAccount();
  const [reviewing, setReviewing] = useState(false);
  const { deploy, status, address, hash, error, reset } = useDeployToken();
  const [name, setName] = useState(defaultName);
  const [symbol, setSymbol] = useState(defaultSymbol);
  const [supply, setSupply] = useState(defaultSupply);
  const [decimalsInput, setDecimalsInput] = useState(String(decimalsProp));
  const [confirmed, setConfirmed] = useState(false);
  const toasts = useWidgetToast(toast);
  useStatusChange(status, onStatusChange);
  useEffect(() => setReviewing(false), [from, network?.chainId]);
  useResetOnContextChange(
    () => {
      reset();
      setConfirmed(false);
    },
    status === 'pending',
    from,
    network?.chainId,
  );

  const decimals = showDecimals ? parseDecimals(decimalsInput) : decimalsProp;
  const max = maxSupply?.trim() || null;
  const maxOk = !max || (decimals != null && isValidSupply(max, decimals) && compareDecimal(supply, max) !== 1);
  const supplyOk = decimals != null && isValidSupply(supply, decimals);
  const nameBad = nameError(name);
  const symbolBad = symbolError(symbol);
  const formOk = !nameBad && !symbolBad && supplyOk && maxOk && !walletMismatch;
  const canDeploy = formOk && status !== 'pending';
  const supplyError = !supply.trim() || decimals == null
    ? null
    : !supplyOk
      ? l.invalidSupply
      : !maxOk
        ? formatLabel(l.supplyOverMax, { max })
        : null;
  const explorer = network && hash ? txExplorerUrl(network, hash) : null;
  const feeRequest = useMemo(() => {
    // Without a review step the estimate runs on the form itself, so the deploy still gets a fee.
    if (!showFee || (reviewStep && !reviewing) || !from || !formOk || decimals == null) return null;
    try {
      const data = encodeModuleCallHex({
        module: 'qrc20',
        action: 'deploy',
        params: {
          name: name.trim(),
          symbol: symbol.trim(),
          decimals,
          total_supply: fromUnits(supply.trim(), decimals),
          ...(max ? { max_supply: fromUnits(max, decimals) } : {}),
        },
      });
      return { from, to: MODULE_ROUTER_ADDRESS, data };
    } catch {
      return null;
    }
  }, [showFee, reviewStep, reviewing, from, formOk, decimals, name, symbol, supply, max]);
  const fee = useEstimateFee(feeRequest);
  const { balanceSPLD } = useBalance();
  const feeUnaffordable = showFee && feeRequest != null && exceedsBalanceWithFee('0', fee.fee, balanceSPLD);
  const feeBlocked = feeUnaffordable || (showFee && feeRequest != null && (fee.loading || fee.reverted));

  const onDeploy = async () => {
    if (!canDeploy || decimals == null || feeBlocked) return;
    reset();
    setConfirmed(false);
    const sym = symbol.trim();
    toasts.pending(formatLabel(l.toastPending, { symbol: sym }));
    let res: { address: string; hash?: string; confirmed?: boolean };
    try {
      res = await deploy({
        name: name.trim(),
        symbol: sym,
        decimals,
        totalSupply: fromUnits(supply.trim(), decimals),
        ...(max ? { maxSupply: fromUnits(max, decimals) } : {}),
        ...(showFee && feeRequest && fee.fee ? { fee: fee.fee } : {}),
        waitForConfirmation: true,
      });
    } catch (e) {
      setReviewing(false);
      toasts.error(formatLabel(l.toastError, { symbol: sym }), e);
      onError?.(toError(e));
      return;
    }
    setReviewing(false);
    setConfirmed(res.confirmed === true);
    const explorerUrl = network && res.hash ? txExplorerUrl(network, res.hash) : null;
    toasts.success(formatLabel(l.toastSuccess, { symbol: sym }), {
      hash: res.hash,
      explorerUrl,
      confirmed: res.confirmed === true,
    });
    onSuccess?.({ address: res.address, hash: res.hash, explorerUrl });
    onDeployed?.({ address: res.address, hash: res.hash });
  };

  const feeUnaffordableMsg = (
    <p className="sqc-widget__msg sqc-widget__msg--error" role="alert" data-testid="sqc-review-fee-unaffordable">
      {l.feeUnaffordable}
    </p>
  );

  const field = (key: string, label: string, value: string, set: (v: string) => void, err?: string | null) => (
    <div className="sqc-field">
      <label className="sqc-widget__label" htmlFor={`${uid}-${key}`}>
        {label}
      </label>
      <input
        id={`${uid}-${key}`}
        className="sqc-input"
        value={value}
        onChange={(e) => set(e.target.value)}
        autoComplete="off"
        aria-invalid={!!err}
        aria-describedby={err ? `${uid}-${key}-error` : undefined}
      />
      {err ? (
        <p id={`${uid}-${key}-error`} className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {err}
        </p>
      ) : null}
    </div>
  );

  return (
    <WidgetShell
      kind="deploy"
      status={status}
      className={className}
      style={style}
      title={title ?? l.title}
      hideTitle={hideTitle}
      gateLabels={l}
    >
      {reviewing && formOk && decimals != null ? (
        <ReviewPanel
          from={from}
          networkName={network?.name}
          rows={[
            { label: l.nameLabel, value: name.trim(), testId: 'sqc-review-name' },
            { label: l.symbolLabel, value: symbol.trim(), testId: 'sqc-review-symbol' },
            { label: l.decimalsLabel, value: String(decimals) },
            { label: l.supplyLabel, value: supply.trim(), testId: 'sqc-review-supply' },
            ...(max ? [{ label: l.maxSupplyLabel, value: max }] : []),
          ]}
          pending={status === 'pending'}
          confirmDisabled={walletMismatch || feeBlocked}
          confirmLabel={l.confirmDeploy}
          ariaLabel={l.reviewDeployAria}
          onBack={() => setReviewing(false)}
          onConfirm={() => void onDeploy()}
          labels={l}
        >
          {showFee && feeRequest ? <FeeRow estimate={fee} labels={l} /> : null}
          {feeUnaffordable ? feeUnaffordableMsg : null}
        </ReviewPanel>
      ) : (
        <>
      {field('name', l.nameLabel, name, (v) => { setName(v); setReviewing(false); }, name.trim() && nameBad ? formatLabel(l.invalidName, { max: TOKEN_NAME_MAX }) : null)}
      {field('sym', l.symbolLabel, symbol, (v) => { setSymbol(v); setReviewing(false); }, symbol.trim() && symbolBad ? formatLabel(l.invalidSymbol, { max: TOKEN_SYMBOL_MAX }) : null)}
      {showDecimals
        ? field('dec', l.decimalsLabel, decimalsInput, setDecimalsInput, decimals == null ? l.invalidDecimals : null)
        : null}
      {field('supply', l.supplyLabel, supply, setSupply, supplyError)}
      {!reviewStep && showFee && feeRequest ? <FeeRow estimate={fee} labels={l} /> : null}
      {!reviewStep && feeUnaffordable ? feeUnaffordableMsg : null}
      <button
        type="button"
        className="sqc-btn sqc-btn--primary sqc-btn--block"
        disabled={!canDeploy || (!reviewStep && feeBlocked)}
        onClick={() => (reviewStep ? setReviewing(true) : void onDeploy())}
      >
        {status === 'pending' ? l.deploying : reviewStep ? l.reviewDeploy : l.deploy}
      </button>
        </>
      )}
      <TxResult
        hash={hash}
        confirmed={status === 'success' && confirmed}
        explorerUrl={explorer}
        extra={address ? { label: l.tokenAddress, value: address } : null}
        labels={l}
      />
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(error)}
        </p>
      ) : null}
    </WidgetShell>
  );
}
