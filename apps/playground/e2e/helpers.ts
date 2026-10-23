import { expect, type Page } from '@playwright/test';
import { E2E } from './env';

/** The mock-wallet preview iframe on the current page. */
export const preview = (page: Page) => page.frameLocator('[data-testid="preview-frame"]');

/** Signs the browser context in through the API's test-only route (ALLOW_TEST_LOGIN=1). */
export async function testLogin(page: Page, address: string) {
  const res = await page.request.post('/api/auth/test-login', {
    data: { address },
    headers: { origin: E2E.web },
  });
  expect(res.ok(), await res.text()).toBeTruthy();
}

/** Wallet calls the test-wallet preview has recorded, as shown under "Test wallet activity". */
export async function mockCalls(page: Page) {
  const panel = page.getByTestId('mock-calls');
  if (!(await panel.isVisible())) await page.getByText(/Test wallet activity/).click();
  return panel;
}

/** Opens the builder fresh from a template (drops any draft left by an earlier test). */
export async function openTemplate(page: Page, id: string) {
  await page.goto('/');
  await page.evaluate(() => localStorage.clear());
  await page.goto(`/build?template=${id}`);
  await expect(page.getByTestId('block-list')).toBeVisible();
}
