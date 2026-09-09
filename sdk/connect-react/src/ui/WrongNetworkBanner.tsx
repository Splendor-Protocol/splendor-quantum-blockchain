/**
 * Wrong-network interstitial — shown while soft-switch is pending or network label is ambiguous.
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
  const { networkId, switching, switchNetwork, network } = useNetwork();

  if (switching) {
    return (
      <div className={['sqc-wrong-network', className].filter(Boolean).join(' ')} role="status">
        <span>Switching network…</span>
        <NetworkBadge switchable={false} />
      </div>
    );
  }

  if (!expectedNetworkId || expectedNetworkId === networkId) {
    return null;
  }

  return (
    <div className={['sqc-wrong-network', className].filter(Boolean).join(' ')} role="alert">
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
