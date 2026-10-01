import { useId, useState } from 'react';
import { fromUnits } from '@splendor/sqc-sdk/units';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { useDeployToken, useNetwork } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import {
  DEPLOY_TOKEN_LABELS,
  formatLabel,
  useWidgetLabels,
  type DeployTokenWidgetLabels,
} from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { compareDecimal } from './utils.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { TxResult } from './parts/TxResult.js';
import { toError, useStatusChange, useWidgetToast } from './parts/useWidgetEffects.js';

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
  /** @deprecated Use `onSuccess`. */
  onDeployed?: (result: { address: string; hash?: string }) => void;
};

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
  onSuccess,
  onDeployed,
  onError,
  onStatusChange,
  toast = true,
}: DeployTokenWidgetProps) {
  const l = useWidgetLabels(DEPLOY_TOKEN_LABELS, labels, 'deploy');
  const uid = useId();
  const { network } = useNetwork();
  const { deploy, status, address, hash, error, reset } = useDeployToken();
  const [name, setName] = useState(defaultName);
  const [symbol, setSymbol] = useState(defaultSymbol);
  const [supply, setSupply] = useState(defaultSupply);
  const [decimalsInput, setDecimalsInput] = useState(String(decimalsProp));
  const toasts = useWidgetToast(toast);
  useStatusChange(status, onStatusChange);

  const decimals = showDecimals ? parseDecimals(decimalsInput) : decimalsProp;
  const max = maxSupply?.trim() || null;
  const maxOk = !max || (decimals != null && isValidSupply(max, decimals) && compareDecimal(supply, max) !== 1);
  const supplyOk = decimals != null && isValidSupply(supply, decimals);
  const canDeploy = !!name.trim() && !!symbol.trim() && supplyOk && maxOk && status !== 'pending';
  const supplyError = !supply.trim() || decimals == null
    ? null
    : !supplyOk
      ? l.invalidSupply
      : !maxOk
        ? formatLabel(l.supplyOverMax, { max })
        : null;
  const explorer = network && hash ? txExplorerUrl(network, hash) : null;

  const onDeploy = async () => {
    if (!canDeploy || decimals == null) return;
    reset();
    const sym = symbol.trim();
    toasts.pending(formatLabel(l.toastPending, { symbol: sym }));
    let res: { address: string; hash?: string };
    try {
      res = await deploy({
        name: name.trim(),
        symbol: sym,
        decimals,
        totalSupply: fromUnits(supply.trim(), decimals),
        ...(max ? { maxSupply: fromUnits(max, decimals) } : {}),
        waitForConfirmation: true,
      });
    } catch (e) {
      toasts.error(formatLabel(l.toastError, { symbol: sym }), e);
      onError?.(toError(e));
      return;
    }
    const explorerUrl = network && res.hash ? txExplorerUrl(network, res.hash) : null;
    toasts.success(formatLabel(l.toastSuccess, { symbol: sym }), {
      hash: res.hash,
      explorerUrl,
      confirmed: true,
    });
    onSuccess?.({ address: res.address, hash: res.hash, explorerUrl });
    onDeployed?.({ address: res.address, hash: res.hash });
  };

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
      {field('name', l.nameLabel, name, setName)}
      {field('sym', l.symbolLabel, symbol, setSymbol)}
      {showDecimals
        ? field('dec', l.decimalsLabel, decimalsInput, setDecimalsInput, decimals == null ? l.invalidDecimals : null)
        : null}
      {field('supply', l.supplyLabel, supply, setSupply, supplyError)}
      <button
        type="button"
        className="sqc-btn sqc-btn--primary sqc-btn--block"
        disabled={!canDeploy}
        onClick={() => void onDeploy()}
      >
        {status === 'pending' ? l.deploying : l.deploy}
      </button>
      <TxResult
        hash={hash}
        confirmed={status === 'success'}
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
