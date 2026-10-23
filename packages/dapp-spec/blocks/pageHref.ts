/**
 * Link target of a page path or external URL.
 *
 * Shared by the blocks that render links. This default build uses hash routing (`#/about`), which
 * is what the Vite export and the playground preview use. The code generator ships a variant that
 * returns the plain route (`/about`) for Next.js exports.
 */
export function pageHref(path: string): string {
  const v = path.trim();
  if (!v) return '';
  if (/^https?:\/\//i.test(v)) return v;
  const p = v.replace(/^#/, '');
  return `#${p.startsWith('/') ? p : `/${p}`}`;
}
