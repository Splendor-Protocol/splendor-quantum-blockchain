import { expect, test } from '@playwright/test';

// Read-only recipes against the real network the playground defaults to (E2E_LIVE_NETWORK).
// They go through the same /rpc proxy and SqcProvider a visitor's browser uses.
for (const id of ['network-status', 'latest-block']) {
  test(`live recipe: ${id}`, async ({ page }) => {
    await page.goto(`/learn/recipe/${id}`);
    const recipe = page.getByTestId(`recipe-${id}`);
    await recipe.getByRole('button', { name: /^Run on / }).click();
    await expect(recipe.getByTestId('recipe-error')).toHaveCount(0, { timeout: 30_000 });
    await expect(recipe.getByTestId('recipe-result')).toBeVisible({ timeout: 30_000 });
  });
}
