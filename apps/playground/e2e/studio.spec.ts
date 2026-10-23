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
  page.getByRole('tablist', { name: 'Studio panels' }).getByRole('tab', { name });

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

    await rail(page, 'Theme').click();
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

test.describe('simplified studio', () => {
  test('layers: Alt+Arrow moves a component, Delete removes it, containers are not counted', async ({ page }) => {
    await openTemplateStudio(page, 'wallet-dashboard');
    const tree = page.getByTestId('block-list');
    const titles = tree.locator('li .pg-layer-title');
    await expect(tree.locator('li')).toHaveCount(4);
    const before = await titles.allTextContents();

    await tree.getByRole('button', { name: 'SPLD transfer', exact: true }).click();
    await expect(tree.locator('li.is-active')).toHaveCount(1);
    await page.keyboard.press('Alt+ArrowUp');
    const moved = [...before];
    const at = moved.indexOf('SPLD transfer');
    [moved[at - 1], moved[at]] = [moved[at]!, moved[at - 1]!];
    await expect(titles).toHaveText(moved);
    // The row keeps keyboard focus after it moves.
    await expect(tree.getByRole('button', { name: 'SPLD transfer', exact: true })).toBeFocused();

    await page.keyboard.press('Delete');
    await expect(tree.locator('li')).toHaveCount(3);
  });

  test('plain words: test wallet / real wallet, Content tab, and one network picker', async ({ page }) => {
    await openTemplateStudio(page, 'wallet-dashboard');
    const mode = page.getByRole('group', { name: 'Preview mode' });
    await expect(mode.getByRole('button', { name: 'Test wallet' })).toHaveAttribute('aria-pressed', 'true');
    await expect(mode.getByRole('button', { name: 'Real wallet' })).toBeVisible();
    await expect(page.getByLabel('dApp network')).toBeVisible();
    // The playground header's own network select is hidden in the studio.
    await expect(page.getByRole('banner').first().getByLabel('Network', { exact: true })).toHaveCount(0);
    await expect(page.getByText('Click a component on the page to edit it.')).toBeVisible();
    await page.getByTestId('block-list').getByRole('button', { name: 'SPLD transfer', exact: true }).click();
    await expect(page.getByRole('tab', { name: 'Content' })).toBeVisible();
  });

  test('finish setup lists what is missing and keeps the issue texts', async ({ page }) => {
    await openTemplateStudio(page, 'tip-jar');
    const card = page.getByTestId('spec-issues');
    await expect(card).toContainText('Finish setup');
    await expect(card).toContainText('Tip jar → Recipient');
  });

  test('page settings: who can see a page and whether it is in the navigation', async ({ page }) => {
    await openBlankStudio(page);
    await rail(page, 'Pages').click();
    // With nothing selected the inspector shows the same settings: use the Pages panel's.
    const panel = page.getByRole('tabpanel', { name: 'Pages' });
    await expect(panel.getByLabel('Who can see this page')).toBeDisabled();
    await expect(panel.getByText('The home page is always public')).toBeVisible();

    await page.getByRole('button', { name: '+ Add page' }).click();
    await page.getByLabel('New page title').fill('About');
    await page.getByRole('button', { name: 'Add blank', exact: true }).click();
    await panel.getByLabel('Who can see this page').selectOption('wallet');
    await panel.getByLabel('Show in navigation').uncheck();
    await expect.poll(async () => (await draftSpec(page))?.pages?.find((p: { path: string }) => p.path === '/about')?.access).toBe('wallet');
    await expect.poll(async () => (await draftSpec(page))?.nav?.links).toEqual(['/']);
    await panel.getByLabel('Show in navigation').check();
    await expect.poll(async () => (await draftSpec(page))?.nav?.links).toBeUndefined();
  });

  test('data pickers: a contract block picks from the dApp contracts and takes its address from them', async ({ page }) => {
    await openBlankStudio(page);
    await rail(page, 'Contracts').click();
    await page.getByLabel('Name', { exact: true }).fill('Counter');
    await page.getByLabel('Address').fill(`SQ:${'ab'.repeat(32)}`);
    await page.getByRole('button', { name: 'Add contract' }).click();
    await rail(page, 'Components').click();
    await page.getByTestId('add-contract-read').click();

    const contract = page.locator('#prop-contract-read-contractId');
    await expect(contract).toBeVisible();
    await contract.selectOption({ label: 'Counter' });
    await expect(page.getByText('Address comes from Counter')).toBeVisible();
    await expect(page.locator('#prop-contract-read-functionSig')).toContainText('get()');
    await page.getByRole('button', { name: '+ Add contract' }).click();
    await expect(rail(page, 'Contracts')).toHaveAttribute('aria-selected', 'true');
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

  test('a browser workspace can be renamed, and the new name survives a reload', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');

    await page.getByLabel('New workspace name').fill('Team A');
    await page.getByRole('button', { name: 'New workspace' }).click();
    const card = page.locator('[data-testid^="workspace-card-"]').first();
    await expect(card).toContainText('Team A');
    await card.getByRole('button', { name: 'Open workspace Team A' }).click();
    await expect(page.getByTestId('workspace-projects')).toContainText('Projects in Team A');

    // Escape cancels without saving.
    await card.getByRole('button', { name: 'Rename workspace Team A' }).click();
    await card.getByLabel('New name for workspace Team A').fill('Discarded');
    await page.keyboard.press('Escape');
    await expect(card.getByRole('heading', { name: 'Team A' })).toBeVisible();

    await card.getByRole('button', { name: 'Rename workspace Team A' }).click();
    await card.getByLabel('New name for workspace Team A').fill('Team Rocket');
    await page.keyboard.press('Enter');
    await expect(card.getByRole('heading', { name: 'Team Rocket' })).toBeVisible();
    await expect(page.getByTestId('workspace-projects')).toContainText('Projects in Team Rocket');

    await page.reload();
    await expect(page.locator('[data-testid^="workspace-card-"]').first().getByRole('heading', { name: 'Team Rocket' })).toBeVisible();
    expect(await page.evaluate(() => JSON.parse(localStorage.getItem('sqc-playground-workspaces-v1') ?? '{}').workspaces?.[0]?.name)).toBe('Team Rocket');
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

test.describe('studio selection and dialogs', () => {
  test('selecting a container via the breadcrumb shows its style fields and they reach the preview', async ({ page }) => {
    await openTemplateStudio(page, 'landing');
    await preview(page).locator('[data-block-id]').first().click();
    const crumbs = page.getByTestId('canvas-breadcrumb');
    await expect(crumbs).toBeVisible();
    // The last crumb is the block; the one before it is its container.
    const buttons = crumbs.getByRole('button');
    const count = await buttons.count();
    await buttons.nth(count - 2).click();
    await expect(page.getByTestId('inspector-container')).toBeVisible();
    await page.locator('#style-maxWidth').fill('480px');
    await expect.poll(() => preview(page).locator('[data-node-id]').evaluateAll((els) => els.some((e) => (e as HTMLElement).style.maxWidth === '480px'))).toBe(true);
  });

  test('new project wizard: focus moves in, Escape closes it and focus returns to the opener', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');
    const opener = page.getByTestId('new-project');
    await opener.click();
    const dialog = page.getByRole('dialog', { name: 'New project' });
    await expect(dialog).toBeVisible();
    await expect.poll(() => dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(opener).toBeFocused();
  });
});
