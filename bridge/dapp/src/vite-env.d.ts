/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_BURN_CONTRACT?: string;
  readonly VITE_SPLV_TOKEN?: string;
  readonly VITE_LEGACY_CHAIN_ID?: string;
  readonly VITE_BRIDGE_ADDRESS?: string;
  readonly VITE_SQC_NETWORK?: string;
  readonly VITE_SQC_WASM_SHA256?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
