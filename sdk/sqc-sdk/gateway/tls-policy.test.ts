import { describe, expect, it } from 'vitest';
import {
  CLASSICAL_TLS_GROUPS,
  HYBRID_TLS_GROUP,
  decideTlsGroups,
} from './tls-policy.ts';

describe('gateway TLS group policy', () => {
  it('enforces hybrid-only groups after a successful probe', () => {
    const d = decideTlsGroups({ probeGroup: 'X25519MLKEM768', allowClassical: false });
    expect(d.ok).toBe(true);
    if (d.ok) {
      expect(d.mode).toBe('hybrid-enforced');
      expect(d.groups).toBe(HYBRID_TLS_GROUP);
      expect(d.groups).not.toMatch(/X25519:P-256/);
    }
  });

  it('does not treat OpenSSL version as a substitute for a probe', () => {
    const d = decideTlsGroups({ probeGroup: null, allowClassical: false });
    expect(d.ok).toBe(false);
  });

  it('serves classical groups only under ALLOW_CLASSICAL_TLS', () => {
    const d = decideTlsGroups({ probeGroup: null, allowClassical: true });
    expect(d.ok).toBe(true);
    if (d.ok) {
      expect(d.mode).toBe('classical-opt-in');
      expect(d.groups).toBe(CLASSICAL_TLS_GROUPS);
      expect(d.groups).not.toContain('MLKEM');
    }
  });
});
