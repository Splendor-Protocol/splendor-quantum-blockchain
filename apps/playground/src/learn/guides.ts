import connectGuide from '../../../../sdk/connect-react/docs/GUIDE.md?raw';
import connectReadme from '../../../../sdk/connect-react/README.md?raw';
import dappIntegration from '../../../../sdk/sqc-sdk/docs/DAPP_INTEGRATION.md?raw';
import errors from '../../../../sdk/sqc-sdk/docs/ERRORS.md?raw';
import wasmHosting from '../../../../sdk/sqc-sdk/docs/WASM_HOSTING.md?raw';
import sdkReadme from '../../../../sdk/sqc-sdk/README.md?raw';
import dappSpecReadme from '../../../../packages/dapp-spec/README.md?raw';

/** Docs bundled at build time from the same repo, so the guide always matches the packages. */
export const GUIDES: { id: string; title: string; group: string; markdown: string }[] = [
  { id: 'widgets', title: 'Widgets & hooks guide', group: 'React (connect-react)', markdown: connectGuide },
  { id: 'connect-react', title: 'connect-react overview', group: 'React (connect-react)', markdown: connectReadme },
  { id: 'sdk', title: 'SDK overview', group: 'SDK (sqc-sdk)', markdown: sdkReadme },
  { id: 'dapp-integration', title: 'dApp integration', group: 'SDK (sqc-sdk)', markdown: dappIntegration },
  { id: 'errors', title: 'Error codes', group: 'SDK (sqc-sdk)', markdown: errors },
  { id: 'wasm-hosting', title: 'Hosting the signer (WASM)', group: 'SDK (sqc-sdk)', markdown: wasmHosting },
  { id: 'dapp-spec', title: 'dApp spec & code generator', group: 'Playground', markdown: dappSpecReadme },
];

/** `code → { meaning, retry }` from the table in ERRORS.md, for decoding recipe failures. */
export const ERROR_HINTS: Record<string, { meaning: string; retry: string }> = (() => {
  const out: Record<string, { meaning: string; retry: string }> = {};
  for (const line of errors.split(/\r?\n/)) {
    const m = /^\|\s*`([A-Z_]+)`\s*\|\s*(.+?)\s*\|\s*(.+?)\s*\|\s*$/.exec(line);
    if (m) out[m[1]!] = { meaning: m[2]!, retry: m[3]! };
  }
  return out;
})();
