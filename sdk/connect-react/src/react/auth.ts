/**
 * ML-DSA Sign-In recipe — domain-separated auth messages (not eth_siwe).
 * Server verifies via wallet.verifyMessage / WASM verify path.
 */
import { SIGN_MESSAGE_DOMAIN } from '@splendor/sqc-sdk';

export type AuthMessageParams = {
  /** Relying-party origin, e.g. https://app.example.com */
  domain: string;
  /** Application / site name shown to the user */
  statement?: string;
  /** Optional opaque nonce from the server */
  nonce?: string;
  /** Unix seconds; defaults to now */
  issuedAt?: number;
  /** Optional expiry (unix seconds) */
  expirationTime?: number;
  /** Chain / network id string */
  chainId?: string;
  /** SQ address binding (optional; often filled after connect) */
  address?: string;
};

/**
 * Build a Connect-style auth challenge for `wallet.signMessage`.
 * Already uses the Splendor Connect v2 prefix so prepareSignMessage leaves it intact.
 */
export function createAuthMessage(params: AuthMessageParams): string {
  const issuedAt = params.issuedAt ?? Math.floor(Date.now() / 1000);
  const lines = [
    'Splendor Connect v2',
    `Sign in to ${params.domain}`,
  ];
  if (params.statement) lines.push(params.statement);
  if (params.address) lines.push(`Address: ${params.address}`);
  if (params.chainId) lines.push(`Chain ID: ${params.chainId}`);
  if (params.nonce) lines.push(`Nonce: ${params.nonce}`);
  lines.push(`Issued At: ${issuedAt}`);
  if (params.expirationTime != null) lines.push(`Expiration Time: ${params.expirationTime}`);
  return lines.join('\n');
}

export type SignedAuth = {
  message: string;
  signatureB64: string;
  publicKeyB64: string;
  address: string;
  /** Domain used for unstructured fallbacks (always SIGN_MESSAGE_DOMAIN family). */
  messageDomain: typeof SIGN_MESSAGE_DOMAIN | 'Splendor Connect v2';
};
