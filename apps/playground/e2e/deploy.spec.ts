import { expect, test } from '@playwright/test';
import { E2E } from './env';
import { openTemplate, testLogin } from './helpers';

test.describe('deploy through the user\'s own GitHub', () => {
  test('export, connect GitHub, push, and the Vercel import link points at the new repository', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await testLogin(page, E2E.user);
    await page.reload();
    await expect(page.getByTestId('block-list')).toBeVisible();
    await page.getByTestId('tab-export').click();
    const panel = page.getByTestId('export-panel');
    await expect(panel.getByTestId('github-connect')).toBeVisible();

    // The fake GitHub approves at once and sends the popup back to github-connected.html.
    const popupOpened = page.waitForEvent('popup');
    await panel.getByTestId('github-connect').click();
    const popup = await popupOpened;
    await popup.waitForLoadState();
    await expect(panel.getByTestId('github-account')).toContainText('e2e-user');

    await panel.locator('#github-repo').fill('e2e-dapp');
    await panel.getByTestId('github-push-button').click();
    await expect(panel.getByTestId('github-result')).toContainText('e2e-user/e2e-dapp');
    await expect(panel.getByTestId('github-deploy-vercel')).toHaveAttribute(
      'href',
      `https://vercel.com/new/import?s=${encodeURIComponent('https://github.com/e2e-user/e2e-dapp')}`,
    );
    await expect(panel.getByTestId('github-deploy-netlify')).toHaveAttribute('href', /app\.netlify\.com\/start\/deploy\?repository=https%3A%2F%2Fgithub\.com%2Fe2e-user%2Fe2e-dapp/);

    const pushes = (await (await page.request.get(`${E2E.fake}/__pushes`)).json()) as { repo: string; files: string[] }[];
    const last = pushes.at(-1)!;
    expect(last.repo).toBe('e2e-dapp');
    expect(last.files).toEqual(expect.arrayContaining(['package.json', 'vercel.json', 'netlify.toml', '.github/workflows/ci.yml', 'src/App.tsx']));

    // Disconnecting forgets the account.
    await panel.getByRole('button', { name: 'Disconnect' }).click();
    await expect(panel.getByTestId('github-connect')).toBeVisible();
  });

  test('signed out, the export says how to push instead', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByTestId('tab-export').click();
    await expect(page.getByTestId('export-panel').getByTestId('github-signin-hint')).toBeVisible();
  });
});
