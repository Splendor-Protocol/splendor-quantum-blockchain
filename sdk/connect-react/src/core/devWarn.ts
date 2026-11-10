const warned = new Set<string>();

/** Bundlers replace one or the other: `process.env.NODE_ENV` (webpack, Next.js) or `import.meta.env.PROD` (Vite). */
function isProduction(): boolean {
  try {
    if ((import.meta as { env?: { PROD?: boolean } }).env?.PROD === true) return true;
  } catch {
    /* not an ESM bundle with import.meta.env */
  }
  try {
    return typeof process !== 'undefined' && process.env?.NODE_ENV === 'production';
  } catch {
    return false;
  }
}

/**
 * Console warning for a misconfiguration that would otherwise fail silently. Development builds
 * only, and once per `key` so a re-rendering component does not flood the console.
 */
export function devWarn(key: string, message: string): void {
  if (isProduction() || warned.has(key)) return;
  warned.add(key);
  console.warn(`@splendor/connect-react: ${message}`);
}

/** Test hook. */
export function __resetDevWarnings(): void {
  warned.clear();
}
