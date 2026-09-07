import { NetworkBadge } from './NetworkBadge.js';
import { ConnectButton } from './ConnectButton.js';

export type ConnectKitProps = {
  /** Show network badge next to the connect button (default true). */
  showNetwork?: boolean;
  connectLabel?: string;
};

/**
 * Drop-in connect control: ConnectButton + optional NetworkBadge.
 * Modals are hosted by SplendorConnectProvider (`hostModals`, default true).
 */
export function ConnectKit({ showNetwork = true, connectLabel }: ConnectKitProps) {
  return (
    <div className="sqc-connect-kit">
      {showNetwork ? <NetworkBadge /> : null}
      <ConnectButton label={connectLabel} />
    </div>
  );
}
