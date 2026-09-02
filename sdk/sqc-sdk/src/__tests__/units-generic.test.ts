import { describe, expect, it } from 'vitest';
import { fromUnits, toUnits } from '../units.js';

describe('fromUnits / toUnits', () => {
  it('round-trips 18 decimals like SPLD', () => {
    expect(fromUnits('1', 18)).toBe('1000000000000000000');
    expect(toUnits('1000000000000000000', 18)).toBe('1.0');
    expect(fromUnits('1.5', 18)).toBe('1500000000000000000');
  });

  it('handles 6 decimals', () => {
    expect(fromUnits('1.25', 6)).toBe('1250000');
    expect(toUnits('1250000', 6)).toBe('1.25');
  });

  it('handles 0 decimals', () => {
    expect(fromUnits('42', 0)).toBe('42');
    expect(toUnits('42', 0)).toBe('42');
  });
});
