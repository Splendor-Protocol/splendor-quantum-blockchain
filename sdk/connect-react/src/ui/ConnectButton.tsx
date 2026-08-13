import { useAccount, useBalance, useConnect } from '../react/hooks.js';
import { ConnectModal } from './ConnectModal.js';
import { AccountModal } from './AccountModal.js';

function truncateAddress(addr: string, left = 6, right = 4): string {
  if (addr.length <= left + right + 1) return addr;
  return `${addr.slice(0, left)}…${addr.slice(-right)}`;
}

export type ConnectButtonProps = {
  label?: string;
  className?: string;
  /**
   * When false, only render the button (host must mount ConnectModal/AccountModal).
   * Default true — self-contained drop-in.
   */
  includeModals?: boolean;
};

export function ConnectButton({
  label = 'Connect',
  className,
  includeModals = true,
}: ConnectButtonProps) {
  const { openModal, status, connectingStep } = useConnect();
  const { address, isConnected } = useAccount();
  const { balanceSPLD } = useBalance();

  const connectingLabel =
    connectingStep === 'init'
      ? 'Loading signer…'
      : connectingStep === 'sign'
        ? 'Sign in wallet…'
        : 'Connecting…';

  const button =
    isConnected && address ? (
      <button
        type="button"
        className={['sqc-btn', 'sqc-account-chip', className].filter(Boolean).join(' ')}
        onClick={() => openModal('account')}
        aria-label="Account"
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
        disabled={status === 'connecting'}
      >
        {status === 'connecting' ? connectingLabel : label}
      </button>
    );

  if (!includeModals) return button;

  return (
    <>
      {button}
      <ConnectModal />
      <AccountModal />
    </>
  );
}
