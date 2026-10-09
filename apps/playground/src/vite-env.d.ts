/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_PLAYGROUND_API_URL?: string;
  readonly VITE_SQC_DIRECT_RPC?: string;
  readonly VITE_SQC_WASM_SHA256?: string;
  readonly VITE_SQC_EXEC_SHA256?: string;
  readonly VITE_PLAYGROUND_DEFAULT_NETWORK?: string;
  /** Defined by vite.config.ts from the workspace package.json files. */
  readonly VITE_SQC_SDK_VERSION: string;
  readonly VITE_CONNECT_REACT_VERSION: string;
}

declare module '*.md?raw' {
  const content: string;
  export default content;
}
