/**
 * ML-DSA Sign-In recipe — domain-separated auth messages (not eth_siwe).
 * Message building and checks live in `@splendor/sqc-sdk` (servers use `@splendor/sqc-sdk/auth`
 * for `verifySignIn`); they are re-exported here so the connect-react API is unchanged.
 */
import type { SIGN_MESSAGE_DOMAIN } from '@splendor/sqc-sdk';

export {
  createAuthMessage,
  parseAuthMessage,
  checkAuthMessage,
  authCheckMessage,
  type AuthMessageParams,
  type ParsedAuthMessage,
  type AuthCheckReason,
  type AuthCheckResult,
  type AuthCheckOptions,
} from '@splendor/sqc-sdk';

export type SignedAuth = {
  message: string;
  signatureB64: string;
  publicKeyB64: string;
  address: string;
  /** Domain used for unstructured fallbacks (always SIGN_MESSAGE_DOMAIN family). */
  messageDomain: typeof SIGN_MESSAGE_DOMAIN | 'Splendor Connect v2';
};
