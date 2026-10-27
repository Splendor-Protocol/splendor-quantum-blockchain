import { expect, test } from '@playwright/test';
import { openTemplate, preview } from './helpers';

const isDev = process.env.E2E_DEV === '1';

test.describe('production protections', () => {
  test.skip(isDev, 'the dev server has no CSP and a same-origin preview');

  test('pages are served with the deploy CSP and no violations occur', async ({ page }) => {
    const violations: string[] = [];
    page.on('console', (m) => {
      if (/Content Security Policy|Refused to/i.test(m.text())) violations.push(m.text());
    });
    const res = await page.goto('/build?template=wallet-dashboard');
    const csp = res!.headers()['content-security-policy'] ?? '';
    expect(csp).toContain("script-src 'self' 'wasm-unsafe-eval'");
    expect(csp).toContain("frame-ancestors 'self'");
    expect(res!.headers()['x-content-type-options']).toBe('nosniff');
    await expect(preview(page).getByRole('heading', { name: 'Wallet dashboard' })).toBeVisible();
    expect(violations).toEqual([]);
  });

  test('the preview iframe has an opaque origin and cannot reach the playground', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await expect(page.getByTestId('preview-frame')).toHaveAttribute('data-isolated', 'true');
    await expect(page.getByTestId('preview-frame')).not.toHaveAttribute('sandbox', /allow-same-origin/);
    await expect(preview(page).getByRole('heading', { name: 'Wallet dashboard' })).toBeVisible();

    const frame = page.frames().find((f) => /\/preview(\.html)?(#|$)/.test(f.url()))!;
    const probe = await frame.evaluate(() => {
      const attempt = (fn: () => unknown) => {
        try {
          fn();
          return 'allowed';
        } catch {
          return 'blocked';
        }
      };
      return {
        origin: self.origin,
        parentDom: attempt(() => window.parent.document.title),
        storage: attempt(() => window.localStorage.length),
        cookies: attempt(() => document.cookie),
      };
    });
    expect(probe).toEqual({ origin: 'null', parentDom: 'blocked', storage: 'blocked', cookies: 'blocked' });
  });
});
