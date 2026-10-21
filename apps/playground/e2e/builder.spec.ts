import { readFile } from 'node:fs/promises';
import { expect, test } from '@playwright/test';
import JSZip from 'jszip';
import { E2E } from './env';
import { mockCalls, openTemplate, preview } from './helpers';

test.describe('templates in the builder (mock wallet)', () => {
  test('home page template opens in the builder and renders in the preview', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.getByTestId('template-wallet-dashboard').click();
    await expect(page).toHaveURL(/\/build/);
    await expect(preview(page).getByRole('heading', { name: 'Wallet dashboard' })).toBeVisible();
    await expect(page.getByTestId('spec-issues')).toHaveCount(0);
  });

  test('wallet dashboard: SPLD transfer goes through review and reaches the wallet', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    const frame = preview(page);
    await frame.getByLabel('To (SQ:…)').fill(E2E.recipient);
    await frame.getByLabel('Amount (SPLD)').fill('1.5');
    await frame.getByRole('button', { name: 'Review', exact: true }).click();
    await frame.getByRole('button', { name: 'Confirm send' }).click();
    await expect(await mockCalls(page)).toContainText('sendTransfer');
    await expect(await mockCalls(page)).toContainText(E2E.recipient);
  });

  test('wallet dashboard: no wallet shows the connect hint', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await page.getByRole('group', { name: 'Mock wallet state' }).getByRole('button', { name: 'No wallet' }).click();
    await expect(preview(page).getByText('Connect a wallet to send SPLD.')).toBeVisible();
  });

  test('tip jar: setup field blocks export until filled, then tips and exports both frameworks', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await expect(page.getByTestId('spec-issues')).toContainText('Tip jar → Recipient');
    await page.getByTestId('tab-export').click();
    await expect(page.getByTestId('export-zip')).toBeDisabled();
    // Export opens as a modal overlay in the studio; close it before editing blocks.
    await page.getByRole('dialog', { name: 'Export' }).getByRole('button', { name: 'Close' }).click();

    await page.getByTestId('block-list').getByRole('button', { name: /tip jar/i }).first().click();
    await page.locator('#prop-tip-jar-recipient').fill(E2E.recipient);
    await expect(page.getByTestId('spec-issues')).toHaveCount(0);

    const frame = preview(page);
    await frame.getByRole('button', { name: 'Send tip' }).click();
    await expect(await mockCalls(page)).toContainText('sendTransfer');

    await page.getByTestId('tab-export').click();
    for (const [framework, marker] of [
      ['Vite + React', 'vite.config.ts'],
      ['Next.js', 'next.config.mjs'],
    ] as const) {
      await page.getByRole('group', { name: 'Framework' }).getByRole('button', { name: framework }).click();
      const [download] = await Promise.all([page.waitForEvent('download'), page.getByTestId('export-zip').click()]);
      expect(download.suggestedFilename()).toMatch(/\.zip$/);
      const zip = await JSZip.loadAsync(await readFile((await download.path())!));
      const names = Object.keys(zip.files);
      const prefix = names.find((n) => n.endsWith('package.json'))!.replace(/package\.json$/, '');
      expect(names).toContain(`${prefix}${marker}`);
      const pkg = JSON.parse(await zip.file(`${prefix}package.json`)!.async('string'));
      expect(pkg.dependencies['@splendor/connect-react']).toMatch(/^\^\d+\.\d+\.\d+/);
      expect(pkg.dependencies['@splendor/sqc-sdk']).toMatch(/^\^\d+\.\d+\.\d+/);
      const sources = await Promise.all(names.filter((n) => /Page\.tsx$/.test(n)).map((n) => zip.file(n)!.async('string')));
      expect(sources.join('\n')).toContain(E2E.recipient);
    }
  });

  test('members area: wallet sign-in unlocks the members content', async ({ page }) => {
    await openTemplate(page, 'members-area');
    const frame = preview(page);
    await frame.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(frame.getByTestId('dapp-members-content')).toContainText("members-only update");
    await expect(await mockCalls(page)).toContainText('signMessage');
  });

  test('token launchpad: deploy goes through review and the second page renders', async ({ page }) => {
    await openTemplate(page, 'token-launchpad');
    const frame = preview(page);
    await frame.getByRole('button', { name: 'Review deploy' }).click();
    await frame.getByRole('button', { name: 'Confirm deploy' }).click();
    await expect(await mockCalls(page)).toContainText(/deployContract|sendModuleCall/);

    await page.getByRole('button', { name: /Manage/ }).first().click();
    await expect(frame.getByLabel('Token (SQ:…)')).toBeVisible();
  });

  test('editing: add, remove and undo blocks', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/build');
    await page.getByTestId('pick-blank').click();
    const blocks = page.getByTestId('block-list').locator('li');
    await expect(blocks).toHaveCount(1);
    await page.getByTestId('add-transfer').click();
    await expect(blocks).toHaveCount(2);
    await expect(preview(page).getByText('Transfer SPLD')).toBeVisible();
    await page.getByRole('button', { name: 'Remove SPLD transfer' }).click();
    await expect(blocks).toHaveCount(1);
    await page.getByRole('button', { name: 'Undo' }).click();
    await expect(blocks).toHaveCount(2);
  });
});

test.describe('gallery', () => {
  test('a widget previews in isolation and can be added to a dApp', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/widgets');
    await page.getByTestId('gallery-transfer').click();
    await expect(page.getByRole('heading', { name: 'SPLD transfer', level: 1 })).toBeVisible();
    await expect(preview(page).getByText('Transfer SPLD')).toBeVisible();
    await page.getByRole('button', { name: 'Add to my dApp' }).click();
    await expect(page).toHaveURL(/\/build\?add=transfer/);
    await page.getByTestId('pick-blank').click();
    await expect(page.getByTestId('block-list').getByRole('button', { name: 'SPLD transfer', exact: true })).toBeVisible();
    await expect(preview(page).getByText('Transfer SPLD')).toBeVisible();
  });
});
