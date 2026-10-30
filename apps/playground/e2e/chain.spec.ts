import { readFile } from 'node:fs/promises';
import { expect, test } from '@playwright/test';
import JSZip from 'jszip';
import { E2E } from './env';
import { mockCalls, openTemplate, preview, testLogin } from './helpers';

test.describe('chain-native widgets (mock wallet)', () => {
  test('NFT membership: preview the gallery, send a pass, export the project and share it', async ({ page, browser }) => {
    await openTemplate(page, 'nft-membership');
    const frame = preview(page);

    // Public gallery: 12 ids, the mock wallet owns #1-#3.
    await expect(frame.getByTestId('dapp-nft721')).toHaveCount(12);
    await expect(frame.locator('[data-testid="dapp-nft721"][data-owned="true"]')).toHaveCount(3);

    // Members page: the NFT panel sends an owned pass; receive shows a QR; activity lists history.
    await frame.getByRole('navigation', { name: 'dApp pages' }).getByRole('link', { name: 'Members' }).click();
    const panel = frame.locator('[data-sqc-widget="nft"]');
    await expect(panel.locator('[data-testid="sqc-nft"][data-owned="true"]')).toHaveCount(3);
    await expect(frame.locator('svg.sqc-qr')).toBeVisible();
    await expect(frame.getByTestId('sqc-activity-item').first()).toBeVisible();
    await panel.getByRole('button', { name: 'Token #1 Yours', exact: true }).click();
    await panel.getByLabel('To (SQ:…)', { exact: true }).fill(E2E.recipient);
    await panel.getByRole('button', { name: 'Send NFT #1' }).click();
    await expect(panel.locator('[data-testid="sqc-nft"][data-owned="true"]')).toHaveCount(2);
    await expect(await mockCalls(page)).toContainText('sendModuleCall');

    // Export: the QRC-721 gallery block is copied and the widgets are imported from the package.
    await page.getByTestId('tab-export').click();
    await page.getByRole('group', { name: 'Framework' }).getByRole('button', { name: 'Vite + React' }).click();
    const [download] = await Promise.all([page.waitForEvent('download'), page.getByTestId('export-zip').click()]);
    const zip = await JSZip.loadAsync(await readFile((await download.path())!));
    const names = Object.keys(zip.files);
    expect(names.some((n) => n.endsWith('src/blocks/Nft721Gallery.tsx'))).toBe(true);
    const pages = await Promise.all(names.filter((n) => /Page\.tsx$/.test(n)).map((n) => zip.file(n)!.async('string')));
    const source = pages.join('\n');
    for (const tag of ['<Nft721Gallery', '<NftPanel', '<ReceiveWidget', '<AddressBook', '<ActivityFeed']) expect(source).toContain(tag);
    await page.getByRole('dialog', { name: 'Export' }).getByRole('button', { name: 'Close' }).click();

    // Share: a visitor sees the public gallery from the share link.
    await testLogin(page, E2E.user);
    await page.reload();
    await page.getByTestId('tab-project').click();
    await page.getByRole('button', { name: 'Save to my account' }).click();
    await expect(page.getByTestId('save-status')).toHaveText('Saved to your account.');
    await page.getByRole('button', { name: 'Create share link' }).click();
    const shareUrl = await page.locator('#share-url').inputValue();
    const visitor = await browser.newPage();
    await visitor.goto(shareUrl);
    await expect(preview(visitor).getByTestId('dapp-nft721')).toHaveCount(12);
    await visitor.close();
  });

  test('validator directory and x402 paywall run against the mock chain', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await page.getByTestId('tab-project').click();
    await page.getByText('Spec JSON').click();
    const spec = {
      version: 1,
      name: 'Chain tools',
      network: 'testnet',
      theme: { base: 'dark', overrides: {} },
      pages: [
        {
          path: '/',
          title: 'Home',
          blocks: [
            { id: 'vals', widget: 'validator-list', props: { maxRows: 10 } },
            { id: 'pay', widget: 'x402-paywall', props: { payTo: E2E.recipient, price: '0.25', maxPrice: '1', content: 'Premium alpha' } },
          ],
        },
      ],
    };
    await page.locator('#import-spec').fill(JSON.stringify(spec));
    await page.getByTestId('import-spec').click();
    const frame = preview(page);
    await expect(frame.getByTestId('sqc-validator')).toHaveCount(3);
    await expect(frame.getByText('3 active · bond 2500 SPLD')).toBeVisible();
    await frame.getByRole('button', { name: 'Unlock' }).click();
    await expect(frame.getByTestId('dapp-x402-quote')).toContainText('0.25 SPLD');
    await frame.getByRole('button', { name: 'Pay and unlock' }).click();
    await expect(frame.getByTestId('dapp-x402-content')).toContainText('Premium alpha');
    await expect(await mockCalls(page)).toContainText('payX402');
  });
});
