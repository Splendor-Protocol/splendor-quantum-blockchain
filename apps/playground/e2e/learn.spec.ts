import { expect, test } from '@playwright/test';

test('learn: guides render and recipes show their code', async ({ page }) => {
  await page.goto('/learn');
  await expect(page).toHaveURL(/\/learn\/guide\/widgets/);
  await expect(page.locator('.pg-markdown').getByRole('heading').first()).toBeVisible();

  await page.goto('/learn/recipe/network-status');
  const recipe = page.getByTestId('recipe-network-status');
  await expect(recipe.getByRole('button', { name: /^Run on / })).toBeEnabled();
  await expect(recipe.locator('pre, code').first()).toBeVisible();
});
