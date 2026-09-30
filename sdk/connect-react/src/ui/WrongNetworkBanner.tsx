/**
 * Wrong-network interstitial — shown while soft-switch is pending, when the app is not on
 * `expectedNetworkId`, or when the connected wallet (extension) is on a different chain.
 */
import { useNetwork } from '../react/hooks.js';
import { NetworkBadge } from './NetworkBadge.js';

export type WrongNetworkBannerProps = {
  className?: string;
  /** Expected network id; when set and current differs (and not switching), show switch CTA. */
  expectedNetworkId?: string;
  message?: string;
};

export function WrongNetworkBanner({
  className,
  expectedNetworkId,
  message,
}: WrongNetworkBannerProps) {
  const { networkId, switching, switchNetwork, network, walletMismatch, walletChainId, walletNetwork } =
    useNetwork();
  const cls = ['sqc-wrong-network', className].filter(Boolean).join(' ');

  if (switching) {
    return (
      <div className={cls} role="status">
        <span>Switching network…</span>
        <NetworkBadge switchable={false} />
      </div>
    );
  }

  if (expectedNetworkId && expectedNetworkId !== networkId) {
    return (
      <div className={cls} role="alert">
        <div className="sqc-wrong-network__msg">
          {message ??
            `This app expects ${expectedNetworkId}. You are on ${network?.name || networkId}.`}
        </div>
        <button
          type="button"
          className="sqc-btn"
          onClick={() => void switchNetwork(expectedNetworkId as Parameters<typeof switchNetwork>[0])}
        >
          Switch to {expectedNetworkId}
        </button>
        <NetworkBadge />
      </div>
    );
  }

  if (walletMismatch) {
    const walletLabel = walletNetwork?.name ?? walletChainId;
    const appLabel = network?.name || networkId;
    // With a pinned expected network the app must not follow the wallet; ask for a wallet switch.
    const canFollowWallet = !expectedNetworkId && !!walletNetwork;
    return (
      <div className={cls} role="alert" data-reason="wallet-network">
        <div className="sqc-wrong-network__msg">
          Your wallet is on {walletLabel}, but this app is on {appLabel}. Switch the network in
          your Splendor wallet{canFollowWallet ? ', or use the wallet’s network here' : ''} —
          transactions are blocked until they match.
        </div>
        {canFollowWallet ? (
          <button
            type="button"
            className="sqc-btn"
            onClick={() => void switchNetwork(walletNetwork!.id as Parameters<typeof switchNetwork>[0])}
          >
            Use {walletNetwork!.name}
          </button>
        ) : null}
        <NetworkBadge />
      </div>
    );
  }

  return null;
}
