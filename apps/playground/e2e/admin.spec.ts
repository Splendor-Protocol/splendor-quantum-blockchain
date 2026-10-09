import { expect, test } from '@playwright/test';
import { E2E } from './env';
import { openTemplate, testLogin } from './helpers';

test.describe('accounts and sharing', () => {
  test('a signed-in user saves a project, shares it, and a visitor can remix it', async ({ page, browser }) => {
    await openTemplate(page, 'wallet-dashboard');
    await testLogin(page, E2E.user);
    await page.reload();
    await page.getByTestId('tab-project').click();
    await page.getByRole('button', { name: 'Save to my account' }).click();
    await expect(page.getByTestId('save-status')).toHaveText('Saved to your account.');
    await page.getByRole('button', { name: 'Create share link' }).click();
    const shareUrl = await page.locator('#share-url').inputValue();
    expect(shareUrl).toMatch(/\/s\/[\w-]+$/);

    const visitor = await browser.newPage();
    await visitor.goto(shareUrl);
    await expect(visitor.getByRole('heading', { name: 'Wallet Dashboard', level: 1 })).toBeVisible();
    await visitor.getByRole('button', { name: /Remix/ }).or(visitor.getByRole('link', { name: /Remix/ })).click();
    await expect(visitor).toHaveURL(/\/build/);
    await expect(visitor.getByTestId('block-list')).toContainText('SPLD transfer');
    await visitor.close();
  });
});

test.describe('admin', () => {
  test('a non-admin wallet is refused', async ({ page }) => {
    await page.goto('/admin');
    await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible();
    await testLogin(page, E2E.user);
    await page.reload();
    await expect(page.getByText('This wallet is not an admin.')).toBeVisible();
    const res = await page.request.put('/api/admin/catalog/transfer', { data: { hidden: true }, headers: { origin: E2E.web } });
    expect(res.status()).toBe(403);
  });

  // Flags are global to the suite's API, so put the widget back even when an assertion fails.
  test.afterEach(async ({ page }) => {
    await testLogin(page, E2E.admin);
    await page.request.put('/api/admin/catalog/transfer', { data: {}, headers: { origin: E2E.web } });
  });

  test('hiding a widget removes it from the builder catalog and is audited', async ({ page }) => {
    await testLogin(page, E2E.admin);
    await page.goto('/admin/catalog');
    const show = page.getByRole('checkbox', { name: 'Show SPLD transfer' });
    await expect(show).toBeChecked();
    const saved = page.waitForResponse((r) => r.url().endsWith('/api/admin/catalog/transfer') && r.request().method() === 'PUT');
    await show.click();
    expect((await saved).ok()).toBeTruthy();
    await expect(show).not.toBeChecked();

    await page.evaluate(() => localStorage.clear());
    await page.goto('/build');
    await page.getByTestId('pick-blank').click();
    await expect(page.getByTestId('add-account-card')).toBeVisible();
    await expect(page.getByTestId('add-transfer')).toHaveCount(0);

    await page.goto('/admin/catalog');
    const restored = page.waitForResponse((r) => r.url().endsWith('/api/admin/catalog/transfer') && r.request().method() === 'PUT');
    await page.getByRole('checkbox', { name: 'Show SPLD transfer' }).click();
    expect((await restored).ok()).toBeTruthy();
    await expect(page.getByRole('checkbox', { name: 'Show SPLD transfer' })).toBeChecked();

    await page.goto('/admin/audit');
    await expect(page.getByTestId('audit-log')).toContainText('catalog.flag');
    await expect(page.getByTestId('audit-log')).toContainText('transfer');
  });

  test('release dashboard shows versions and changesets, and dispatches a dry run', async ({ page }) => {
    await testLogin(page, E2E.admin);
    await page.goto('/admin/releases');
    const packages = page.getByTestId('release-packages');
    await expect(packages).toContainText('@splendor/connect-react');
    await expect(packages.getByRole('row', { name: /@splendor\/connect-react/ })).toContainText('3.1.0');
    await expect(packages.getByRole('row', { name: /@splendor\/dapp-spec/ })).toContainText('not published');
    await expect(page.getByText('widget-manifest.md')).toBeVisible();

    await expect(page.locator('#release-dry')).toBeChecked();
    await page.getByTestId('trigger-release').click();
    await expect(page.getByTestId('release-result')).toContainText('Dry run started');

    const dispatches = (await (await page.request.get(`${E2E.fake}/__dispatches`)).json()) as { workflow: string; body: { ref: string; inputs: Record<string, string> } }[];
    expect(dispatches.at(-1)).toMatchObject({
      workflow: 'release-packages.yml',
      body: { ref: 'main', inputs: { channel: 'next', dry_run: 'true' } },
    });

    await page.goto('/admin/audit');
    await expect(page.getByTestId('audit-log')).toContainText('release.dispatch');
  });
});
