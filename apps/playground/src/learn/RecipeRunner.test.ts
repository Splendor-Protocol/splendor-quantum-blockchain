import { describe, expect, it } from 'vitest';
import { getNetwork } from '@splendor/sqc-sdk/networks';
import { RECIPES } from '../registry/recipes';
import { SANDBOX_ASSET, sandboxContext } from './RecipeRunner';

const network = getNetwork('testnet')!;

describe('recipe sandbox', () => {
  it.each(RECIPES.map((r) => [r.id, r] as const))('%s runs with empty inputs', async (_id, recipe) => {
    const inputs = Object.fromEntries((recipe.inputs ?? []).map((i) => [i.name, i.defaultValue ?? '']));
    const value = await recipe.run(sandboxContext(network, inputs, () => undefined));
    expect(value).toBeTruthy();
  });

  it('fills empty token and collection fields with the demo asset only', () => {
    const ctx = sandboxContext(network, { token: '', owner: '', id: '1' }, () => undefined);
    expect(ctx.inputs).toEqual({ token: SANDBOX_ASSET, owner: '', id: '1' });
  });
});
