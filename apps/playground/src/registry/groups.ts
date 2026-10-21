/** Friendly shelf names for the builder palette and the widget rail. */
export const BLOCK_GROUPS: Record<string, { label: string; tone: string }> = {
  layout: { label: 'Page', tone: 'violet' },
  wallet: { label: 'Wallet', tone: 'mint' },
  tokens: { label: 'Tokens', tone: 'amber' },
  auth: { label: 'Sign-in', tone: 'sky' },
  activity: { label: 'Activity', tone: 'rose' },
  nft: { label: 'Collectibles', tone: 'pink' },
  modules: { label: 'Contracts', tone: 'slate' },
  forms: { label: 'Forms', tone: 'sky' },
};

export function groupMeta(category: string): { label: string; tone: string } {
  return BLOCK_GROUPS[category] ?? { label: category, tone: 'slate' };
}

export function groupOrder(categories: readonly string[]): string[] {
  const known = Object.keys(BLOCK_GROUPS);
  return [...categories].sort((a, b) => {
    const ia = known.indexOf(a);
    const ib = known.indexOf(b);
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
  });
}
