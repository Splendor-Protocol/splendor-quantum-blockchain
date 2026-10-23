import { expect, test, type Browser, type Page } from '@playwright/test';
import { E2E } from './env';
import { openTemplate, testLogin } from './helpers';

/** Template -> sign in -> Save to my account. Returns the new project id (kept in the URL). */
async function savedProject(page: Page, template = 'wallet-dashboard'): Promise<string> {
  await openTemplate(page, template);
  await testLogin(page, E2E.user);
  await page.reload();
  await page.getByTestId('tab-project').click();
  await page.getByRole('button', { name: 'Save to my account' }).click();
  await expect(page.getByTestId('save-status')).toHaveText('Saved to your account.');
  await page.keyboard.press('Escape');
  await expect(page).toHaveURL(/[?&]project=/);
  return new URL(page.url()).searchParams.get('project')!;
}

/** Renames the dApp and waits for the autosave of that edit to come back. */
async function renameAndSync(page: Page, projectId: string, name: string) {
  const put = page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().includes(`/projects/${projectId}`));
  await page.getByLabel('dApp name').fill(name);
  return put;
}

/** A second browser (its own storage), signed in as the same user, on the same project. */
async function secondTab(browser: Browser, projectId: string) {
  const context = await browser.newContext({ baseURL: E2E.web });
  const page = await context.newPage();
  await page.goto('/');
  await testLogin(page, E2E.user);
  await page.goto(`/build?project=${projectId}`);
  await expect(page.getByTestId('block-list')).toBeVisible();
  return { context, page };
}

test.describe('saving to the account', () => {
  test('the open project stays in the URL, so a reload reopens it from the account', async ({ page }) => {
    const id = await savedProject(page);
    await expect(page.getByTestId('save-chip')).toHaveText('Synced');
    expect((await renameAndSync(page, id, 'Reloaded dApp')).status()).toBe(200);
    await expect(page.getByTestId('save-chip')).toHaveText('Synced');
    await page.reload();
    await expect(page).toHaveURL(new RegExp(`project=${id}`));
    await expect(page.getByLabel('dApp name')).toHaveValue('Reloaded dApp');
    await expect(page.getByTestId('save-chip')).toHaveText('Synced');
  });

  test('opening a project does not save it again until something changes', async ({ page }) => {
    const id = await savedProject(page);
    const puts: string[] = [];
    page.on('request', (r) => {
      if (r.method() === 'PUT' && r.url().includes(`/projects/${id}`)) puts.push(r.url());
    });
    await page.reload();
    await expect(page.getByLabel('dApp name')).toHaveValue('Wallet Dashboard');
    await page.waitForTimeout(2_500);
    expect(puts).toEqual([]);
  });

  test('a save from a stale tab is a conflict, and the newer copy can be opened', async ({ page, browser }) => {
    const id = await savedProject(page);
    const other = await secondTab(browser, id);
    try {
      expect((await renameAndSync(other.page, id, 'Saved in tab B')).status()).toBe(200);

      expect((await renameAndSync(page, id, 'Edited in tab A')).status()).toBe(409);
      await expect(page.getByTestId('save-conflict')).toBeVisible();
      await expect(page.getByTestId('save-chip')).toHaveText('Conflict');

      await page.getByTestId('conflict-open-newer').click();
      await expect(page.getByLabel('dApp name')).toHaveValue('Saved in tab B');
      await expect(page.getByTestId('save-conflict')).toHaveCount(0);
    } finally {
      await other.context.close();
    }
  });

  test('"Keep my version" saves the stale tab on top of the newer copy', async ({ page, browser }) => {
    const id = await savedProject(page);
    const other = await secondTab(browser, id);
    try {
      expect((await renameAndSync(other.page, id, 'Saved in tab B')).status()).toBe(200);
    } finally {
      await other.context.close();
    }
    expect((await renameAndSync(page, id, 'Mine wins')).status()).toBe(409);
    await expect(page.getByTestId('save-conflict')).toBeVisible();
    const retry = page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().includes(`/projects/${id}`));
    await page.getByTestId('conflict-keep-mine').click();
    expect((await retry).status()).toBe(200);
    await expect(page.getByTestId('save-chip')).toHaveText('Synced');
    const saved = await page.request.get(`/api/projects/${id}`);
    expect((await saved.json()).name).toBe('Mine wins');
  });
});

test.describe('drafts are not lost', () => {
  test('a template start keeps the unsaved draft it replaces, and Restore brings it back', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByLabel('dApp name').fill('My unsaved jar');
    await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem('splendor-playground:draft:v1') ?? '{}').spec?.name)).toBe('My unsaved jar');

    await page.goto('/build?template=wallet-dashboard');
    await expect(page.getByLabel('dApp name')).toHaveValue('Wallet Dashboard');
    await expect(page.getByTestId('stash-notice')).toContainText('My unsaved jar');
    await page.getByTestId('stash-restore').click();
    await expect(page.getByLabel('dApp name')).toHaveValue('My unsaved jar');
    await expect(page.getByTestId('stash-notice')).toHaveCount(0);
  });
});

test.describe('project cards', () => {
  test('a browser project can be renamed and duplicated from its workspace', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');
    await page.getByLabel('New workspace name').fill('Cards');
    await page.getByRole('button', { name: 'New workspace' }).click();
    const wsCard = page.locator('[data-testid^="workspace-card-"]').first();
    const wsId = (await wsCard.getAttribute('data-testid'))!.replace('workspace-card-', '');

    await page.goto(`/build?workspace=${wsId}&template=tip-jar`);
    await page.getByTestId('add-transfer').click();
    await expect.poll(() => page.evaluate((id) => JSON.parse(localStorage.getItem('sqc-playground-workspace-projects-v1') ?? '{}')[id]?.length ?? 0, wsId)).toBe(1);

    await page.goto(`/?workspace=${wsId}`);
    const cards = page.locator('[data-testid^="project-card-"]');
    await expect(cards).toHaveCount(1);
    await cards.first().getByRole('button', { name: 'Rename project Tip Jar' }).click();
    await cards.first().getByLabel('New name for project Tip Jar').fill('Coffee fund');
    await page.keyboard.press('Enter');
    await expect(cards.first().getByRole('heading', { name: 'Coffee fund' })).toBeVisible();

    await cards.first().getByRole('button', { name: 'Duplicate project Coffee fund' }).click();
    await expect(cards).toHaveCount(2);
    await expect(page.getByRole('heading', { name: 'Coffee fund copy' })).toBeVisible();

    // The rename reached the spec: the studio opens it with the new name.
    await page.locator('[data-testid^="project-card-"]', { hasText: 'Coffee fund copy' }).getByRole('link', { name: 'Open' }).click();
    await expect(page.getByLabel('dApp name')).toHaveValue('Coffee fund copy');
  });
});
