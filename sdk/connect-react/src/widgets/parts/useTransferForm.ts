import { useCallback, useEffect, useRef, useState } from 'react';
import { getRecentRecipients, rememberRecipient, reviewTransfer, type TransferReview } from '../utils.js';
import { COMMON_LABELS, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';
import { addressFieldError, amountFieldError } from './inputs.js';

export type UseTransferFormOptions = {
  /** Spendable balance in the same unit as the amount (SPLD, or whole tokens). */
  balance?: string | null;
  symbol?: string;
  /** Connected address; sending to it is blocked. */
  self?: string | null;
  defaultTo?: string;
  defaultAmount?: string;
  /** Maximum fractional digits (18 for SPLD, token decimals for QRC-20). */
  decimals?: number;
  /**
   * Anything that invalidates an open review (chain id, token address, …). When it or
   * `self` changes, the review closes so the user never confirms stale details.
   */
  contextKey?: string;
  labels?: Partial<CommonWidgetLabels>;
};

export type TransferForm = {
  to: string;
  setTo: (value: string) => void;
  amount: string;
  setAmount: (value: string) => void;
  /** Fill the amount with the full balance (no-op while the balance is unknown). */
  setMax: () => void;
  /** Result of the same checks the widgets run before review. */
  review: TransferReview;
  /** Per-field messages for display; null when empty or valid. */
  errors: { to: string | null; amount: string | null };
  canReview: boolean;
  reviewing: boolean;
  startReview: () => void;
  cancelReview: () => void;
  /** Recently used recipients, newest first. */
  recents: string[];
  /** Store a recipient after a successful send. */
  remember: (address: string) => void;
  /** Back to the default values. */
  reset: () => void;
};

/**
 * Headless state and validation behind TransferWidget and TokenPanel: SQ: recipient,
 * self-send, decimal amount, balance, review step and recent recipients.
 */
export function useTransferForm({
  balance = null,
  symbol = 'SPLD',
  self = null,
  defaultTo = '',
  defaultAmount = '',
  decimals,
  contextKey = '',
  labels,
}: UseTransferFormOptions = {}): TransferForm {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  const [to, setToState] = useState(defaultTo);
  const [amount, setAmountState] = useState(defaultAmount);
  const [reviewingState, setReviewing] = useState(false);
  const [recents, setRecents] = useState<string[]>(() => getRecentRecipients());
  const toEdited = useRef(false);
  const amountEdited = useRef(false);

  useEffect(() => {
    setReviewing(false);
  }, [self, contextKey]);

  // Follow new defaults (e.g. a different invoice) until the user types in that field.
  useEffect(() => {
    if (toEdited.current) return;
    setToState(defaultTo);
    setReviewing(false);
  }, [defaultTo]);
  useEffect(() => {
    if (amountEdited.current) return;
    setAmountState(defaultAmount);
    setReviewing(false);
  }, [defaultAmount]);

  const setTo = useCallback((v: string) => {
    toEdited.current = true;
    setToState(v);
    setReviewing(false);
  }, []);
  const setAmount = useCallback((v: string) => {
    amountEdited.current = true;
    setAmountState(v);
    setReviewing(false);
  }, []);

  const review = reviewTransfer({ to, amount, balance, self, decimals });
  const errors = {
    to: addressFieldError(to, self, l),
    amount: amountFieldError(amount, balance, symbol, l, decimals),
  };

  return {
    to,
    setTo,
    amount,
    setAmount,
    setMax: () => {
      if (balance != null) setAmount(balance);
    },
    review,
    errors,
    canReview: review.ok,
    reviewing: reviewingState && review.ok,
    startReview: () => {
      if (review.ok) setReviewing(true);
    },
    cancelReview: () => setReviewing(false),
    recents,
    remember: (address: string) => setRecents(rememberRecipient(address)),
    reset: () => {
      toEdited.current = false;
      amountEdited.current = false;
      setToState(defaultTo);
      setAmountState(defaultAmount);
      setReviewing(false);
    },
  };
}
