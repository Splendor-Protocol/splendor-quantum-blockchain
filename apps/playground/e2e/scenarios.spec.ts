import { expect, test } from '@playwright/test';
import { E2E } from './env';
import { mockCalls, openTemplate, preview, testLogin } from './helpers';

test.describe('gallery previews', () => {
  test('tip jar previews with a demo recipient instead of pausing', async ({ page }) => {
    await page.goto('/widgets/tip-jar');
    const frame = preview(page);
    await expect(frame.getByRole('button', { name: 'Send tip' })).toBeVisible();
    await expect(frame.getByText('Preview paused')).toHaveCount(0);
  });

  test('the fee row part has an interactive demo', async ({ page }) => {
    await page.goto('/widgets/fee-row');
    const demo = page.getByTestId('part-preview');
    await expect(demo).toContainText('0.000021');
    await demo.getByRole('button', { name: 'Will revert' }).click();
    await expect(demo).toContainText('expected to fail');
  });
});

test.describe('builder scenarios (mock wallet)', () => {
  test('an empty setup field keeps the preview running and the issue jumps to the field', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await expect(preview(page).getByText('Set a recipient address for this tip jar.')).toBeVisible();
    await page.getByTestId('spec-issues').getByRole('button', { name: 'Tip → Tip jar → Recipient' }).click();
    await expect(page.locator('#prop-tip-jar-recipient')).toBeFocused();
  });

  test('nft drop: deploy, mint, and watch the balance and the pending list update', async ({ page }) => {
    await openTemplate(page, 'nft-drop');
    const frame = preview(page);
    await frame.getByRole('button', { name: 'Deploy "Genesis"' }).click();
    await expect(frame.getByTestId('dapp-nft-owned')).toHaveText('You own 0 of token #1');
    await expect(frame.getByTestId('dapp-nft-collection')).toContainText('SQ:');
    await frame.getByRole('button', { name: 'Mint 1' }).click();
    await expect(frame.getByTestId('dapp-nft-owned')).toHaveText('You own 1 of token #1');
    await expect(frame.getByTestId('sqc-pending-tx').first()).toBeVisible();
    await expect(frame.getByTestId('sqc-pending-tx')).toHaveCount(0, { timeout: 20_000 });
  });

  test('wallet dashboard: a transfer lowers the balance', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    const frame = preview(page);
    await expect(frame.getByText('1250.5 SPLD').first()).toBeVisible();
    await frame.getByLabel('To (SQ:…)').fill(E2E.recipient);
    await frame.getByLabel('Amount (SPLD)').fill('250');
    await frame.getByRole('button', { name: 'Review', exact: true }).click();
    await frame.getByRole('button', { name: 'Confirm send' }).click();
    await expect(frame.getByText('1000.499979 SPLD').first()).toBeVisible();
  });

  test('token launchpad: the token deployed on page 1 is offered on page 2', async ({ page }) => {
    await openTemplate(page, 'token-launchpad');
    const frame = preview(page);
    await frame.getByRole('button', { name: 'Review deploy' }).click();
    await frame.getByRole('button', { name: 'Confirm deploy' }).click();
    await expect(await mockCalls(page)).toContainText('sendModuleCall');
    await page.getByRole('button', { name: /Manage/ }).first().click();
    const chips = frame.getByTestId('sqc-recent-tokens');
    await chips.getByRole('button', { name: 'MYT' }).click();
    await expect(frame.getByText('Balance: 1000000.0 MYT')).toBeVisible();
  });

  test('contract call block reads what a write stored', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    for (const mode of ['write', 'read'] as const) {
      await page.getByTestId('add-contract-call').click();
      await page.locator('#prop-contract-call-contract').fill(E2E.recipient);
      await page.locator('#prop-contract-call-functionSig').fill(mode === 'write' ? 'set(uint256)' : 'get()');
      await page.locator('#prop-contract-call-mode').selectOption(mode);
      if (mode === 'write') await page.locator('#prop-contract-call-args').fill('42');
      else await page.locator('#prop-contract-call-decode').selectOption('uint256');
    }
    const frame = preview(page);
    await frame.getByRole('button', { name: 'Send transaction' }).click();
    await expect(await mockCalls(page)).toContainText('callContract');
    await frame.getByRole('button', { name: 'Read' }).click();
    await expect(frame.getByTestId('dapp-contract-result')).toHaveText('Result: 42');
  });

  test('duplicate a block and add a page without a prompt', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    const blocks = page.getByTestId('block-list').locator('li');
    await expect(blocks).toHaveCount(4);
    await page.getByRole('button', { name: 'Duplicate SPLD transfer' }).click();
    await expect(blocks).toHaveCount(5);
    await page.getByRole('tab', { name: 'Pages' }).click();
    await page.getByRole('button', { name: '+ Add page' }).click();
    await page.getByLabel('New page title').fill('About');
    await page.getByRole('button', { name: 'Add blank', exact: true }).click();
    await expect(page.getByRole('button', { name: /About \/about/ })).toBeVisible();
  });
});

test.describe('live mode safety', () => {
  test('live preview stays off while the playground is on another network', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await page.locator('#dapp-network').selectOption('devnet');
    await page.getByRole('group', { name: 'Preview mode' }).getByRole('button', { name: 'Real wallet' }).click();
    await expect(page.getByTestId('live-network-mismatch')).toContainText('devnet');
  });

  test('switching a live preview to mainnet asks first, and declining keeps the network', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await page.getByRole('group', { name: 'Preview mode' }).getByRole('button', { name: 'Real wallet' }).click();
    await page.locator('#dapp-network').selectOption('mainnet');
    const dialog = page.getByTestId('confirm-dialog');
    await expect(dialog).toContainText('real SPLD');
    await expect(dialog.getByTestId('confirm-cancel')).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(page.locator('#dapp-network')).toHaveValue('testnet');
  });
});

test.describe('projects', () => {
  test('a share link can be revoked and a project deleted', async ({ page, browser }) => {
    await openTemplate(page, 'members-area');
    await testLogin(page, E2E.user);
    await page.reload();
    await page.getByTestId('tab-project').click();
    await page.getByRole('button', { name: 'Save to my account' }).click();
    await expect(page.getByTestId('save-status')).toHaveText('Saved to your account.');
    await page.getByRole('button', { name: 'Create share link' }).click();
    const shareUrl = await page.locator('#share-url').inputValue();
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.getByTestId('share-copy').click();
    await expect(page.getByTestId('share-copy')).toHaveText('Copied');
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(shareUrl);

    await page.getByTestId('unshare').click();
    await page.getByTestId('confirm-ok').click();
    await expect(page.getByRole('button', { name: 'Create share link' })).toBeVisible();
    const visitor = await browser.newPage();
    await visitor.goto(shareUrl);
    await expect(visitor.getByRole('alert')).toContainText('removed by its owner');
    await visitor.close();

    await page.getByRole('button', { name: 'Delete project Members Club' }).click();
    await page.getByTestId('confirm-ok').click();
    await expect(page.getByRole('button', { name: 'Delete project Members Club' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Save to my account' })).toBeVisible();
  });

  test('a spec can be imported from JSON', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await page.getByTestId('tab-project').click();
    await page.getByText('Spec JSON').click();
    const spec = {
      version: 1,
      name: 'Imported',
      network: 'testnet',
      theme: { base: 'light', overrides: {} },
      pages: [{ path: '/', title: 'Home', blocks: [{ id: 'hero', widget: 'hero', props: { heading: 'From JSON' } }] }],
    };
    await page.locator('#import-spec').fill(JSON.stringify(spec));
    await page.getByTestId('import-spec').click();
    await expect(preview(page).getByRole('heading', { name: 'From JSON' })).toBeVisible();
    await expect(page.locator('#dapp-name')).toHaveValue('Imported');
  });
});

test.describe('preview containment', () => {
  test('a web link in the preview opens a new window; the frame stays on the preview', async ({ page, context }) => {
    await context.route('https://example.com/**', (r) => r.fulfill({ contentType: 'text/html', body: '<h1>elsewhere</h1>' }));
    await openTemplate(page, 'wallet-dashboard');
    await page.getByTestId('tab-project').click();
    await page.getByText('Spec JSON').click();
    const spec = {
      version: 1,
      name: 'Links',
      network: 'testnet',
      theme: { base: 'dark', overrides: {} },
      pages: [
        {
          path: '/',
          title: 'Home',
          blocks: [
            { id: 'hero', widget: 'hero', props: { heading: 'Link test' } },
            { id: 'go', widget: 'action-button', props: { label: 'Elsewhere', href: 'https://example.com/' } },
          ],
        },
      ],
    };
    await page.locator('#import-spec').fill(JSON.stringify(spec));
    await page.getByTestId('import-spec').click();
    await expect(preview(page).getByRole('heading', { name: 'Link test' })).toBeVisible();

    const popup = context.waitForEvent('page');
    await preview(page).getByRole('link', { name: 'Elsewhere' }).click();
    expect((await popup).url()).toContain('example.com');
    // Same frame document: still the preview, still rendering the draft.
    expect(page.frames().some((f) => /\/preview(\.html)?(#|$)/.test(f.url()))).toBe(true);
    expect(page.frames().some((f) => f.url().includes('example.com'))).toBe(false);
    await expect(preview(page).getByRole('heading', { name: 'Link test' })).toBeVisible();
  });
});

test.describe('narrow studio', () => {
  test.use({ viewport: { width: 820, height: 1000 } });

  test('panels open as drawers, and tapping a component adds it and shows the page', async ({ page }) => {
    // Not openTemplate: the layers list it waits for lives in the closed drawer here.
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/build?template=tip-jar');
    await expect(preview(page).getByRole('heading').first()).toBeVisible();
    const side = page.getByTestId('studio-side');
    await expect(side).toBeHidden();
    await expect(page.getByTestId('studio-inspector')).toBeHidden();

    await page.getByRole('tablist', { name: 'Studio panels' }).getByRole('tab', { name: 'Components' }).click();
    await expect(side).toBeVisible();
    await page.getByTestId('add-transfer').click();
    await expect(side).toBeHidden();
    await expect(preview(page).getByText('Transfer SPLD')).toBeVisible();

    await page.getByTestId('drawer-inspector').click();
    await expect(page.getByTestId('studio-inspector')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('studio-inspector')).toBeHidden();
  });
});

test.describe('accessibility and navigation', () => {
  test('studio rail follows the ARIA tabs pattern with arrow keys', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    const railTabs = page.getByRole('tablist', { name: 'Studio panels' });
    const components = railTabs.getByRole('tab', { name: 'Components' });
    await expect(components).toHaveAttribute('aria-selected', 'true');
    await components.focus();
    await page.keyboard.press('ArrowDown');
    const theme = railTabs.getByRole('tab', { name: 'Theme' });
    await expect(theme).toBeFocused();
    await expect(theme).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('tabpanel', { name: 'Theme' })).toContainText('Theme presets');
    await page.keyboard.press('End');
    await expect(railTabs.getByRole('tab', { name: 'Flows' })).toBeFocused();
    await page.keyboard.press('Home');
    await expect(railTabs.getByRole('tab', { name: 'Pages' })).toBeFocused();
  });

  test('export dialog takes focus, closes on Escape and gives focus back', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    const opener = page.getByTestId('tab-export');
    await opener.click();
    const dialog = page.getByRole('dialog', { name: 'Export' });
    await expect(dialog).toBeVisible();
    await expect.poll(() => dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(opener).toBeFocused();
  });

  test('a skip link jumps past the header, and pages have their own titles', async ({ page }) => {
    await page.goto('/widgets');
    await expect(page).toHaveTitle('Widgets · Splendor dApp Playground');
    await page.keyboard.press('Tab');
    const skip = page.getByRole('link', { name: 'Skip to content' });
    await expect(skip).toBeFocused();
    await skip.press('Enter');
    await expect(page.locator('#main')).toBeFocused();
  });

  test('an unknown address offers a way back', async ({ page }) => {
    await page.goto('/no-such-page');
    await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible();
    await page.getByRole('link', { name: 'Home' }).click();
    await expect(page).toHaveURL(/\/$/);
  });

  test('gallery code never contains the preview demo address', async ({ page }) => {
    await page.goto('/widgets/tip-jar');
    await page.getByRole('tab', { name: 'Code' }).click();
    const code = page.getByRole('tabpanel', { name: 'Code' });
    await expect(code).toContainText('SQ:YOUR_ADDRESS');
    await expect(code).not.toContainText('5e5e5e');
  });
});

test('learn: recipes run in the mock sandbox without a node or a wallet', async ({ page }) => {
  for (const [id, expected] of [
    ['qrc20-info', 'Demo Token'],
    ['sign-message', 'signatureB64'],
  ] as const) {
    await page.goto(`/learn/recipe/${id}`);
    const recipe = page.getByTestId(`recipe-${id}`);
    await recipe.getByRole('button', { name: 'Mock sandbox' }).click();
    await recipe.getByRole('button', { name: 'Run in sandbox' }).click();
    await expect(recipe.getByTestId('recipe-result')).toContainText(expected);
  }
});
