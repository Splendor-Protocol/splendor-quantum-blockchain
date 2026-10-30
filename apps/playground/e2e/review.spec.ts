import { expect, test, type Page } from '@playwright/test';
import { E2E } from './env';
import { openTemplate, preview, testLogin } from './helpers';

// Scenarios from the review of the draft, trash, revision and preview-hardening work: each one is a
// way work could be lost or a guard could be bypassed.

const DRAFT_KEY = 'splendor-playground:draft:v1';
const draftName = (page: Page) => page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? '{}').spec?.name as string | undefined, DRAFT_KEY);

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

async function importSpec(page: Page, spec: object) {
  await page.getByTestId('tab-project').click();
  await page.getByText('Spec JSON').click();
  await page.locator('#import-spec').fill(JSON.stringify(spec));
  await page.getByTestId('import-spec').click();
}

test.describe('drafts survive fast leaves and replacements', () => {
  test('a reload right after an edit keeps it', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByLabel('dApp name').fill('Fast edit');
    await page.reload();
    await expect(page.getByLabel('dApp name')).toHaveValue('Fast edit');
  });

  test('New right after typing keeps the typed draft for Restore', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByLabel('dApp name').fill('Typed then New');
    await page.getByTestId('tab-project').click();
    await page.getByRole('button', { name: 'New project…' }).click();
    await expect(page.getByTestId('stash-notice')).toContainText('Typed then New');
  });

  test('Import right after typing keeps the typed draft for Restore', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByLabel('dApp name').fill('Typed then Import');
    await importSpec(page, {
      version: 1,
      name: 'Imported',
      network: 'testnet',
      theme: { base: 'light', overrides: {} },
      pages: [{ path: '/', title: 'Home', blocks: [{ id: 'hero', widget: 'hero', props: { heading: 'From JSON' } }] }],
    });
    await expect(preview(page).getByRole('heading', { name: 'From JSON' })).toBeVisible();
    await expect(page.getByTestId('stash-notice')).toContainText('Typed then Import');
  });

  test('an untouched template never pushes real work out of the stash', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByLabel('dApp name').fill('Real work');
    await expect.poll(() => draftName(page)).toBe('Real work');
    await page.goto('/build?template=wallet-dashboard');
    await expect(page.getByTestId('stash-notice')).toContainText('Real work');
    // Wallet Dashboard is left as it opened; the next template must not stash it over Real work.
    await page.goto('/build?template=members-area');
    await expect(page.getByTestId('stash-notice')).toContainText('Real work');
  });

  test('Restore swaps the stash with the current draft, so neither is lost', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByLabel('dApp name').fill('Draft A');
    await expect.poll(() => draftName(page)).toBe('Draft A');
    await page.goto('/build?template=wallet-dashboard');
    await page.getByLabel('dApp name').fill('Draft B');
    await page.getByTestId('stash-restore').click();
    await expect(page.getByLabel('dApp name')).toHaveValue('Draft A');
    await expect(page.getByTestId('stash-notice')).toContainText('Draft B');
  });

  test('a project link that fails says why, leaves the draft alone, and picking a template stashes it', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await page.getByLabel('dApp name').fill('Keep me');
    await expect.poll(() => draftName(page)).toBe('Keep me');
    await testLogin(page, E2E.user);
    await page.goto('/build?project=missingproj01');
    await expect(page.getByTestId('project-load-error')).toContainText('not found');
    await expect(page.getByLabel('dApp name')).toHaveCount(0);
    expect(await draftName(page)).toBe('Keep me');
    await page.getByTestId('pick-blank').click();
    await expect(page.getByTestId('stash-notice')).toContainText('Keep me');
  });
});

test.describe('saving to the account when things go wrong', () => {
  test('unsaved account edits survive a template start, and restoring them cannot overwrite a newer save', async ({ page }) => {
    const id = await savedProject(page);
    await page.route('**/api/projects/*', (route) => (route.request().method() === 'PUT' ? route.abort() : route.continue()));
    await page.getByLabel('dApp name').fill('Unsynced edit');
    await expect.poll(() => page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? '{}').dirty, DRAFT_KEY)).toBe(true);
    // Another device saves on top in the meantime (version 1 -> 2).
    const current = (await (await page.request.get(`/api/projects/${id}`)).json()).spec;
    const other = await page.request.put(`/api/projects/${id}`, { data: { spec: { ...current, name: 'Other device' }, baseVersion: 1 }, headers: { origin: E2E.web } });
    expect(other.status()).toBe(200);

    await page.goto('/build?template=members-area');
    await expect(page.getByTestId('stash-notice')).toContainText('Unsaved edits to “Unsynced edit”');
    await page.unroute('**/api/projects/*');
    const put = page.waitForRequest((r) => r.method() === 'PUT' && r.url().includes(`/projects/${id}`));
    await page.getByTestId('stash-restore').click();
    expect((await put).postDataJSON().baseVersion).toBe(1);
    await expect(page.getByTestId('save-conflict')).toBeVisible();
    await expect(page.getByLabel('dApp name')).toHaveValue('Unsynced edit');
    expect((await (await page.request.get(`/api/projects/${id}`)).json()).name).toBe('Other device');
  });

  test('a refused draft write shows "Not saved"', async ({ page }) => {
    await page.addInitScript((key) => {
      const original = Storage.prototype.setItem;
      Storage.prototype.setItem = function (this: Storage, k: string, v: string) {
        if (k === key) throw new DOMException('full', 'QuotaExceededError');
        return original.call(this, k, v);
      };
    }, DRAFT_KEY);
    await page.goto('/build?template=tip-jar');
    await expect(page.getByTestId('save-chip')).toHaveText('Not saved');
  });

  test('autosave retries a server error, and never retries a rejected spec', async ({ page }) => {
    await savedProject(page);
    let puts = 0;
    await page.route('**/api/projects/*', async (route) => {
      if (route.request().method() !== 'PUT') return route.continue();
      puts += 1;
      if (puts === 1) return route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"Busy"}' });
      return route.continue();
    });
    await page.getByLabel('dApp name').fill('Retried');
    await expect(page.getByTestId('save-chip')).toHaveText('Sync failed');
    await expect(page.getByTestId('save-chip')).toHaveText('Synced', { timeout: 20_000 });
    expect(puts).toBe(2);

    await page.unroute('**/api/projects/*');
    let rejected = 0;
    await page.route('**/api/projects/*', (route) => {
      if (route.request().method() !== 'PUT') return route.continue();
      rejected += 1;
      return route.fulfill({ status: 422, contentType: 'application/json', body: '{"error":"Invalid spec"}' });
    });
    await page.getByLabel('dApp name').fill('Rejected');
    await expect(page.getByTestId('save-chip')).toHaveText('Sync failed');
    await page.waitForTimeout(3_000);
    expect(rejected).toBe(1);
  });

  test('a double click on "Save to my account" makes one project', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await testLogin(page, E2E.user);
    await page.reload();
    let posts = 0;
    page.on('request', (r) => {
      if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/projects') posts += 1;
    });
    await page.getByTestId('tab-project').click();
    await page.getByRole('button', { name: 'Save to my account' }).dblclick();
    await expect(page.getByTestId('save-status')).toHaveText('Saved to your account.');
    expect(posts).toBe(1);
  });

  test('signed out, an account workspace link asks to sign in and keeps nothing under its id', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await page.goto('/build?workspace=abcdefghij12&template=tip-jar');
    await expect(page.getByTestId('workspace-signin')).toBeVisible();
    await page.getByLabel('dApp name').fill('Signed out work');
    await page.waitForTimeout(1_200);
    const keys = await page.evaluate(() => Object.keys(JSON.parse(localStorage.getItem('sqc-playground-workspace-projects-v1') ?? '{}')));
    expect(keys).not.toContain('abcdefghij12');
    expect(await draftName(page)).toBe('Signed out work');
  });
});

test.describe('trash, version history and share links', () => {
  test('a project deleted on the home page can be restored from the trash', async ({ page }) => {
    const wsName = `Trash ${Date.now()}`;
    await page.goto('/');
    await page.evaluate(() => localStorage.clear());
    await testLogin(page, E2E.user);
    await page.goto('/');
    await page.getByLabel('New workspace name').fill(wsName);
    await page.getByRole('button', { name: 'New workspace' }).click();
    const wsCard = page.locator('[data-testid^="workspace-card-"]', { hasText: wsName });
    await expect(wsCard).toHaveCount(1);
    const wsId = (await wsCard.getAttribute('data-testid'))!.replace('workspace-card-', '');

    await page.goto(`/build?workspace=${wsId}&template=wallet-dashboard`);
    await page.getByLabel('dApp name').fill('Trash me');
    await page.getByTestId('tab-project').click();
    await page.getByRole('button', { name: 'Save to my account' }).click();
    await expect(page.getByTestId('save-status')).toHaveText('Saved to your account.');

    await page.goto(`/?workspace=${wsId}`);
    const card = page.locator('[data-testid^="project-card-"]', { hasText: 'Trash me' });
    await expect(card).toHaveCount(1);
    await card.getByRole('button', { name: 'Delete project Trash me' }).click();
    await page.getByTestId('confirm-ok').click();
    await expect(card).toHaveCount(0);
    await page.getByTestId('trash').getByRole('button', { name: 'Show' }).click();
    await page.getByRole('button', { name: 'Restore Trash me' }).first().click();
    await expect(card).toHaveCount(1);
  });

  test('an older version can be restored from the version history', async ({ page }) => {
    const id = await savedProject(page);
    const put = page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().includes(`/projects/${id}`));
    await page.getByLabel('dApp name').fill('Second name');
    expect((await put).status()).toBe(200);
    await expect(page.getByTestId('save-chip')).toHaveText('Synced');
    await page.getByTestId('tab-project').click();
    await page.getByTestId('version-history').locator('summary').click();
    await page.getByRole('button', { name: 'Restore version 1' }).click();
    await page.getByTestId('confirm-ok').click();
    await expect(page.getByLabel('dApp name')).toHaveValue('Wallet Dashboard');
    const saved = await (await page.request.get(`/api/projects/${id}`)).json();
    expect(saved).toMatchObject({ name: 'Wallet Dashboard', version: 3 });
  });

  test('an expired share link says it expired', async ({ page }) => {
    await page.route('**/api/share/**', (route) =>
      route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: 'This share link has expired', code: 'EXPIRED' }) }),
    );
    await page.goto('/s/abcdefghijkl');
    await expect(page.getByRole('alert')).toContainText('expired');
  });

  test('a share link shows a protected page behind its sign-in gate, as a visitor would see it', async ({ page }) => {
    // The home page must stay public, so the gated page is the second one.
    const spec = {
      version: 1,
      name: 'Gated share',
      network: 'testnet',
      theme: { base: 'dark', overrides: {} },
      pages: [
        { path: '/', title: 'Home', blocks: [{ id: 'hero', widget: 'hero', props: { heading: 'Welcome' } }] },
        { path: '/members', title: 'Members', access: 'signin', blocks: [{ id: 'secret', widget: 'hero', props: { heading: 'Members only content' } }] },
      ],
    };
    await page.route('**/api/share/**', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ name: spec.name, spec }) }),
    );
    await page.goto('/s/abcdefghijkl');
    await expect(preview(page).getByRole('heading', { name: 'Welcome' })).toBeVisible();
    await preview(page).getByRole('link', { name: 'Members' }).click();
    await expect(preview(page).getByText('Sign in to continue')).toBeVisible();
    await expect(preview(page).getByText('Members only content')).toHaveCount(0);
    // Visitor mode, not the builder: no "Visitors must sign in" note in place of the gate.
    await expect(preview(page).getByText(/Visitors must sign in/)).toHaveCount(0);
  });

  test('Escape in the stop-sharing confirm closes only the confirm, and the link stays live', async ({ page }) => {
    await savedProject(page);
    await page.getByTestId('tab-project').click();
    await page.getByRole('button', { name: 'Create share link' }).click();
    const shareId = new URL(await page.locator('#share-url').inputValue()).pathname.split('/').pop();
    await page.getByTestId('unshare').click();
    await expect(page.getByTestId('confirm-dialog')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('confirm-dialog')).toHaveCount(0);
    await expect(page.locator('#share-url')).toBeVisible();
    expect((await page.request.get(`/api/share/${shareId}`)).status()).toBe(200);
  });
});

test.describe('studio jumps and drops', () => {
  test.describe('narrow screen', () => {
    test.use({ viewport: { width: 820, height: 1000 } });

    test('a Finish setup item opens the inspector drawer on its field', async ({ page }) => {
      await page.goto('/');
      await page.evaluate(() => localStorage.clear());
      await page.goto('/build?template=tip-jar');
      await expect(page.getByTestId('spec-issues')).toBeVisible();
      await expect(page.getByTestId('studio-inspector')).toBeHidden();
      await page.getByTestId('spec-issues').getByRole('button').first().click();
      await expect(page.getByTestId('studio-inspector')).toBeVisible();
      await expect.poll(() => page.evaluate(() => document.activeElement?.id ?? '')).toMatch(/^prop-/);
    });
  });

  test('a contract function with a display name can be dragged into the page', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await importSpec(page, {
      version: 1,
      name: 'Drops',
      network: 'testnet',
      theme: { base: 'dark', overrides: {} },
      contracts: [
        { id: 'counter', name: 'Counter', address: `SQ:${'ab'.repeat(32)}`, functions: [{ name: 'Get count', signature: 'get()', params: [], kind: 'read' }] },
      ],
      pages: [{ path: '/', title: 'Home', blocks: [{ id: 'hero', widget: 'hero', props: { heading: 'Drop here' } }] }],
    });
    await expect(preview(page).getByRole('heading', { name: 'Drop here' })).toBeVisible();
    const layers = page.getByTestId('block-list').locator('li');
    await expect(layers).toHaveCount(1);
    await page.getByRole('tablist', { name: 'Studio panels' }).getByRole('tab', { name: 'Contracts' }).click();
    await page
      .getByRole('button', { name: /^Read · get\(\)/ })
      .dragTo(page.getByTestId('block-list').getByRole('treeitem', { name: 'Page' }));
    await expect(layers).toHaveCount(2);
  });
});
