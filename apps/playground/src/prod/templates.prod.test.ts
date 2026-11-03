import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import {
  TEMPLATES,
  createCatalog,
  emptySpec,
  generateProject,
  wrapBlocks,
  instantiateTemplate,
  updateBlockProps,
  validateSpec,
  type DappSpec,
} from '@splendor/dapp-spec';
import type { Signer } from '@splendor/sqc-sdk';
import { deployQRC20 } from '@splendor/sqc-sdk/tokens';
import { validateForPreview } from '../preview/validatePreview';
import { DEMO_RECIPIENT, MOCK_ADDRESS, createPlaygroundMock } from '../preview/mock';

const catalog = createCatalog(WIDGET_MANIFESTS);
const versions = { sqcSdk: '3.0.0', connectReact: '3.0.0' };
const RECIPIENT = 'SQ:' + 'ab'.repeat(32);

/** A template with its setup fields filled, the way the wizard leaves it. */
function ready(id: string): DappSpec {
  const t = TEMPLATES.find((x) => x.id === id)!;
  const spec = instantiateTemplate(t);
  for (const s of t.setup) {
    const block = spec.pages.flatMap((p) => p.blocks ?? []).find((b) => b.id === s.blockId)!;
    block.props[s.prop] = RECIPIENT;
  }
  for (const p of spec.pages) {
    for (const b of p.blocks ?? []) p.root = updateBlockProps(p.root, b.id, b.props) as typeof p.root;
  }
  return spec;
}

/** First block on the first page, edited in both the flat list and the tree. */
function patchFirstBlock(spec: DappSpec, patch: (b: { widget: string; props: Record<string, unknown> }) => void): DappSpec {
  const page = spec.pages[0]!;
  const first = (page.blocks ?? [])[0]!;
  patch(first as never);
  const walk = (n: unknown): void => {
    const node = n as { kind: string; block?: { id: string }; children?: unknown[] };
    if (node.kind === 'block' && node.block?.id === first.id) Object.assign(node.block, first);
    node.children?.forEach(walk);
  };
  walk(page.root);
  return spec;
}

const ids = TEMPLATES.map((t) => t.id);

describe('templates prod', () => {
  it.each(ids)('[L1] %s validates, previews, and exports for Vite and Next', (id) => {
    const spec = ready(id);
    const v = validateSpec(spec, { catalog });
    expect(v.ok, JSON.stringify(!v.ok && v.issues)).toBe(true);
    expect(validateForPreview(spec, catalog).spec).not.toBeNull();
    for (const framework of ['vite', 'next'] as const) {
      const files = generateProject(spec, { framework, catalog, versions }).files;
      expect(files['package.json']).toBeTruthy();
      expect(Object.keys(files).some((f) => /Page\d*\.tsx$/.test(f))).toBe(true);
    }
  });

  it.each(ids)('[L2] %s previews before its setup fields are filled', (id) => {
    const spec = instantiateTemplate(TEMPLATES.find((t) => t.id === id)!);
    expect(validateForPreview(spec, catalog).spec).not.toBeNull();
  });

  it.each(ids)('[L2] %s with an unknown widget is blocked from preview and export', (id) => {
    const spec = patchFirstBlock(ready(id), (b) => {
      b.widget = 'definitely-not-a-widget';
    });
    expect(validateSpec(spec, { catalog }).ok).toBe(false);
    expect(validateForPreview(spec, catalog).spec).toBeNull();
  });

  it('[L2] a javascript: link is reported and never reaches the rendered spec', () => {
    const spec = emptySpec('Links');
    const blocks = [{ id: 'btn', widget: 'action-button', props: { label: 'Go', href: 'javascript:alert(1)' } }];
    spec.pages[0]!.blocks = blocks;
    spec.pages[0]!.root = wrapBlocks(blocks);
    expect(validateSpec(spec, { catalog }).ok).toBe(false);
    const preview = validateForPreview(spec, catalog);
    expect(preview.issues.length).toBeGreaterThan(0);
    expect(JSON.stringify(preview.spec ?? {})).not.toContain('javascript:');
  });
});

function chain(balance: string) {
  const ctrl = createPlaygroundMock({ network: 'testnet', state: 'connected', balance });
  const wallet = ctrl.wallet as unknown as Signer & { sendTransfer(o: object): Promise<{ hash: string }>; callContract(o: object): Promise<unknown> };
  const raw = async () => BigInt((await (ctrl.wallet.connection.getBalance as (a: string) => Promise<{ balance: string }>)(MOCK_ADDRESS)).balance);
  return { ctrl, wallet, raw };
}

const SPLD = 10n ** 18n;
const FEE = 21_000_000_000_000n;

describe('preview mock chain prod', () => {
  it('[L1] a normal transfer moves the amount and charges the fee', async () => {
    const { wallet, raw } = chain('10');
    await wallet.sendTransfer({ to: DEMO_RECIPIENT, amountSPLD: '1' });
    expect(await raw()).toBe(9n * SPLD - FEE);
  });

  it('[L2] an over-balance send is refused and charges nothing', async () => {
    const { wallet, raw } = chain('1');
    await expect(wallet.sendTransfer({ to: DEMO_RECIPIENT, amountSPLD: '1' })).rejects.toThrow(/Insufficient/);
    expect(await raw()).toBe(SPLD);
  });

  it('[L3] a module call or contract call the wallet cannot pay the fee for is refused, like the node', async () => {
    const { wallet, raw } = chain('0');
    await expect(deployQRC20(wallet, { name: 'T', symbol: 'T', decimals: 18, totalSupply: '1' })).rejects.toThrow(/fee/);
    await expect(wallet.callContract({ contract: DEMO_RECIPIENT, function: 'increment()', args: [] })).rejects.toThrow(/fee/);
    expect(await raw()).toBe(0n);
  });

  it('[L4] 50 parallel transfers settle to the exact balance', async () => {
    const { wallet, raw } = chain('10');
    await Promise.all(Array.from({ length: 50 }, () => wallet.sendTransfer({ to: DEMO_RECIPIENT, amountSPLD: '0.1' })));
    expect(await raw()).toBe(10n * SPLD - 50n * (SPLD / 10n) - 50n * FEE);
  });

  it('[L4] 20 parallel 1 SPLD sends from 10 SPLD: exactly 9 land, balance never goes negative', async () => {
    const { wallet, raw } = chain('10');
    const out = await Promise.allSettled(Array.from({ length: 20 }, () => wallet.sendTransfer({ to: DEMO_RECIPIENT, amountSPLD: '1' })));
    expect(out.filter((r) => r.status === 'fulfilled')).toHaveLength(9);
    const left = await raw();
    expect(left).toBe(10n * SPLD - 9n * (SPLD + FEE));
    expect(left >= 0n).toBe(true);
  });
});
