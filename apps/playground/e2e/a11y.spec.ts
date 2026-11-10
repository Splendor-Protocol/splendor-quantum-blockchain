import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';
import { E2E } from './env';
import { testLogin } from './helpers';

// Automated WCAG 2.1 A/AA scan of every route, on desktop and in the mobile and tablet projects.
// Serious and critical findings fail the run; minor/moderate ones are listed in the report.

async function scan(page: Page, label: string) {
  // Let fonts, the preview frame and lazy routes settle before scanning.
  await page.waitForTimeout(300);
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    // The preview iframe renders the user's own dApp; it is scanned through the gallery and export tests.
    .exclude('[data-testid="preview-frame"]')
    .analyze();
  const blocking = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  const summary = blocking.map(
    (v) =>
      `${v.id} (${v.impact}): ${v.help}\n` +
      v.nodes
        .slice(0, 3)
        .map((n) => `  ${n.target.join(' ')}\n    ${(n.failureSummary ?? '').split('\n').slice(0, 3).join(' | ')}`)
        .join('\n'),
  );
  expect(summary, `${label}: accessibility violations`).toEqual([]);
}

test.describe('accessibility', () => {
  for (const path of ['/', '/widgets', '/widgets/transfer', '/learn/guide/widgets', '/build']) {
    test(`${path} has no serious accessibility violations`, async ({ page }) => {
      await page.goto('/');
      await page.evaluate(() => localStorage.clear());
      await page.goto(path);
      await expect(page.locator('#main')).toBeVisible();
      await scan(page, path);
    });
  }

  test('the studio has no serious accessibility violations', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/build?template=wallet-dashboard');
    await expect(page.getByTestId('preview-frame')).toBeVisible();
    await scan(page, 'studio');
  });

  test('the admin pages have no serious accessibility violations', async ({ page }) => {
    await testLogin(page, E2E.admin);
    for (const path of ['/admin/catalog', '/admin/templates', '/admin/audit']) {
      await page.goto(path);
      await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible();
      await scan(page, path);
    }
  });
});
