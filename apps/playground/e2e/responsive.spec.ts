import { expect, test, type Page } from '@playwright/test';
import { preview } from './helpers';

// Runs in the desktop project and in the `mobile` (390x844) and `tablet` (768x1024) projects.

async function noHorizontalScroll(page: Page) {
  // Let late layout (fonts, the preview frame) settle before measuring.
  await page.waitForTimeout(150);
  const { scroll, width } = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, width: window.innerWidth }));
  expect(scroll, `page is ${scroll}px wide in a ${width}px window`).toBeLessThanOrEqual(width);
}

const isNarrow = (page: Page) => (page.viewportSize()?.width ?? 1280) <= 1100;
const isPhone = (page: Page) => (page.viewportSize()?.width ?? 1280) <= 768;

/** Like `openTemplate`, but waits for the canvas: on narrow screens the layers list is in a closed drawer. */
async function openTemplate(page: Page, id: string) {
  await page.goto('/');
  await page.evaluate(() => localStorage.clear());
  await page.goto(`/build?template=${id}`);
  await expect(page.getByTestId('preview-frame')).toBeVisible();
}

/** Spec tab: in the top bar on desktop, inside "More" on narrow screens. */
async function openProjectTab(page: Page) {
  const more = page.getByTestId('topbar-more');
  if (await more.isVisible()) await more.click();
  await page.getByTestId('tab-project').click();
}

test.describe('responsive layout', () => {
  for (const path of ['/', '/widgets', '/widgets/tip-jar', '/learn/guide/widgets']) {
    test(`${path} fits the window`, async ({ page }) => {
      await page.goto(path);
      await expect(page.locator('#main')).toBeVisible();
      await noHorizontalScroll(page);
    });
  }

  test('the studio fits the window and the canvas shows the page', async ({ page }) => {
    await openTemplate(page, 'wallet-dashboard');
    await expect(preview(page).getByRole('heading', { name: 'Wallet dashboard' })).toBeVisible();
    await noHorizontalScroll(page);
  });

  test('phones get a header menu; it opens the navigation', async ({ page }) => {
    await page.goto('/');
    const toggle = page.getByRole('button', { name: 'Menu' });
    if (!isPhone(page)) {
      await expect(toggle).toBeHidden();
      await expect(page.getByRole('navigation', { name: 'Studio' })).toBeVisible();
      return;
    }
    await expect(page.getByRole('navigation', { name: 'Studio' })).toBeHidden();
    await toggle.click();
    await page.getByRole('navigation', { name: 'Studio' }).getByRole('link', { name: 'Widgets' }).click();
    await expect(page).toHaveURL(/\/widgets$/);
    // Navigating closes the menu.
    await expect(page.getByRole('navigation', { name: 'Studio' })).toBeHidden();
  });

  test('the widget gallery filters by search', async ({ page }) => {
    await page.goto('/widgets');
    await page.getByTestId('gallery-search').fill('transfer');
    await expect(page.getByTestId('gallery-transfer')).toBeVisible();
    await expect(page.getByTestId('gallery-tip-jar')).toHaveCount(0);
    await page.getByTestId('gallery-search').fill('zzzz-nothing');
    await expect(page.getByTestId('gallery-empty')).toBeVisible();
  });

  test('learn: a topic picker on phones, the sidebar list otherwise', async ({ page }) => {
    await page.goto('/learn/guide/widgets');
    if (isPhone(page)) {
      await expect(page.getByTestId('learn-jump')).toBeVisible();
      await expect(page.getByRole('navigation', { name: 'Learn' })).toBeHidden();
    } else {
      await expect(page.getByTestId('learn-jump')).toBeHidden();
      await expect(page.getByRole('navigation', { name: 'Learn' })).toBeVisible();
    }
  });

  test('narrow studio: tapping a block on the page opens the inspector drawer as a dialog', async ({ page }) => {
    test.skip(!isNarrow(page), 'drawers are for narrow screens');
    await openTemplate(page, 'tip-jar');
    await expect(page.getByTestId('studio-inspector')).toBeHidden();
    await preview(page).getByRole('heading').first().click();
    const inspector = page.getByTestId('studio-inspector');
    await expect(inspector).toBeVisible();
    await expect(inspector).toHaveAttribute('role', 'dialog');
    await expect.poll(() => inspector.evaluate((el) => el.contains(document.activeElement))).toBe(true);
    await page.keyboard.press('Escape');
    await expect(inspector).toBeHidden();
  });

  test('columns sit side by side on wide pages and stack on phones', async ({ page }) => {
    await openTemplate(page, 'tip-jar');
    await openProjectTab(page);
    await page.getByText('Spec JSON').click();
    const col = (id: string, heading: string) => ({ kind: 'column', id, style: { width: '50%' }, children: [{ kind: 'block', block: { id: `${id}-h`, widget: 'hero', props: { heading } } }] });
    const spec = {
      version: 2,
      name: 'Columns',
      network: 'testnet',
      theme: { base: 'dark', overrides: {} },
      pages: [{ path: '/', title: 'Home', root: { kind: 'section', id: 'root', style: {}, children: [{ kind: 'row', id: 'r', style: { gap: '1rem' }, children: [col('left', 'Left side'), col('right', 'Right side')] }] }, blocks: [] }],
    };
    await page.locator('#import-spec').fill(JSON.stringify(spec));
    await page.getByTestId('import-spec').click();
    // The import dialog may stay open on narrow screens; the canvas is what matters.
    await page.keyboard.press('Escape');
    const left = preview(page).getByRole('heading', { name: 'Left side' });
    const right = preview(page).getByRole('heading', { name: 'Right side' });
    await expect(left).toBeVisible();
    const a = (await left.boundingBox())!;
    const b = (await right.boundingBox())!;
    // The app's breakpoint is 720px of the app's own window (the preview frame), as in the export.
    const appWidth = await preview(page).locator('body').evaluate(() => window.innerWidth);
    if (appWidth <= 720) expect(b.y).toBeGreaterThan(a.y + a.height - 1);
    else expect(Math.abs(a.y - b.y)).toBeLessThan(4);
  });

  test('a laptop-sized studio shows Desktop at desktop width, zoomed to fit', async ({ page }) => {
    test.skip(isNarrow(page), 'narrow studios show the page at its real width');
    await openTemplate(page, 'tip-jar');
    // 1280px window: the canvas is narrower than 1024px once the panels are open.
    await expect(page.getByTestId('canvas-zoom')).toHaveText(/^\d{2}%$/);
    const innerWidth = await preview(page).locator('body').evaluate(() => window.innerWidth);
    expect(innerWidth).toBe(1024);
    await page.getByRole('group', { name: 'Preview device' }).getByRole('button', { name: 'Mobile' }).click();
    await expect(page.getByTestId('canvas-zoom')).toHaveCount(0);
  });
});
