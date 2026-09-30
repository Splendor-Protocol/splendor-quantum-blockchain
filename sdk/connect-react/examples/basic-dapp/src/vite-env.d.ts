/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_SQC_NETWORK?: string;
  readonly VITE_SQC_WASM_SHA256?: string;
  readonly VITE_SQC_DIRECT_RPC?: string;
  /** Per-network overrides: VITE_SQC_<ID>_RPC_URL / _CHAIN_ID / _EXPLORER_URL (see networks.ts). */
  readonly [key: `VITE_SQC_${string}`]: string | undefined;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
