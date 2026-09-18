#!/usr/bin/env python3
"""Runs on quantum-obs. prom-range.py <start> <end> <tier> <out.json>

Queries Prometheus (localhost:9090) at step=15 and Loki (localhost:3100) for
the window, writes one JSON document. Python 3.9-compatible, stdlib only.

Dead counters (always 0 on every tier, noted in the report): sqc_txs_failed_total,
sqc_rpc_requests_total. They are still fetched so the report can show it.
"""
import json, re, sys, urllib.parse, urllib.request

start, end, tier, out = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3], sys.argv[4]
T = 'tier="%s"' % tier

EXPRS = {
    "height": "sqc_chain_height{%s}" % T,
    "blocks_per_s": "rate(sqc_chain_height{%s}[1m])" % T,
    "block_time_s": "1/rate(sqc_chain_height{%s}[1m])" % T,
    "txs_per_s": "rate(sqc_txs_processed_total{%s}[1m])" % T,
    "txs_per_block": "increase(sqc_txs_processed_total{%s}[1m])/increase(sqc_chain_height{%s}[1m])" % (T, T),
    "txs_total": "sqc_txs_processed_total{%s}" % T,
    "mempool": "sqc_mempool_size{%s}" % T,
    "ai_batch_size": "sqc_ai_batch_size{%s}" % T,
    "txs_failed_total_DEAD": "sqc_txs_failed_total{%s}" % T,
    "rpc_requests_total_DEAD": "sqc_rpc_requests_total{%s}" % T,
    # host health, as on the dashboard
    "cpu_pct": '100 - (avg by (tier) (rate(node_cpu_seconds_total{mode="idle", %s}[5m])) * 100)' % T,
    "cpu_pct_1m": '100 - (avg by (tier) (rate(node_cpu_seconds_total{mode="idle", %s}[1m])) * 100)' % T,
    "mem_used_pct": "100 * (1 - node_memory_MemAvailable_bytes{%s} / node_memory_MemTotal_bytes{%s})" % (T, T),
    "mem_avail_bytes": "node_memory_MemAvailable_bytes{%s}" % T,
    "load1": "node_load1{%s}" % T,
    "net_in_bps": 'sum by (tier) (rate(node_network_receive_bytes_total{device!~"lo|veth.*|docker.*|br-.*", %s}[5m]))' % T,
    "net_out_bps": 'sum by (tier) (rate(node_network_transmit_bytes_total{device!~"lo|veth.*|docker.*|br-.*", %s}[5m]))' % T,
    "disk_free_bytes": 'node_filesystem_avail_bytes{mountpoint="/", %s}' % T,
}


def get(url, params):
    q = urllib.parse.urlencode(params)
    with urllib.request.urlopen(url + "?" + q, timeout=120) as r:
        return json.load(r)


prom = {}
for name, expr in EXPRS.items():
    try:
        d = get("http://localhost:9090/api/v1/query_range", {"query": expr, "start": start, "end": end, "step": 15})
        series = d["data"]["result"]
        prom[name] = [[float(t), float(v)] for t, v in series[0]["values"]] if series else []
    except Exception as e:  # keep going; the report notes the gap
        prom[name] = []
        sys.stderr.write("prom %s: %s\n" % (name, e))

ANSI = re.compile(r"\x1b\[[0-9;]*m")
HEIGHT = re.compile(r"height=(\d+)")
TXS = re.compile(r"txs=(\d+)")
STREAM = '{job="sqc-containers",service="sqc-node",tier="%s"}' % tier


def loki(query, s, e, limit=5000):
    d = get("http://localhost:3100/loki/api/v1/query_range",
            {"query": query, "start": str(s * 10**9), "end": str(e * 10**9), "limit": limit, "direction": "forward"})
    for stream in d["data"]["result"]:
        for ts, line in stream["values"]:
            yield int(ts) / 1e9, ANSI.sub("", line)


blocks = {}
notable = []
# 5-minute chunks keep every query under Loki's 5000-entry cap at 1 block/s.
t = start
while t < end:
    e = min(t + 300, end)
    try:
        for ts, line in loki(STREAM + ' |= "block committed" != "valid QC"', t, e):
            h, x = HEIGHT.search(line), TXS.search(line)
            if h and x:
                blocks[int(h.group(1))] = [round(ts, 3), int(x.group(1))]
    except Exception as ex:
        sys.stderr.write("loki blocks %d: %s\n" % (t, ex))
    try:
        for ts, line in loki(STREAM + ' |~ "(?i)evict|panic|OOM|fatal|mempool full|rate limit|slow|stall|timeout"', t, e, 2000):
            notable.append([round(ts, 3), line[:300]])
    except Exception as ex:
        sys.stderr.write("loki notable %d: %s\n" % (t, ex))
    t = e

doc = {
    "tier": tier, "start": start, "end": end, "step": 15,
    "exprs": EXPRS, "prom": prom,
    "blocks": [[h, v[0], v[1]] for h, v in sorted(blocks.items())],
    "notable": notable,
    "dead_counters": ["sqc_txs_failed_total", "sqc_rpc_requests_total"],
}
with open(out, "w") as f:
    json.dump(doc, f)
print("wrote %s: %d series, %d blocks, %d notable lines" % (out, len(prom), len(blocks), len(notable)))
