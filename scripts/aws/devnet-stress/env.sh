#!/usr/bin/env bash
# Shared settings for the DEVNET stress test. Source this; do not run it.
#
# Everything here targets the devnet tier only. The guards at the bottom refuse
# any RPC or chain id that is not devnet, so a copy-paste can never point this
# at testnet or mainnet.

export AWS_PROFILE="${AWS_PROFILE:-}"
export AWS_REGION="${AWS_REGION:-us-east-1}"
export AWS_DEFAULT_REGION="$AWS_REGION"
# The AWS account id for the devnet tier. Deliberately NOT defaulted: account
# ids, instance ids and bucket names are infra surface and stay out of the
# public repo — export them per run (see the infra repo / AWS console).
export DS_ACCOUNT="${DS_ACCOUNT:-}"

# The chain under test.
export DS_TIER=devnet
export DS_RPC="${DS_RPC:-https://quantum-rpc-devnet.splendor.org}"
export DS_CHAIN_ID="${DS_CHAIN_ID:-splendor-quantum-devnet-1}"
export DS_INSTANCE="${DS_INSTANCE:-}"       # splendor-quantum-devnet-rpc (the single validator)
export DS_OBS_INSTANCE="${DS_OBS_INSTANCE:-}" # splendor-quantum-obs (Prometheus :9090, Loki :3100)
export DS_REQUIRED_TAG="${DS_REQUIRED_TAG:-v1.1.1}"   # /status.image_tag the node must report before traffic

# Funding. The faucet key is managed by infra/bin/qkey; the 20 funders are
# txload senders of the faucet at --sender-offset 100000, so their seeds are
# SHA3-256("sqc-txload/v1/" + faucet address + "/" + (100000+i)).
export DS_FAUCET_KEY=faucet-devnet
export DS_FAUCET_ADDR=SQ:433c89047f51f8c8ea8e0b54e7f132569ce0f7e01645b59e7ab4c7e9eea73bd9
export DS_K="${DS_K:-20}"                    # driver containers = funders
export DS_FUNDER_OFFSET=100000
export DS_FUNDER_SPLD="${DS_FUNDER_SPLD:-2200}"   # per funder; 20 x 2200 = 44k SPLD (budget cap 50k)
export DS_SENDER_FUND_UNITS=1000000000000000000   # 1 SPLD per sender
export DS_TX_AMOUNT_UNITS=1000000000000000        # 0.001 SPLD per transfer

# Host layout on the validator box and the compose network the node lives in.
export DS_HOST_ROOT=/srv/sqc-load
export DS_NODE_NET=splendor-quantum-node_default
export DS_NODE_SVC=sqc-node
export DS_PROXY_PORT="${DS_PROXY_PORT:-18080}"   # sqc-load-rpcproxy: host 127.0.0.1:PORT -> sqc-node:8080; override if another sidecar holds 18080
export DS_DRIVER_IMAGE=alpine:3.20
export DS_SOCAT_IMAGE=alpine/socat:1.8.0.3
export DS_DRIVER_CPUS="${DS_DRIVER_CPUS:-0.1}"     # per container; 20 x 0.1 = 2 of the 4 vCPUs at most
export DS_DRIVER_MEM="${DS_DRIVER_MEM:-48m}"
# FUND pass budget. A funder can hold 64 pending (mempool MaxPerAddr), so funding
# N senders takes roughly N/64 blocks per funder plus confirmation; 10k accounts
# took ~5 min, a from-scratch 30k stage needs ~15. Bounded by the driver's
# --duration / --fund-deadline and the host-side wait.
export DS_FUND_DURATION="${DS_FUND_DURATION:-25m}"
export DS_FUND_DEADLINE="${DS_FUND_DEADLINE:-20m}"
export DS_FUND_WAIT_S="${DS_FUND_WAIT_S:-1500}"

# Staging bucket for the arm64 binary (deleted by 90-cleanup.sh).
export DS_BUCKET="${DS_BUCKET:-}"

# Local working directory: keys/, runs/, figures/, bin/. Never committed.
export DS_WORK="${DS_WORK:-$HOME/.sqc-devnet-stress}"
export DS_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
export DS_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.14}"
# infra/bin/qkey (managed devnet/testnet keys); the infra repo is a sibling of the chain repo in the workspace.
export DS_QKEY="${DS_QKEY:-$HOME/Workspace/splendor/infra/bin/qkey}"

# Balance of an SQ: address on devnet, in whole SPLD (integer) or with 3 decimals.
ds_balance_spld() { curl -sS -m 15 "$DS_RPC/balance/$1" | python3 -c 'import json,sys; b=int(json.load(sys.stdin)["balance"]); print("%.3f" % (b/10**18))'; }

# Stage -> senders per container (x DS_K containers = accounts).
ds_stage_senders() {
  case "$1" in
    s10k) echo 500 ;;
    s20k) echo 1000 ;;
    s30k) echo 1500 ;;
    s40k) echo 2000 ;;
    sustained) echo 2000 ;;
    *) return 1 ;;
  esac
}

ds_log()  { printf '\033[0;32m[devnet-stress]\033[0m %s\n' "$*"; }
ds_warn() { printf '\033[0;33m[devnet-stress]\033[0m %s\n' "$*" >&2; }
ds_die()  { printf '\033[0;31m[devnet-stress]\033[0m %s\n' "$*" >&2; exit 1; }

# Guards: devnet only, always. The per-run infra ids must be supplied by the
# operator — none are baked into this public repo.
case "$DS_RPC" in *quantum-rpc-devnet.splendor.org*) ;; *) ds_die "DS_RPC=$DS_RPC is not the devnet RPC" ;; esac
[ "$DS_CHAIN_ID" = "splendor-quantum-devnet-1" ] || ds_die "DS_CHAIN_ID=$DS_CHAIN_ID is not devnet"
[ -n "$AWS_PROFILE" ] || ds_die "AWS_PROFILE is not set (export the devnet account's profile)"
[ -n "$DS_ACCOUNT" ] || ds_die "DS_ACCOUNT is not set (export the devnet account id)"
[ -n "$DS_INSTANCE" ] || ds_die "DS_INSTANCE is not set (export the devnet validator instance id)"
[ -n "$DS_OBS_INSTANCE" ] || ds_die "DS_OBS_INSTANCE is not set (export the observability instance id)"
[ -n "$DS_BUCKET" ] || ds_die "DS_BUCKET is not set (export the staging bucket name)"
mkdir -p "$DS_WORK"/{keys,runs,figures,bin,report}
chmod 700 "$DS_WORK" "$DS_WORK/keys"
