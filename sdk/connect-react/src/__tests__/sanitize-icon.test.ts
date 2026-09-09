import { describe, expect, it } from 'vitest';
import { sanitizeIconUrl } from '../widgets/utils.js';

describe('sanitizeIconUrl', () => {
  it('allows https and raster data:image', () => {
    expect(sanitizeIconUrl('https://example.com/icon.png')).toBe('https://example.com/icon.png');
    expect(sanitizeIconUrl('data:image/png;base64,abc')).toBe('data:image/png;base64,abc');
    expect(sanitizeIconUrl('data:image/jpeg;base64,abc')).toBe('data:image/jpeg;base64,abc');
    expect(sanitizeIconUrl('data:image/webp;base64,abc')).toBe('data:image/webp;base64,abc');
  });

  it('rejects javascript, http, and SVG data URIs', () => {
    expect(sanitizeIconUrl('javascript:alert(1)')).toBeNull();
    expect(sanitizeIconUrl('http://insecure.example/x.png')).toBeNull();
    expect(sanitizeIconUrl('data:image/svg+xml;base64,PHN2Zy8+')).toBeNull();
    expect(sanitizeIconUrl('data:image/svg+xml,<svg></svg>')).toBeNull();
    expect(sanitizeIconUrl(undefined)).toBeNull();
  });
});
