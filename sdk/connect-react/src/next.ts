/**
 * Copy SQC WASM assets for Next.js / Webpack apps (non-Vite).
 *
 *   // scripts/copy-sqc-wasm.mjs
 *   import { copySqcWasmToPublic } from '@splendor/connect-react/next';
 *   copySqcWasmToPublic('./public');
 *
 * Or CLI: npx sqc-copy-wasm ./public
 */
export { copySqcWasmAssets, findSqcWasmDist } from './vite.js';

import { copySqcWasmAssets } from './vite.js';
import { resolve } from 'node:path';

/** Copy signer assets into a Next.js `public/` (or any static dir). */
export function copySqcWasmToPublic(publicDir = 'public'): string {
  const dest = resolve(process.cwd(), publicDir);
  return copySqcWasmAssets(dest);
}

export default copySqcWasmToPublic;
