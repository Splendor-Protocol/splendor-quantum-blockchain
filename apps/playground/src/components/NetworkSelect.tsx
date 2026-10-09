import { useNetworkSwitch } from '@splendor/connect-react';
import type { NetworkId } from '@splendor/dapp-spec';
import { PLAYGROUND_NETWORKS } from '../config';

/** Switches the live wallet provider's network (persisted by the provider). */
export function NetworkSelect() {
  const { networkId, switchNetwork, switching } = useNetworkSwitch();
  return (
    <label className="pg-network">
      <span className="pg-sr-only">Network</span>
      <select
        aria-label="Network"
        value={networkId}
        disabled={switching}
        onChange={(e) => void switchNetwork(e.target.value as NetworkId)}
      >
        {PLAYGROUND_NETWORKS.map((n) => (
          <option key={n.id} value={n.id}>
            {n.label}
          </option>
        ))}
      </select>
    </label>
  );
}
