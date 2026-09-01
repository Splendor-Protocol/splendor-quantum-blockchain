# Generated mirror — edit the source of truth:
#   sdk/bridge/src/config/sqc.networks.json
#
# Keep in sync with:
#   npm run sync:config
# (copies here, verifies Go DerivationDomainMessage, refreshes wasmSha256 when WASM exists)
#
# This copy is for operators / Docker / docs discovery.
# The SDK always loads from src/config (compiled to dist/config).
#
# networkId is client-facing (splendor-local / testnet / mainnet).
# chainId is the wire protocol id (splendor-quantum-1) shared with the node.
