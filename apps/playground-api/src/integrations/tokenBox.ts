import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto';

const VERSION = 'v1';

/** 32-byte key from `INTEGRATION_TOKEN_KEY`: base64 (44 chars) or hex (64 chars). Null when not a valid key. */
export function parseTokenKey(raw: string | undefined): Buffer | null {
  const v = raw?.trim();
  if (!v) return null;
  const key = /^[0-9a-fA-F]{64}$/.test(v) ? Buffer.from(v, 'hex') : Buffer.from(v, 'base64');
  return key.length === 32 ? key : null;
}

/**
 * AES-256-GCM with the owner and provider as associated data: a sealed token copied to another
 * user's row (or another provider) does not open.
 */
export function sealToken(key: Buffer, token: string, owner: string, provider: string): string {
  const iv = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, iv);
  cipher.setAAD(Buffer.from(`${owner}\n${provider}`));
  const ct = Buffer.concat([cipher.update(token, 'utf8'), cipher.final()]);
  return [VERSION, iv.toString('base64url'), cipher.getAuthTag().toString('base64url'), ct.toString('base64url')].join('.');
}

/** The plain token, or null when the value was not sealed for this owner and provider with this key. */
export function openToken(key: Buffer, sealed: string, owner: string, provider: string): string | null {
  const [version, iv, tag, ct] = sealed.split('.');
  if (version !== VERSION || !iv || !tag || !ct) return null;
  try {
    const decipher = createDecipheriv('aes-256-gcm', key, Buffer.from(iv, 'base64url'));
    decipher.setAAD(Buffer.from(`${owner}\n${provider}`));
    decipher.setAuthTag(Buffer.from(tag, 'base64url'));
    return Buffer.concat([decipher.update(Buffer.from(ct, 'base64url')), decipher.final()]).toString('utf8');
  } catch {
    return null;
  }
}
