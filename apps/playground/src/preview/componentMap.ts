import type { ComponentType } from 'react';
import * as ConnectReact from '@splendor/connect-react';
import type { CatalogEntry } from '@splendor/dapp-spec';
import { ActionButton } from '@splendor/dapp-spec/blocks/ActionButton.tsx';
import { BalanceSummary } from '@splendor/dapp-spec/blocks/BalanceSummary.tsx';
import { ContractCall } from '@splendor/dapp-spec/blocks/ContractCall.tsx';
import { ContractRead } from '@splendor/dapp-spec/blocks/ContractRead.tsx';
import { ContractWrite } from '@splendor/dapp-spec/blocks/ContractWrite.tsx';
import { CtaBanner } from '@splendor/dapp-spec/blocks/CtaBanner.tsx';
import { Divider } from '@splendor/dapp-spec/blocks/Divider.tsx';
import { Faq } from '@splendor/dapp-spec/blocks/Faq.tsx';
import { FeatureGrid } from '@splendor/dapp-spec/blocks/FeatureGrid.tsx';
import { FooterBar } from '@splendor/dapp-spec/blocks/FooterBar.tsx';
import { Heading } from '@splendor/dapp-spec/blocks/Heading.tsx';
import { Hero } from '@splendor/dapp-spec/blocks/Hero.tsx';
import { HeroCentered } from '@splendor/dapp-spec/blocks/HeroCentered.tsx';
import { ImageBlock } from '@splendor/dapp-spec/blocks/ImageBlock.tsx';
import { MembersArea } from '@splendor/dapp-spec/blocks/MembersArea.tsx';
import { NftDrop } from '@splendor/dapp-spec/blocks/NftDrop.tsx';
import { RichText } from '@splendor/dapp-spec/blocks/RichText.tsx';
import { Spacer } from '@splendor/dapp-spec/blocks/Spacer.tsx';
import { StatCard } from '@splendor/dapp-spec/blocks/StatCard.tsx';
import { TipJar } from '@splendor/dapp-spec/blocks/TipJar.tsx';
import { TokenInfoCard } from '@splendor/dapp-spec/blocks/TokenInfoCard.tsx';
import { Badge } from '@splendor/dapp-spec/blocks/Badge.tsx';
import { CardShell } from '@splendor/dapp-spec/blocks/CardShell.tsx';
import { ColumnsShell } from '@splendor/dapp-spec/blocks/ColumnsShell.tsx';
import { ContractAction } from '@splendor/dapp-spec/blocks/ContractAction.tsx';
import { FormAddressInput } from '@splendor/dapp-spec/blocks/FormAddressInput.tsx';
import { FormAmountInput } from '@splendor/dapp-spec/blocks/FormAmountInput.tsx';
import { FormSelect } from '@splendor/dapp-spec/blocks/FormSelect.tsx';
import { FormTextInput } from '@splendor/dapp-spec/blocks/FormTextInput.tsx';
import { FormToggle } from '@splendor/dapp-spec/blocks/FormToggle.tsx';
import { HeroGradient } from '@splendor/dapp-spec/blocks/HeroGradient.tsx';
import { HeroSplit } from '@splendor/dapp-spec/blocks/HeroSplit.tsx';
import { LogoRow } from '@splendor/dapp-spec/blocks/LogoRow.tsx';
import { NavbarBlock } from '@splendor/dapp-spec/blocks/NavbarBlock.tsx';
import { NftGallery } from '@splendor/dapp-spec/blocks/NftGallery.tsx';
import { PricingCard } from '@splendor/dapp-spec/blocks/PricingCard.tsx';
import { RowShell } from '@splendor/dapp-spec/blocks/RowShell.tsx';
import { SectionShell } from '@splendor/dapp-spec/blocks/SectionShell.tsx';
import { Steps } from '@splendor/dapp-spec/blocks/Steps.tsx';
import { Testimonial } from '@splendor/dapp-spec/blocks/Testimonial.tsx';
import { TxHistoryTable } from '@splendor/dapp-spec/blocks/TxHistoryTable.tsx';
import { TxStatusCard } from '@splendor/dapp-spec/blocks/TxStatusCard.tsx';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyComponent = ComponentType<any>;

/** Same files the code generator copies into exported projects, so preview = export. */
const BLOCKS: Record<string, AnyComponent> = {
  ActionButton,
  BalanceSummary,
  ContractCall,
  ContractRead,
  ContractWrite,
  CtaBanner,
  Divider,
  Faq,
  FeatureGrid,
  FooterBar,
  Heading,
  Hero,
  HeroCentered,
  ImageBlock,
  MembersArea,
  NftDrop,
  RichText,
  Spacer,
  StatCard,
  TipJar,
  TokenInfoCard,
  Badge,
  CardShell,
  ColumnsShell,
  ContractAction,
  FormAddressInput,
  FormAmountInput,
  FormSelect,
  FormTextInput,
  FormToggle,
  HeroGradient,
  HeroSplit,
  LogoRow,
  NavbarBlock,
  NftGallery,
  PricingCard,
  RowShell,
  SectionShell,
  Steps,
  Testimonial,
  TxHistoryTable,
  TxStatusCard,
};

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
