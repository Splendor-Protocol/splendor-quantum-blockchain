import { readFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import JSZip from 'jszip';
import { preview } from './helpers';

/** Fresh browser storage, then the studio on a blank page. */
async function openBlankStudio(page: Page) {
  await page.goto('/');
  await page.evaluate(() => localStorage.clear());
  await page.goto('/build');
  await page.getByTestId('pick-blank').click();
  await expect(page.getByTestId('block-list')).toBeVisible();
}

async function openTemplateStudio(page: Page, id: string) {
  await page.goto('/');
  await page.evaluate(() => localStorage.clear());
  await page.goto(`/build?template=${id}`);
  await expect(page.getByTestId('block-list')).toBeVisible();
}

const rail = (page: Page, name: 'Pages' | 'Components' | 'Theme' | 'Contracts' | 'Flows') =>
  page.getByRole('navigation', { name: 'Studio panels' }).getByRole('button', { name });

/** The spec the studio autosaves to localStorage. */
async function draftSpec(page: Page) {
  return page.evaluate(() => JSON.parse(localStorage.getItem('splendor-playground:draft:v1') ?? 'null')?.spec ?? null);
}

test.describe('studio', () => {
  test('opens, adds a hero by test id, and switches the theme panel', async ({ page }) => {
    await openBlankStudio(page);
    const blocks = page.getByTestId('block-list').locator('li');
    await expect(blocks).toHaveCount(1);

    await page.getByTestId('add-hero').click();
    await expect(blocks).toHaveCount(2);

    // `tab-theme` is a visually hidden test hook (the visible control is the Theme rail button).
    await page.getByTestId('tab-theme').dispatchEvent('click');
    await expect(page.getByText('Theme presets')).toBeVisible();
    const midnight = page.getByRole('button', { name: /Midnight/ });
    await midnight.click();
    await expect(midnight).toHaveAttribute('aria-pressed', 'true');
    await expect.poll(async () => (await draftSpec(page))?.theme?.preset).toBe('midnight');
    expect((await draftSpec(page)).theme.base).toBe('dark');

    // Back to the components rail: the hero is still there.
    await rail(page, 'Components').click();
    await expect(blocks).toHaveCount(2);
  });

  test('export zip carries contracts.ts and actions.ts (and vendor files when the SDK is vendored)', async ({ page }) => {
    await openTemplateStudio(page, 'wallet-dashboard');
    await page.getByTestId('tab-export').click();
    await page.getByRole('group', { name: 'Framework' }).getByRole('button', { name: 'Vite + React' }).click();
    const [download] = await Promise.all([page.waitForEvent('download'), page.getByTestId('export-zip').click()]);
    const zip = await JSZip.loadAsync(await readFile((await download.path())!));
    const names = Object.keys(zip.files);

    expect(names.some((n) => /(^|\/)src\/contracts\.ts$/.test(n))).toBe(true);
    expect(names.some((n) => /(^|\/)src\/actions\.ts$/.test(n))).toBe(true);
    const vendored = names.some((n) => /(^|\/)vendor\/@splendor\//.test(n));
    const prefix = names.find((n) => n.endsWith('package.json') && !n.includes('/vendor/'))!.replace(/package\.json$/, '');
    const pkg = JSON.parse(await zip.file(`${prefix}package.json`)!.async('string'));
    if (vendored) {
      expect(pkg.dependencies['@splendor/connect-react']).toBe('file:vendor/@splendor/connect-react');
      expect(pkg.dependencies['@splendor/sqc-sdk']).toBe('file:vendor/@splendor/sqc-sdk');
    } else {
      expect(pkg.dependencies['@splendor/connect-react']).toMatch(/^\^\d+\.\d+\.\d+/);
    }
    const actions = await zip.file(`${prefix}src/actions.ts`)!.async('string');
    expect(actions).toContain('export async function runAction');
    const contracts = await zip.file(`${prefix}src/contracts.ts`)!.async('string');
    expect(contracts).toContain('export const CONTRACTS');
  });

  test('pages panel: duplicate a page and make it the home page', async ({ page }) => {
    await openTemplateStudio(page, 'token-launchpad');
    await rail(page, 'Pages').click();

    await page.getByRole('button', { name: 'Duplicate page Manage' }).click();
    const chips = page.locator('.pg-chips');
    await expect(chips.getByRole('button', { name: 'Manage copy /manage-copy' })).toBeVisible();

    await page.getByRole('button', { name: 'Set Manage as home page' }).first().click();
    await expect(chips.getByRole('button', { name: 'Manage /' })).toBeVisible();
    await expect(chips.getByRole('button', { name: 'Launch /launch' })).toBeVisible();
    await expect(page.getByTestId('page-is-home')).toHaveCount(1);
  });

  test('flows panel: clicking a link selects the button that holds it', async ({ page }) => {
    await openTemplateStudio(page, 'landing');
    await rail(page, 'Flows').click();
    await expect(page.getByTestId('flow-edge').first()).toBeVisible();
    await page.getByTestId('flow-link').first().click();
    await expect(page.getByTestId('block-list').locator('li.is-active')).toHaveCount(1);
  });

  test('contracts panel: Try describes the call', async ({ page }) => {
    await openBlankStudio(page);
    await rail(page, 'Contracts').click();
    await page.getByLabel('Name', { exact: true }).fill('Counter');
    await page.getByLabel('Address').fill(`SQ:${'ab'.repeat(32)}`);
    await page.getByRole('button', { name: 'Add contract' }).click();
    await page.getByRole('button', { name: 'Try get()' }).click();
    await expect(page.getByTestId('contract-try-result')).toContainText('Would call get() on SQ:');
    await expect(page.getByTestId('contract-try-result')).toContainText('in mock mode');
  });
});

test.describe('onboarding', () => {
  test('is skipped under automation by default', async ({ page }) => {
    await openBlankStudio(page);
    await expect(page.getByTestId('onboarding')).toHaveCount(0);
  });

  test('three coach marks, remembered with sqc-studio-onboarded', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => {
      localStorage.clear();
      localStorage.setItem('sqc-studio-onboarding-force', '1');
    });
    await page.goto('/build');
    await page.getByTestId('pick-blank').click();
    const tour = page.getByTestId('onboarding');
    await expect(tour).toBeVisible();
    await expect(page.getByTestId('onboarding-step')).toHaveText('Step 1 of 3');
    // The tour does not block the studio underneath.
    await page.getByTestId('add-hero').click();
    await page.getByTestId('onboarding-next').click();
    await expect(page.getByTestId('onboarding-step')).toHaveText('Step 2 of 3');
    await page.getByTestId('onboarding-next').click();
    await expect(page.getByTestId('onboarding-step')).toHaveText('Step 3 of 3');
    await page.getByTestId('onboarding-next').click();
    await expect(tour).toHaveCount(0);
    expect(await page.evaluate(() => localStorage.getItem('sqc-studio-onboarded'))).toBe('1');
    await page.reload();
    await expect(page.getByTestId('block-list')).toBeVisible();
    await expect(page.getByTestId('onboarding')).toHaveCount(0);
  });
});

test.describe('workspaces', () => {
  test('wizard creates a project in a workspace and the workspace lists it', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');

    await page.getByLabel('New workspace name').fill('Team A');
    await page.getByRole('button', { name: 'New workspace' }).click();
    const card = page.locator('[data-testid^="workspace-card-"]').first();
    await expect(card).toContainText('Team A');
    await card.getByRole('button', { name: 'Open' }).click();
    await expect(page).toHaveURL(/[?&]workspace=local-/);
    const workspaceId = new URL(page.url()).searchParams.get('workspace')!;
    await expect(page.getByTestId('workspace-projects')).toContainText('Projects in Team A');
    await expect(page.getByTestId('workspace-projects-empty')).toBeVisible();

    // template -> theme -> name
    await page.getByRole('button', { name: 'New project here' }).click();
    await expect(page.getByTestId('wizard')).toBeVisible();
    await expect(page.getByTestId('wizard-next')).toBeDisabled();
    await page.getByTestId('wizard-template-tip-jar').click();
    await page.getByTestId('wizard-next').click();
    await page.getByTestId('wizard-theme-mint').click();
    await page.getByTestId('wizard-next').click();
    await page.getByTestId('wizard-name').fill('Coffee jar');
    await page.getByTestId('wizard-create').click();

    await expect(page).toHaveURL(/\/build/);
    await expect(page).toHaveURL(new RegExp(`workspace=${workspaceId}`));
    await expect(page.getByLabel('dApp name')).toHaveValue('Coffee jar');
    await expect.poll(async () => (await draftSpec(page))?.theme?.preset).toBe('mint');
    await expect(preview(page).getByRole('heading', { name: 'Buy me a coffee' })).toBeVisible();

    // An edit files the project under the workspace (autosaved in this browser).
    await page.getByTestId('add-transfer').click();
    await expect
      .poll(() => page.evaluate((id) => JSON.parse(localStorage.getItem('sqc-playground-workspace-projects-v1') ?? '{}')[id]?.length ?? 0, workspaceId))
      .toBe(1);

    await page.goto(`/?workspace=${workspaceId}`);
    await expect(page.locator('[data-testid^="project-card-"]')).toHaveCount(1);
    await expect(page.locator('[data-testid^="project-card-"]').first()).toContainText('Coffee jar');

    // And it opens again from the grid.
    await page.locator('[data-testid^="project-card-"]').first().getByRole('link', { name: 'Open' }).click();
    await expect(page.getByLabel('dApp name')).toHaveValue('Coffee jar');
    await expect(page.getByTestId('block-list').locator('li')).toHaveCount(4);
  });

  test('template cards still go straight to the studio', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');
    await page.getByTestId('template-tip-jar').click();
    await expect(page).toHaveURL(/\/build/);
    await expect(page.getByLabel('dApp name')).toHaveValue('Tip Jar');
  });
});
