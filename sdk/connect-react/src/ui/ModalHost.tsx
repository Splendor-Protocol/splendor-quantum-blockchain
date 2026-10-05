import { AccountModal } from './AccountModal.js';
import { ConnectModal } from './ConnectModal.js';
import { SignApprovalModal } from './SignApprovalModal.js';

/** Single modal host for SplendorConnectProvider. */
export function ModalHost() {
  return (
    <>
      <ConnectModal />
      <AccountModal />
      <SignApprovalModal />
    </>
  );
}
