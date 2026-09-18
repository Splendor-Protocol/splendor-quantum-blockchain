#!/usr/bin/env bash
# SSM helpers (source after env.sh). Pattern: infra/observability/bin/deploy-alloy.sh.
#
#   ssm_run  <instance> <script-file> [timeout-s]   run a bash script, print its stdout, fail on error
#   ssm_sh   <instance> "<commands>"   [timeout-s]   same, inline
#   ssm_fetch_tar <instance> <remote-dir> <local.tar.gz>   pull a directory back (gzip+base64 paged
#                                                          through get-command-invocation, 20k chars/page)
#   ssm_fetch_file <instance> <remote-file> <local-file>
#
# StandardOutputContent is capped at 24,000 characters per invocation; scripts
# that need to return more write a file on the host and the caller pages it.

_ssm_params() { # <script-file> <timeout-s> -> params json path
  local p; p=$(mktemp)
  python3 - "$1" "$2" "$p" <<'PY'
import json, sys
body = open(sys.argv[1]).read()
if not body.startswith("#!"):
    body = "#!/bin/bash\n" + body  # RunShellScript otherwise executes with dash
json.dump({"commands": [body], "executionTimeout": [str(int(sys.argv[2]))]}, open(sys.argv[3], "w"))
PY
  echo "$p"
}

# Returns the invocation status in $SSM_STATUS, stdout in $SSM_OUT, stderr in $SSM_ERR.
ssm_run() {
  local iid="$1" script="$2" timeout="${3:-600}"
  local params cmd st i
  params=$(_ssm_params "$script" "$timeout")
  cmd=$(aws ssm send-command --instance-ids "$iid" --document-name AWS-RunShellScript \
        --parameters "file://$params" --query Command.CommandId --output text)
  rm -f "$params"
  for ((i = 0; i < timeout / 3 + 20; i++)); do
    st=$(aws ssm get-command-invocation --command-id "$cmd" --instance-id "$iid" \
         --query Status --output text 2>/dev/null || echo Pending)
    case "$st" in Success|Failed|Cancelled|TimedOut) break ;; esac
    sleep 3
  done
  SSM_STATUS="$st"
  SSM_OUT=$(aws ssm get-command-invocation --command-id "$cmd" --instance-id "$iid" --query StandardOutputContent --output text)
  SSM_ERR=$(aws ssm get-command-invocation --command-id "$cmd" --instance-id "$iid" --query StandardErrorContent --output text)
  [ "$SSM_ERR" = "None" ] && SSM_ERR=""
  printf '%s\n' "$SSM_OUT"
  [ -n "$SSM_ERR" ] && printf '%s\n' "--- stderr ($iid) ---" "$SSM_ERR" >&2
  [ "$st" = "Success" ]
}

ssm_sh() {
  local iid="$1" body="$2" timeout="${3:-600}" f
  f=$(mktemp)
  printf '#!/bin/bash\nset -u\n%s\n' "$body" > "$f"
  ssm_run "$iid" "$f" "$timeout"
  local rc=$?
  rm -f "$f"
  return $rc
}

# Page a base64 file on the host back to a local file. 20,000 chars per page.
_ssm_page_b64() { # <instance> <remote-b64-file> <local-b64-file>
  local iid="$1" rf="$2" lf="$3" size off=0 page=20000 n=0
  size=$(ssm_sh "$iid" "stat -c %s '$rf'" 120 | tr -d '[:space:]') || return 1
  [[ "$size" =~ ^[0-9]+$ ]] || { echo "ssm: bad size '$size'" >&2; return 1; }
  : > "$lf"
  while [ "$off" -lt "$size" ]; do
    n=$((n + 1))
    ssm_sh "$iid" "tail -c +$((off + 1)) '$rf' | head -c $page" 120 | tr -d '[:space:]' >> "$lf" || return 1
    off=$((off + page))
    printf '\r  page %d/%d' "$n" "$(( (size + page - 1) / page ))" >&2
  done
  echo >&2
  local got; got=$(wc -c < "$lf" | tr -d ' ')
  [ "$got" -eq "$size" ] || { echo "ssm: fetched $got of $size chars" >&2; return 1; }
}

ssm_fetch_tar() { # <instance> <remote-dir> <local.tar.gz>
  local iid="$1" rdir="$2" out="$3" tmp="/tmp/ssm-fetch-$$.b64" b64
  b64=$(mktemp)
  ssm_sh "$iid" "tar czf - -C '$(dirname "$rdir")' '$(basename "$rdir")' | base64 -w0 > '$tmp' && stat -c 'b64 %s bytes' '$tmp'" 300 >/dev/null || return 1
  _ssm_page_b64 "$iid" "$tmp" "$b64" || return 1
  base64 -d < "$b64" > "$out" && rm -f "$b64"
  ssm_sh "$iid" "rm -f '$tmp'" 60 >/dev/null || true
  tar tzf "$out" >/dev/null
}

ssm_fetch_file() { # <instance> <remote-file> <local-file>
  local iid="$1" rf="$2" out="$3" tmp="/tmp/ssm-fetch-$$.b64" b64
  b64=$(mktemp)
  ssm_sh "$iid" "gzip -9c '$rf' | base64 -w0 > '$tmp'" 300 >/dev/null || return 1
  _ssm_page_b64 "$iid" "$tmp" "$b64" || return 1
  base64 -d < "$b64" | gunzip > "$out" && rm -f "$b64"
  ssm_sh "$iid" "rm -f '$tmp'" 60 >/dev/null || true
}

# /status of the node as seen from the host through the sqc-load-rpcproxy sidecar.
ssm_node_status() { ssm_sh "$DS_INSTANCE" "curl -s -m 5 http://127.0.0.1:$DS_PROXY_PORT/status" 60; }
