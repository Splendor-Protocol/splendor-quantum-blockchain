import { ConnectButton } from './ConnectButton.js';
import { ConnectModal } from './ConnectModal.js';
import { AccountModal } from './AccountModal.js';

/**
 * Drop-in: ConnectButton + connect/account modals.
 * Place once under SplendorConnectProvider (e.g. in the app shell).
 */
export function ConnectKit() {
  return (
    <>
      <ConnectButton />
      <ConnectModal />
      <AccountModal />
    </>
  );
}
