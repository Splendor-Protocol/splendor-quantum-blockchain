import { useNetwork } from '../react/hooks.js';

export type NetworkBadgeProps = {
  className?: string;
};

export function NetworkBadge({ className }: NetworkBadgeProps) {
  const { network } = useNetwork();
  if (!network) return null;
  return (
    <span className={['sqc-network-badge', className].filter(Boolean).join(' ')}>
      <span className="sqc-network-badge__dot" aria-hidden />
      {network.name || network.id}
    </span>
  );
}
