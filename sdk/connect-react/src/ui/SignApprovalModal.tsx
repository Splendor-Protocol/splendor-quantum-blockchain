import { useEffect } from 'react';
import { useConnectController, useConnectState } from '../react/context.js';
import { ModalShell } from './ModalShell.js';

const KIND_TITLE: Record<string, string> = {
  transfer: 'Approve transfer',
  call: 'Approve contract call',
  deploy: 'Approve deploy',
  module: 'Approve transaction',
  message: 'Sign message',
  sign: 'Approve signature',
};

/**
 * Asks the user before the in-page (seed) key signs anything. Mounted by `ModalHost`;
 * mount it yourself when the provider has `hostModals={false}`.
 */
export function SignApprovalModal() {
  const ctrl = useConnectController();
  const req = useConnectState((s) => s.signRequest ?? null);
  const address = useConnectState((s) => s.account?.address ?? null);
  const network = useConnectState((s) => s.network);

  useEffect(() => {
    if (ctrl.isStub || typeof ctrl.attachSignApprovalUi !== 'function') return;
    return ctrl.attachSignApprovalUi();
  }, [ctrl]);

  const answer = (ok: boolean) => ctrl.resolveSignRequest?.(ok);

  return (
    <ModalShell
      open={req != null}
      title={req ? (KIND_TITLE[req.kind] ?? 'Approve signature') : ''}
      subtitle="Your key in this page will sign this request."
      onClose={() => answer(false)}
    >
      {req ? (
        <div className="sqc-sign-approval" data-testid="sqc-sign-approval" data-kind={req.kind}>
          <p className="sqc-sign-approval__summary">{req.summary}</p>
          <div className="sqc-sign-approval__rows">
            {address ? (
              <div className="sqc-account-row">
                <span className="sqc-account-row__label">From</span>
                <div className="sqc-account-row__value">
                  <code>{address}</code>
                </div>
              </div>
            ) : null}
            {req.to ? (
              <div className="sqc-account-row">
                <span className="sqc-account-row__label">To</span>
                <div className="sqc-account-row__value">
                  <code>{req.to}</code>
                </div>
              </div>
            ) : null}
            {req.amount ? (
              <div className="sqc-account-row">
                <span className="sqc-account-row__label">Amount</span>
                <div className="sqc-account-row__value">{req.amount}</div>
              </div>
            ) : null}
            {network ? (
              <div className="sqc-account-row">
                <span className="sqc-account-row__label">Network</span>
                <div className="sqc-account-row__value sqc-sign-approval__network">
                  <span>{network.name}</span>
                  <code>{network.chainId}</code>
                </div>
              </div>
            ) : null}
          </div>
          {req.message != null ? (
            <pre className="sqc-sign-approval__message" tabIndex={0}>
              {req.message}
            </pre>
          ) : null}
          <div className="sqc-modal__actions">
            <button type="button" className="sqc-btn sqc-btn--ghost" onClick={() => answer(false)}>
              Reject
            </button>
            <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => answer(true)}>
              Approve
            </button>
          </div>
        </div>
      ) : null}
    </ModalShell>
  );
}
