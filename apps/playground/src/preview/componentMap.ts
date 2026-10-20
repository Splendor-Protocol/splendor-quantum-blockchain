import type { ComponentType } from 'react';
import * as ConnectReact from '@splendor/connect-react';
import type { CatalogEntry } from '@splendor/dapp-spec';
import { BalanceSummary } from '@splendor/dapp-spec/blocks/BalanceSummary.tsx';
import { ContractCall } from '@splendor/dapp-spec/blocks/ContractCall.tsx';
import { Hero } from '@splendor/dapp-spec/blocks/Hero.tsx';
import { MembersArea } from '@splendor/dapp-spec/blocks/MembersArea.tsx';
import { NftDrop } from '@splendor/dapp-spec/blocks/NftDrop.tsx';
import { TipJar } from '@splendor/dapp-spec/blocks/TipJar.tsx';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyComponent = ComponentType<any>;

/** Same files the code generator copies into exported projects, so preview = export. */
const BLOCKS: Record<string, AnyComponent> = { BalanceSummary, ContractCall, Hero, MembersArea, NftDrop, TipJar };

/** Packages the playground can render. Third-party widget packages register here (phase 2). */
const PACKAGES: Record<string, Record<string, unknown>> = {
  '@splendor/connect-react': ConnectReact,
};

export function resolveComponent(entry: CatalogEntry): AnyComponent | null {
  if (entry.kind === 'block') return BLOCKS[entry.importName] ?? null;
  const mod = PACKAGES[entry.package ?? '@splendor/connect-react'];
  const c = mod?.[entry.importName];
  return typeof c === 'function' || (typeof c === 'object' && c !== null) ? (c as AnyComponent) : null;
}
