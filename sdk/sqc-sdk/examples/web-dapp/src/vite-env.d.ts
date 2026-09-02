/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_SQC_NETWORK?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
