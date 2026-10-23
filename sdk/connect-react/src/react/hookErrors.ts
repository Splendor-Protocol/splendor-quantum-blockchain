import { BridgeError } from '@splendor/sqc-sdk';

/**
 * Error for a hook action called too early. With `missing` (for example `'token'`), the target
 * address is not set yet (`INVALID_PARAMS`); otherwise no wallet or read connection is ready
 * (`NOT_CONNECTED`). `message` stays technical; the hint is the UI copy.
 */
export function notReady(message: string, missing?: string | null): BridgeError {
  return missing
    ? new BridgeError('INVALID_PARAMS', message, undefined, `Set the ${missing} address first`)
    : new BridgeError('NOT_CONNECTED', message, undefined, 'Connect your wallet to continue');
}
