import { describe, expect, it } from 'vitest';
import { sanitizeIconUrl } from '../widgets/utils.js';

describe('sanitizeIconUrl', () => {
  it('allows https and data:image', () => {
    expect(sanitizeIconUrl('https://example.com/icon.png')).toBe('https://example.com/icon.png');
    expect(sanitizeIconUrl('data:image/png;base64,abc')).toBe('data:image/png;base64,abc');
  });

  it('rejects javascript and http', () => {
    expect(sanitizeIconUrl('javascript:alert(1)')).toBeNull();
    expect(sanitizeIconUrl('http://insecure.example/x.png')).toBeNull();
    expect(sanitizeIconUrl(undefined)).toBeNull();
  });
});
