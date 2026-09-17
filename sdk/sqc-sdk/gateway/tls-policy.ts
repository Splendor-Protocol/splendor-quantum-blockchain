/** Hybrid group only — served when a probe handshake actually negotiated it. */
export const HYBRID_TLS_GROUP = 'X25519MLKEM768';
/** Classical fallbacks served only under ALLOW_CLASSICAL_TLS. */
export const CLASSICAL_TLS_GROUPS = 'X25519:P-256';

export type TlsGroupDecision =
  | { ok: true; groups: string; mode: 'hybrid-enforced' | 'classical-opt-in' }
  | { ok: false; reason: string };

/**
 * Decide which TLS groups to serve. A successful hybrid probe is required
 * before advertising X25519MLKEM768; OpenSSL's version string alone is not
 * evidence the group exists. When the probe succeeds the hybrid group is the
 * only one offered, so each connection negotiates PQ key exchange rather than
 * silently falling back to X25519/P-256.
 */
export function decideTlsGroups(input: {
  probeGroup: string | null;
  allowClassical: boolean;
}): TlsGroupDecision {
  const probed = /mlkem|kyber/i.test(input.probeGroup ?? '');
  if (probed) {
    return { ok: true, groups: HYBRID_TLS_GROUP, mode: 'hybrid-enforced' };
  }
  if (input.allowClassical) {
    return { ok: true, groups: CLASSICAL_TLS_GROUPS, mode: 'classical-opt-in' };
  }
  return {
    ok: false,
    reason:
      'this Node build did not negotiate X25519MLKEM768 (needs OpenSSL 3.5+ with the group compiled in)',
  };
}
