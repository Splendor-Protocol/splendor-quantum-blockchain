// Snapshot the widget manifests into catalog.json so the published CLI needs only
// @splendor/dapp-spec at runtime (no React, no connect-react install).
import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';

const out = fileURLToPath(new URL('../catalog.json', import.meta.url));
writeFileSync(out, JSON.stringify({ widgets: WIDGET_MANIFESTS }, null, 2) + '\n');
console.log(`[create-splendor-dapp] wrote ${WIDGET_MANIFESTS.length} widget manifests to catalog.json`);
