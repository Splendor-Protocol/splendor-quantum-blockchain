import { expect, test, type Locator, type Page } from '@playwright/test';
import { E2E } from './env';
import { preview, testLogin } from './helpers';

// The main user journeys on phones and tablets (the `mobile` and `tablet` projects), not just layout.

const isNarrow = (page: Page) => (page.viewportSize()?.width ?? 1280) <= 1100;

async function noHorizontalScroll(page: Page) {
  await page.waitForTimeout(150);
  const { scroll, width } = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, width: window.innerWidth }));
  expect(scroll, `page is ${scroll}px wide in a ${width}px window`).toBeLessThanOrEqual(width);
}

/** Opens a template without waiting for the layers list, which sits in a closed drawer on narrow screens. */
async function openTemplate(page: Page, id: string) {
  await page.goto('/');
  await page.evaluate(() => localStorage.clear());
  await page.goto(`/build?template=${id}`);
  await expect(page.getByTestId('preview-frame')).toBeVisible();
}

/** The Components panel: a drawer on narrow screens, the sidebar otherwise. */
async function openComponents(page: Page) {
  await page.getByRole('tablist', { name: 'Studio panels' }).getByRole('tab', { name: 'Components' }).click();
  await expect(page.getByTestId('studio-side')).toBeVisible();
}

/** Clicks a top-bar control, opening "More" first only when the control lives in that menu at this width. */
async function clickTopBar(page: Page, target: Locator) {
  if (!(await target.isVisible())) await page.getByTestId('topbar-more').click();
  await target.click();
}

const openProjectTab = (page: Page) => clickTopBar(page, page.getByTestId('tab-project'));

test.describe('journeys on small screens', () => {
  test('home: a template card opens the builder with a working preview', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.getByTestId('template-wallet-dashboard').click();
    await expect(page).toHaveURL(/\/build/);
    await expect(preview(page).getByRole('heading', { name: 'Wallet dashboard' })).toBeVisible();
    await noHorizontalScroll(page);
  });

  test('gallery: a widget previews and is added to a new dApp', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/widgets');
    await page.getByTestId('gallery-transfer').click();
    await expect(page.getByRole('heading', { name: 'SPLD transfer', level: 1 })).toBeVisible();
    await expect(preview(page).getByText('Transfer SPLD')).toBeVisible();
    await noHorizontalScroll(page);
    await page.getByRole('button', { name: 'Add to my dApp' }).click();
    await expect(page).toHaveURL(/\/build\?add=transfer/);
    await page.getByTestId('pick-blank').click();
    await expect(preview(page).getByText('Transfer SPLD')).toBeVisible();
  });

  test('builder: add a component from the panel, then undo it', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    const transfers = preview(page).getByText('Transfer SPLD');
    await expect(transfers).toHaveCount(0);
    await openComponents(page);
    await page.getByTestId('add-transfer').click();
    if (isNarrow(page)) await expect(page.getByTestId('studio-side')).toBeHidden();
    await expect(transfers).toHaveCount(1);
    await clickTopBar(page, page.getByRole('button', { name: 'Undo' }));
    await expect(transfers).toHaveCount(0);
    await noHorizontalScroll(page);
  });

  // Tapping the canvas edits the page on small screens; "Preview app" is where the dApp is used.
  test('preview app: the test wallet sends a transfer through review', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await clickTopBar(page, page.getByRole('button', { name: 'Preview app' }));
    const dialog = page.getByRole('dialog', { name: 'Preview app' });
    await expect(dialog).toBeVisible();
    const frame = dialog.frameLocator('[data-testid="preview-frame"]');
    await frame.getByLabel('To (SQ:…)').fill(E2E.recipient);
    await frame.getByLabel('Amount (SPLD)').fill('1.5');
    await frame.getByRole('button', { name: 'Review', exact: true }).click();
    await frame.getByRole('button', { name: 'Confirm send' }).click();
    await dialog.getByTestId('preview-calls-summary').click();
    await expect(dialog.getByTestId('preview-calls')).toContainText('sendTransfer');
    await noHorizontalScroll(page);
  });

  test('saving: a signed-in user saves to the account and the project stays in the URL', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await testLogin(page, E2E.user);
    await page.reload();
    await expect(page.getByTestId('preview-frame')).toBeVisible();
    await openProjectTab(page);
    await page.getByRole('button', { name: 'Save to my account' }).click();
    await expect(page.getByTestId('save-status')).toHaveText('Saved to your account.');
    await noHorizontalScroll(page);
    await page.keyboard.press('Escape');
    await expect(page).toHaveURL(/[?&]project=/);
  });

  test('export: the dialog opens, fits the screen and closes on Escape', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await clickTopBar(page, page.getByTestId('tab-export'));
    const dialog = page.getByRole('dialog', { name: 'Export' });
    await expect(dialog).toBeVisible();
    const box = (await dialog.boundingBox())!;
    const width = page.viewportSize()!.width;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width + 1);
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
  });

  test('home: workspaces can be created on a phone', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');
    await page.getByLabel('New workspace name').fill('Phone space');
    await page.getByRole('button', { name: 'New workspace' }).click();
    await expect(page.locator('[data-testid^="workspace-card-"]', { hasText: 'Phone space' })).toBeVisible();
    await noHorizontalScroll(page);
  });
});
