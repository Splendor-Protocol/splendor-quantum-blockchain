import { useAccount, useBalance, useConnect } from '../react/hooks.js';
import { useHostModals } from '../react/context.js';
import { AccountModal } from './AccountModal.js';
import { ConnectModal } from './ConnectModal.js';

function truncateAddress(addr: string, left = 6, right = 4): string {
  if (addr.length <= left + right + 1) return addr;
  return `${addr.slice(0, left)}…${addr.slice(-right)}`;
}

export type ConnectButtonProps = {
  label?: string;
  className?: string;
  /**
   * When true, also mount ConnectModal/AccountModal next to the button.
   * Ignored when Provider `hostModals` is true (single modal host).
   * Default false — prefer provider `hostModals`.
   */
  includeModals?: boolean;
  /** Poll balance for the account chip (ms). 0 disables. Default 12s. */
  balancePollMs?: number;
};

export function ConnectButton({
  label = 'Connect',
  className,
  includeModals = false,
  balancePollMs = 12_000,
}: ConnectButtonProps) {
  const hostModals = useHostModals();
  const mountModals = includeModals && !hostModals;
  const { openModal, status, connectingStep, connectors, errorMessage, clearError } = useConnect();
  const { address, isConnected } = useAccount();
  const showBalance = isConnected && !!address;
  const { balanceSPLD } = useBalance(showBalance ? balancePollMs : 0);

  const connectingLabel =
    connectingStep === 'init'
      ? 'Loading signer…'
      : connectingStep === 'sign'
        ? 'Sign in wallet…'
        : 'Connecting…';

  const detecting = status !== 'connecting' && connectors.length === 0;
  const showError = status === 'error' || (!!errorMessage && isConnected);

  const button =
    status === 'locked' ? (
      <button
        type="button"
        className={['sqc-btn', 'sqc-connect-btn', className].filter(Boolean).join(' ')}
        onClick={() => openModal('connect')}
      >
        Unlock wallet
      </button>
    ) : showError && !isConnected ? (
      <button
        type="button"
        className={['sqc-btn', 'sqc-connect-btn', 'sqc-connect-btn--error', className]
          .filter(Boolean)
          .join(' ')}
        onClick={() => {
          clearError();
          openModal('connect');
        }}
        title={errorMessage ?? 'Connection error'}
      >
        {errorMessage ? 'Retry connect' : label}
      </button>
    ) : isConnected && address ? (
      <button
        type="button"
        className={[
          'sqc-btn',
          'sqc-account-chip',
          errorMessage ? 'sqc-account-chip--warn' : '',
          className,
        ]
          .filter(Boolean)
          .join(' ')}
        onClick={() => openModal('account')}
        aria-label={errorMessage ? `Account — ${errorMessage}` : 'Account'}
        title={errorMessage ?? undefined}
      >
        {balanceSPLD != null ? (
          <span className="sqc-account-chip__balance">{balanceSPLD} SPLD</span>
        ) : null}
        <span className="sqc-account-chip__addr">{truncateAddress(address)}</span>
      </button>
    ) : (
      <button
        type="button"
        className={['sqc-btn', 'sqc-connect-btn', className].filter(Boolean).join(' ')}
        onClick={() => openModal('connect')}
        disabled={status === 'connecting' || detecting}
      >
        {status === 'connecting' ? connectingLabel : detecting ? 'Detecting wallets…' : label}
      </button>
    );

  if (!mountModals) return button;

  return (
    <>
      {button}
      <ConnectModal />
      <AccountModal />
    </>
  );
}
