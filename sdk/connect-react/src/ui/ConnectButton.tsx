import { useAccount, useBalance, useConnect } from '../react/hooks.js';

function truncateAddress(addr: string, left = 6, right = 4): string {
  if (addr.length <= left + right + 1) return addr;
  return `${addr.slice(0, left)}…${addr.slice(-right)}`;
}

export type ConnectButtonProps = {
  label?: string;
  className?: string;
};

export function ConnectButton({ label = 'Connect', className }: ConnectButtonProps) {
  const { openModal, status } = useConnect();
  const { address, isConnected } = useAccount();
  const { balanceSPLD } = useBalance();

  if (isConnected && address) {
    return (
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
    );
  }

  return (
    <button
      type="button"
      className={['sqc-btn', 'sqc-connect-btn', className].filter(Boolean).join(' ')}
      onClick={() => openModal('connect')}
      disabled={status === 'connecting'}
    >
      {status === 'connecting' ? 'Connecting…' : label}
    </button>
  );
}
