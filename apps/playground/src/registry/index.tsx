import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { TEMPLATES, createCatalog, type CatalogEntry, type DappTemplate } from '@splendor/dapp-spec';
import { api, type CatalogFlag } from '../api/client';
import { PACKAGE_VERSIONS } from '../config';
import { RECIPES, type SdkRecipeManifest } from './recipes';

/** Template flags share the catalog flag table under `template:<id>`. */
export const templateFlagId = (id: string) => `template:${id}`;
export const recipeFlagId = (id: string) => `recipe:${id}`;

type Registry = {
  /** Every known entry (widgets + blocks), with admin status overrides but including hidden ones. */
  all: CatalogEntry[];
  /** Entries a user may place: not hidden by an admin. */
  catalog: CatalogEntry[];
  templates: DappTemplate[];
  recipes: SdkRecipeManifest[];
  flags: Record<string, CatalogFlag>;
  isFeatured: (id: string) => boolean;
  /** "requires connect-react >= x" when an entry is newer than the version bundled here. */
  compatibilityNote: (entry: CatalogEntry) => string | null;
  reloadFlags: () => Promise<void>;
  setFlags: (flags: Record<string, CatalogFlag>) => void;
};

const RegistryContext = createContext<Registry | null>(null);

function cmpSemver(a: string, b: string): number {
  const pa = a.split(/[.-]/).map((x) => Number.parseInt(x, 10) || 0);
  const pb = b.split(/[.-]/).map((x) => Number.parseInt(x, 10) || 0);
  for (let i = 0; i < 3; i++) if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) - (pb[i] ?? 0);
  return 0;
}

export function RegistryProvider({ children, initialFlags = {} }: { children: ReactNode; initialFlags?: Record<string, CatalogFlag> }) {
  const [flags, setFlags] = useState<Record<string, CatalogFlag>>(initialFlags);

  const reloadFlags = useCallback(async () => {
    try {
      setFlags((await api.catalogFlags()).flags);
    } catch {
      /* API optional: run with built-in defaults */
    }
  }, []);

  useEffect(() => {
    void reloadFlags();
  }, [reloadFlags]);

  const value = useMemo<Registry>(() => {
    const statusOnly = Object.fromEntries(
      Object.entries(flags).map(([id, f]) => [id, f.status ? { status: f.status } : {}]),
    );
    const all = createCatalog(WIDGET_MANIFESTS, statusOnly);
    const catalog = all.filter((e) => !flags[e.id]?.hidden);
    return {
      all,
      catalog,
      templates: TEMPLATES.filter((t) => !flags[templateFlagId(t.id)]?.hidden),
      recipes: RECIPES.filter((r) => !flags[recipeFlagId(r.id)]?.hidden),
      flags,
      isFeatured: (id) => !!flags[id]?.featured,
      compatibilityNote: (entry) => {
        const min = flags[entry.id]?.minVersion ?? entry.since;
        if (!min || entry.kind === 'block') return null;
        return cmpSemver(min, PACKAGE_VERSIONS.connectReact) > 0 ? `Requires connect-react >= ${min}` : null;
      },
      reloadFlags,
      setFlags,
    };
  }, [flags, reloadFlags]);

  return <RegistryContext.Provider value={value}>{children}</RegistryContext.Provider>;
}

export function useRegistry(): Registry {
  const ctx = useContext(RegistryContext);
  if (!ctx) throw new Error('useRegistry must be used inside <RegistryProvider>');
  return ctx;
}
