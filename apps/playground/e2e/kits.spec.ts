import { expect, test, type Page } from '@playwright/test';
import { mockCalls, preview } from './helpers';

/** The spec the studio autosaves to localStorage. */
async function draftSpec(page: Page) {
  return page.evaluate(() => JSON.parse(localStorage.getItem('splendor-playground:draft:v1') ?? 'null')?.spec ?? null);
}

/** Home -> Token project kit -> keep the demo token -> theme -> name -> Create. Lands in the studio. */
async function createTokenProject(page: Page, name = 'Demo Token App') {
  await page.goto('/');
  await page.evaluate(() => localStorage.clear());
  await page.goto('/');
  await page.getByTestId('kit-token-project').click();
  await expect(page.getByRole('dialog', { name: 'New project' })).toBeVisible();
  // The kit is preselected, so the wizard opens on its setup step with the demo token filled in.
  await expect(page.getByTestId('wizard-setup')).toBeVisible();
  await expect(page.getByTestId('setup-token-demo')).toBeChecked();
  await page.getByTestId('wizard-next').click();
  await page.getByTestId('wizard-theme-mint').click();
  await page.getByTestId('wizard-next').click();
  await page.getByTestId('wizard-name').fill(name);
  await page.getByTestId('wizard-create').click();
  await expect(page).toHaveURL(/\/build/);
  await expect(page.getByTestId('block-list')).toBeVisible();
}

/**
 * Opens the full-screen preview from the studio top bar.
 * Depends on the Studio wiring: a top-bar button named "Preview app".
 */
async function openPreviewApp(page: Page) {
  await page.getByRole('button', { name: 'Preview app' }).click();
  const dialog = page.getByRole('dialog', { name: 'Preview app' });
  await expect(dialog).toBeVisible();
  // The studio's own canvas frame is still mounted underneath: always look inside the dialog.
  return { dialog, frame: dialog.frameLocator('[data-testid="preview-frame"]') };
}

test.describe('kits: home and wizard', () => {
  test('home shows the two kits first, and the template cards still link straight to the studio', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');
    const kits = page.getByTestId('home-kits');
    await expect(kits.getByRole('heading', { name: 'Start with a ready-made app' })).toBeVisible();
    await expect(kits.getByTestId('kit-token-project')).toContainText('Landing (public)');
    await expect(kits.getByTestId('kit-token-project')).toContainText('Dashboard (wallet)');
    await expect(kits.getByTestId('kit-contract-app')).toContainText('Dashboard (signed in)');
    await expect(page.getByTestId('template-tip-jar')).toHaveAttribute('href', /\/build\?template=tip-jar/);
  });

  test('wizard: a real token address is validated, and the contract slot can be skipped', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');
    await page.getByTestId('kit-token-project').click();
    await page.getByTestId('setup-token-demo').uncheck();
    await expect(page.getByTestId('setup-token-address')).toHaveValue('');
    await page.getByTestId('wizard-next').click();
    await expect(page.getByTestId('wizard-setup').getByRole('alert')).toContainText('token address');
    await page.getByTestId('setup-token-address').fill('SQ:123');
    await page.getByTestId('wizard-next').click();
    await expect(page.getByTestId('wizard-setup').getByRole('alert')).toContainText('64 hex');
    const mine = `SQ:${'ab'.repeat(32)}`;
    await page.getByTestId('setup-token-address').fill(mine);
    // The optional app contract: leave the demo off and empty to skip it.
    await page.getByTestId('setup-contract-demo').uncheck();
    await page.getByTestId('wizard-next').click();
    await page.getByTestId('wizard-next').click();
    await page.getByTestId('wizard-create').click();
    await expect(page.getByTestId('block-list')).toBeVisible();
    await expect.poll(async () => JSON.stringify(await draftSpec(page))).toContain(mine);
    const spec = await draftSpec(page);
    expect(spec.contracts.find((c: { id: string }) => c.id === 'token').address).toBe(mine);
  });

  test('wizard: the contract app kit turns a pasted function list into one form per write function', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/');
    await page.getByTestId('kit-contract-app').click();
    await page.getByTestId('setup-contract-demo').uncheck();
    await page.getByTestId('setup-contract-address').fill(`SQ:${'cd'.repeat(32)}`);
    await page.getByTestId('setup-contract-abi').fill('total() read\nstake(uint256 amount) write\nclaim() write');
    await page.getByTestId('setup-contract-name').fill('Staking');
    await page.getByTestId('wizard-next').click();
    await page.getByTestId('wizard-next').click();
    await page.getByTestId('wizard-create').click();
    await expect(page.getByTestId('block-list')).toBeVisible();
    await expect(page.getByLabel('dApp name')).toHaveValue('Staking');
    await expect
      .poll(async () => {
        const spec = await draftSpec(page);
        return spec?.pages?.flatMap((p: { blocks?: { widget: string; props: { functionSig?: string } }[] }) => p.blocks ?? []).filter((b: { widget: string }) => b.widget === 'contract-form').map((b: { props: { functionSig?: string } }) => b.props.functionSig);
      })
      .toEqual(['stake(uint256)', 'claim()']);
  });
});

test.describe('kits: token project', () => {
  test('creates the Home + Dashboard pages and the landing shows the token', async ({ page }) => {
    await createTokenProject(page);
    await expect.poll(async () => (await draftSpec(page))?.pages?.map((p: { title: string }) => p.title)).toEqual(['Home', 'Dashboard']);
    expect((await draftSpec(page)).pages[1].access).toBe('wallet');
    // The test wallet answers reads for the demo token, so the stats show the DEMO symbol.
    await expect(preview(page).locator('[data-block-id="stats"]')).toContainText('DEMO', { timeout: 30_000 });
  });

  test('changing the token once in Contracts updates every component bound to it', async ({ page }) => {
    await createTokenProject(page);
    await page.getByRole('tab', { name: 'Pages' }).click();
    await page.getByRole('button', { name: /Dashboard \/app/ }).first().click();
    // The test wallet holds 25,000 of the demo token, and 1,000 of any other token.
    const myToken = preview(page).locator('[data-block-id="my-token"]');
    await expect(myToken).toContainText('25,000', { timeout: 30_000 });

    await page.getByRole('tab', { name: 'Contracts' }).click();
    const other = `SQ:${'ab'.repeat(32)}`;
    await page.getByRole('textbox', { name: 'Demo Token address' }).fill(other);
    await page.getByRole('button', { name: 'Save' }).first().click();

    await expect(myToken).toContainText('1,000', { timeout: 30_000 });
    await expect(myToken).not.toContainText('25,000');
    const spec = await draftSpec(page);
    expect(spec.contracts.find((c: { id: string }) => c.id === 'token').address).toBe(other);
    const blocks = spec.pages.flatMap((p: { blocks: { id: string; props: Record<string, unknown> }[] }) => p.blocks);
    expect(blocks.find((b: { id: string }) => b.id === 'send-token').props.tokenAddress).toBe(other);
    expect(blocks.find((b: { id: string }) => b.id === 'my-token').props.contractId).toBe('token');
  });

  // The next two need the Studio wiring: a top-bar button named "Preview app" that opens <PreviewApp />.
  test('preview app: Launch app opens the dashboard, and without a wallet the gate asks to connect', async ({ page }) => {
    await createTokenProject(page);
    const { dialog, frame } = await openPreviewApp(page);

    await expect(dialog.getByRole('group', { name: 'Test wallet', exact: true }).getByRole('button', { name: 'Connected' })).toHaveAttribute('aria-pressed', 'true');
    await expect(frame.getByRole('heading', { name: 'Meet the Demo Token' })).toBeVisible({ timeout: 30_000 });
    await frame.getByText('Launch app').first().click();
    await expect(frame.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
    await expect(dialog.getByTestId('preview-crumb')).toHaveText(['Home', 'Dashboard']);
    await expect(dialog.getByTestId('preview-hint')).toHaveCount(0);

    await dialog.getByRole('group', { name: 'Test wallet', exact: true }).getByRole('button', { name: 'No wallet' }).click();
    await expect(frame.getByText('Connect your wallet')).toBeVisible();
    await expect(dialog.getByTestId('preview-hint')).toContainText('This page needs a connected wallet');

    // Restart flow: back on the home page.
    await dialog.getByRole('button', { name: 'Restart flow' }).click();
    await expect(frame.getByRole('heading', { name: 'Meet the Demo Token' })).toBeVisible();
    await expect(dialog.getByTestId('preview-crumb')).toHaveText(['Home']);

    // Escape closes the dialog and focus goes back to the opener.
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Preview app' })).toBeFocused();
  });

  test('preview app: the contract form on the dashboard submits through the test wallet', async ({ page }) => {
    await createTokenProject(page);
    const { dialog, frame } = await openPreviewApp(page);
    await expect(frame.getByRole('heading', { name: 'Meet the Demo Token' })).toBeVisible({ timeout: 30_000 });
    await frame.getByText('Launch app').first().click();
    const form = frame.locator('[data-block-id="app-set"]');
    await expect(form).toBeVisible();
    await form.locator('input').first().fill('42');
    await form.getByRole('button', { name: 'Save on-chain' }).click();
    await expect(form.getByText('Saved.')).toBeVisible({ timeout: 30_000 });
    await dialog.getByTestId('preview-calls-summary').click();
    await expect(dialog.getByTestId('preview-calls')).toContainText('callContract');

    // The studio's own mock-wallet log is separate; back in the editor nothing breaks.
    await dialog.getByRole('button', { name: 'Back to editor' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(await mockCalls(page)).toBeVisible();
  });
});
