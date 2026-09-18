#!/usr/bin/env python3
"""Report for the devnet stress test.

    50-report.py [--work DIR] [run-id ...]

Reads $DS_WORK/runs/<run-id>/{run.json,metrics.json,host/...} for every run
(default: all with a metrics.json), $DS_WORK/preflight-*.json for the baseline,
and writes

    $DS_WORK/figures/<run-id>-{throughput,mempool,blocks,host,latency,rejections}.svg
    $DS_WORK/report/summary.json   per-stage numbers + verdict + evidence
    $DS_WORK/report/summary.md     the same as a Markdown table + notes

Figures are black and white only (line styles and markers carry the series).
Dead counters: sqc_txs_failed_total and sqc_rpc_requests_total never move on
any tier; they are pulled and reported as such, never plotted as data.
"""
import argparse, glob, json, os, statistics, sys
from datetime import datetime, timezone

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

plt.rcParams.update({"font.family": "monospace", "font.size": 8, "axes.edgecolor": "black",
                     "axes.labelcolor": "black", "xtick.color": "black", "ytick.color": "black",
                     "figure.facecolor": "white", "axes.facecolor": "white", "savefig.facecolor": "white",
                     "axes.prop_cycle": plt.cycler(color=["black"] * 6, linestyle=["-", "--", ":", "-.", (0, (5, 1)), (0, (1, 1))])})

BASELINE_BT = 1.0   # devnet block interval (SQC_BLOCK_INTERVAL=1)


def epoch(ts):
    return datetime.strptime(ts, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc).timestamp()


def pct(xs, p):
    if not xs:
        return None
    s = sorted(xs)
    return s[min(len(s) - 1, int(len(s) * p / 100))]


def load_pass(pdir):
    """Aggregate one pass directory (fund/burst/sustained) of cNN/ files."""
    out = {"containers": 0, "summaries": 0, "submitted": 0, "accepted": 0, "rejected": 0, "rate_limited_429": 0,
           "transport_errors": 0, "dup_pending": 0, "nonce_resyncs": 0, "gave_up": 0, "rejected_by": {},
           "rtt_p50": [], "rtt_p95": [], "rtt_max": [], "incl_p50": [], "incl_p95": [], "incl_max": [], "incl_n": 0,
           "incl_timeouts": 0, "elapsed_s": [], "bins": {}, "started": [], "ended": [], "funding_failed": 0, "log_errors": []}
    for c in sorted(glob.glob(os.path.join(pdir, "c*"))):
        out["containers"] += 1
        sp = os.path.join(c, "summary.json")
        if os.path.exists(sp):
            d = json.load(open(sp)); out["summaries"] += 1
            for k in ("submitted", "accepted", "rejected", "rate_limited_429", "transport_errors", "dup_pending", "nonce_resyncs"):
                out[k] += d.get(k, 0)
            out["gave_up"] += d.get("one_shot_gave_up", 0)
            for k, v in d.get("rejected_by", {}).items():
                out["rejected_by"][k] = out["rejected_by"].get(k, 0) + v
            r, i = d["rtt_ms"], d["inclusion_s"]
            if r["n"]:
                out["rtt_p50"].append(r["p50"]); out["rtt_p95"].append(r["p95"]); out["rtt_max"].append(r["max"])
            if i["n"]:
                out["incl_p50"].append(i["p50"]); out["incl_p95"].append(i["p95"]); out["incl_max"].append(i["max"]); out["incl_n"] += int(i["n"])
            out["incl_timeouts"] += d.get("inclusion_timeouts", 0)
            out["elapsed_s"].append(d["elapsed_s"]); out["started"].append(epoch(d["started_at"])); out["ended"].append(epoch(d["ended_at"]))
        lp = os.path.join(c, "txload.log")
        if os.path.exists(lp):
            for line in open(lp, errors="replace"):
                if line.startswith("funding:") or "funding not confirmed" in line:
                    out["funding_failed"] += 1
                if line.startswith("txload: ") and any(w in line for w in ("error", "fatal", "could not")):
                    out["log_errors"].append(line.strip()[:160])
        st = os.path.join(c, "stats.jsonl")
        if os.path.exists(st):
            for line in open(st):
                try:
                    d = json.loads(line)
                except ValueError:
                    continue
                b = int(epoch(d["ts"]) // 5 * 5)
                bb = out["bins"].setdefault(b, {"submitted": 0, "accepted": 0, "r429": 0, "rtt50": [], "rtt95": [], "incl95": [], "pool": []})
                bb["submitted"] += d["submitted"]; bb["accepted"] += d["accepted"]; bb["r429"] += d["rate_limited_429"]
                if d["submitted"]:
                    bb["rtt50"].append(d["rtt_ms_p50"]); bb["rtt95"].append(d["rtt_ms_p95"])
                if d.get("incl_samples"):
                    bb["incl95"].append(d["incl_s_p95"])
                bb["pool"].extend(v for v in d.get("mempool", {}).values() if v is not None and v >= 0)
    return out


def load_run(work, run):
    root = os.path.join(work, "runs", run)
    r = json.load(open(os.path.join(root, "run.json")))
    m = json.load(open(os.path.join(root, "metrics.json")))
    passes = {}
    for p in ("fund", "burst", "sustained"):
        pdir = os.path.join(root, "host", p)
        if os.path.isdir(pdir):
            passes[p] = load_pass(pdir)
    watch = []
    wp = os.path.join(root, "host", "watch.jsonl")
    if os.path.exists(wp):
        for line in open(wp):
            try:
                watch.append(json.loads(line))
            except ValueError:
                pass
    rule = None
    sp = os.path.join(root, "host", "stop-rule.txt")
    if os.path.exists(sp):
        rule = open(sp).read().strip()
    return r, m, passes, watch, rule


def window(series, a, b):
    return [(t, v) for t, v in series if a <= t <= b]


def analyse(run, m, passes, watch, rule):
    t = run["t"]
    topo = run.get("topology", {}) or {}
    main = passes.get("sustained") or passes.get("burst") or {}
    mode = run["mode"]
    b0, b1 = t["burst_start"], t["burst_end"]
    w0, w1 = b0, t["drain_end"]
    prom = m["prom"]
    blocks = m["blocks"]  # [height, ts, txs]
    bw = [b for b in blocks if w0 - 5 <= b[1] <= w1 + 5]
    intervals = [(bw[i][1], bw[i][1] - bw[i - 1][1]) for i in range(1, len(bw))]
    bt_peak = max((d for _, d in intervals), default=None)
    slow = [ts for ts, d in intervals if d > 1.5 * BASELINE_BT]
    recovery_s = (max(slow) - b0) if slow else 0.0
    over5 = sum(1 for _, d in intervals if d > 5)
    txs_per_block_peak = max((b[2] for b in bw), default=0)
    pool_prom = [v for _, v in window(prom["mempool"], w0, w1)]
    pool_tx = [v for b in main.get("bins", {}).values() for v in b["pool"]]
    cpu = [v for _, v in window(prom["cpu_pct_1m"], w0, w1)] or [v for _, v in window(prom["cpu_pct"], w0, w1)]
    mem = [v for _, v in window(prom["mem_used_pct"], w0, w1)]
    load = [v for _, v in window(prom["load1"], w0, w1)]
    txps = [v for _, v in window(prom["txs_per_s"], w0, w1)]
    ww = [x for x in watch if w0 <= x["epoch"] <= w1]
    wall = watch
    healthz_bad = sum(1 for x in wall if str(x["healthz"]) != "200")
    restarts = max((x["restarts"] for x in wall), default=0)
    oom = any(str(x["oom"]).lower() == "true" for x in wall)
    killed = max((x["killed"] for x in wall), default=0)
    node_cpu = []
    for x in ww:
        try:
            node_cpu.append(float(str(x["node_cpu"]).rstrip("%")))
        except ValueError:
            pass
    mem_avail_min = min((x["mem_avail_mb"] for x in wall), default=None)
    included_window = 0
    if prom["txs_total"]:
        tw = window(prom["txs_total"], w0 - 15, w1 + 15)
        if len(tw) > 1:
            included_window = tw[-1][1] - tw[0][1]
    # heights: stall = longest gap between consecutive watch samples with equal height
    stall = 0; run_len = 0
    for i in range(1, len(wall)):
        if wall[i]["height"] == wall[i - 1]["height"] and wall[i]["height"]:
            run_len += wall[i]["epoch"] - wall[i - 1]["epoch"]; stall = max(stall, run_len)
        else:
            run_len = 0
    submitted, accepted = main.get("submitted", 0), main.get("accepted", 0)
    acc_ratio = accepted / submitted if submitted else 0
    drain_s = t["drain_end"] - b1 - 180  # the quiet rule needs 3 quiet minutes; subtract them
    evidence, verdict = [], "PASS"
    if rule:
        verdict = "FAIL"; evidence.append("stop rule fired: " + rule)
    if restarts > 0 or oom:
        verdict = "FAIL"; evidence.append("node container restarts=%d oom=%s" % (restarts, oom))
    if killed:
        verdict = "FAIL"; evidence.append("dmesg shows %d killed process lines" % killed)
    if healthz_bad and healthz_bad * 10 >= 60:
        verdict = "FAIL"; evidence.append("healthz not 200 in %d samples (%ds)" % (healthz_bad, healthz_bad * 10))
    if stall >= 120:
        verdict = "FAIL"; evidence.append("height stalled for %ds" % stall)
    if verdict != "FAIL":
        if over5:
            verdict = "DEGRADED"; evidence.append("%d block intervals > 5s (peak %.2fs, largest block %d txs)" % (over5, bt_peak or 0, txs_per_block_peak))
        if healthz_bad:
            verdict = "DEGRADED"; evidence.append("healthz not 200 in %d samples" % healthz_bad)
        if acc_ratio < 0.95 and submitted:
            verdict = "DEGRADED"; evidence.append("accepted %.1f%% of submitted" % (100 * acc_ratio))
        if drain_s > 600:
            verdict = "DEGRADED"; evidence.append("mempool took %ds to drain" % drain_s)
        if main.get("gave_up"):
            verdict = "DEGRADED"; evidence.append("%d one-shot senders gave up" % main["gave_up"])
    interference = topo.get("changed") or []
    post = topo.get("post", {})
    valset = post.get("valset")
    if interference:
        verdict = "CONTAMINATED"; evidence.insert(0, "external change mid-stage: " + "; ".join(interference))
    elif topo.get("external_interference"):
        evidence.append("note: %s co-resident validator container(s), validator_set_size=%s (devnet no longer single-validator)" % (post.get("valcontainers"), valset))
    if verdict == "PASS":
        evidence.append("restarts=0 oom=false healthz 200 in all %d samples, no stall, block time peak %.2fs, %d/%d accepted, drained in %ds" % (
            len(wall), bt_peak or 0, accepted, submitted, max(drain_s, 0)))
    ms_per_tx = None
    big = [(d, txs) for (ts, d), (_, _, txs) in zip(intervals, bw[1:]) if txs >= 1000]
    if big:
        ms_per_tx = round(1000 * sum(d for d, _ in big) / sum(txs for _, txs in big), 2)
    return {
        "exec_ms_per_tx_blocks_ge_1000": ms_per_tx,
        "run": run["run"], "stage": run["stage"], "mode": mode, "accounts": run["accounts"], "containers": run["containers"],
        "senders_per_container": run["senders_per_container"], "image_tag": run["image_tag"],
        "burst_start_utc": datetime.fromtimestamp(b0, timezone.utc).strftime("%H:%M:%S"), "burst_s": b1 - b0,
        "fund": {k: passes["fund"][k] for k in ("submitted", "accepted", "rejected", "rate_limited_429", "rejected_by", "funding_failed")} if "fund" in passes else None,
        "submitted": submitted, "accepted": accepted, "accepted_pct": round(100 * acc_ratio, 2),
        "included_prom_window": included_window, "rejected": main.get("rejected", 0), "rejected_by": main.get("rejected_by", {}),
        "rate_limited_429": main.get("rate_limited_429", 0), "transport_errors": main.get("transport_errors", 0),
        "gave_up": main.get("gave_up", 0), "summaries": "%d/%d" % (main.get("summaries", 0), main.get("containers", 0)),
        "offered_tps_peak_5s": max((b["submitted"] / 5 for b in main.get("bins", {}).values()), default=0),
        "accepted_tps_peak_5s": max((b["accepted"] / 5 for b in main.get("bins", {}).values()), default=0),
        "accepted_tps_mean": round(accepted / (b1 - b0), 2) if b1 > b0 else None,
        "included_tps_peak_prom_1m": max(txps, default=None), "txs_per_block_peak": txs_per_block_peak,
        "mempool_peak_prom": max(pool_prom, default=None), "mempool_peak_txload": max(pool_tx, default=None),
        "block_time_peak_s": bt_peak, "block_intervals_over_5s": over5, "block_time_recovery_s": round(recovery_s, 1),
        "blocks_in_window": len(bw), "drain_s": max(drain_s, 0),
        "cpu_pct_peak_prom": max(cpu, default=None), "node_cpu_pct_peak_docker": max(node_cpu, default=None),
        "mem_used_pct_peak": max(mem, default=None), "mem_avail_min_mb": mem_avail_min, "load1_peak": max(load, default=None),
        "restarts": restarts, "oom": oom, "healthz_bad_samples": healthz_bad, "height_stall_s": stall, "stop_rule": rule,
        "rtt_ms_p50": round(statistics.median(main["rtt_p50"]), 1) if main.get("rtt_p50") else None,
        "rtt_ms_p95": round(max(main["rtt_p95"]), 1) if main.get("rtt_p95") else None,
        "incl_s_p50": round(statistics.median(main["incl_p50"]), 2) if main.get("incl_p50") else None,
        "incl_s_p95": round(max(main["incl_p95"]), 2) if main.get("incl_p95") else None, "incl_samples": main.get("incl_n", 0),
        "verdict": verdict, "evidence": evidence, "notable_log_lines": len(m["notable"]),
        "valset_end": valset, "valcontainers_end": post.get("valcontainers"), "topology_changed": interference,
    }


def save(fig, path):
    fig.savefig(path)
    fig.savefig(path[:-4] + ".png", dpi=110)
    plt.close(fig)


def vlines(ax, t, mode):
    for k, ls in (("fund_start", ":"), ("fund_end", ":"), ("burst_start", "-"), ("burst_end", "-"), ("drain_end", "--")):
        if t.get(k):
            ax.axvline((t[k] - t["burst_start"]) / 60, color="black", linewidth=0.6, linestyle=ls)


def rel(ts, t0):
    return [(x - t0) / 60 for x in ts]


def gapped(ts, ys, maxgap):
    """Break a line where consecutive samples are more than maxgap seconds apart."""
    xs, out = [], []
    for i, (t, y) in enumerate(zip(ts, ys)):
        if i and t - ts[i - 1] > maxgap:
            xs.append(t); out.append(float("nan"))
        xs.append(t); out.append(y)
    return xs, out


def figures(work, run, m, passes, watch, a):
    t = run["t"]; t0 = t["burst_start"]; mode = run["mode"]
    main = passes.get(mode) or passes.get("burst") or {}
    fig_dir = os.path.join(work, "figures"); os.makedirs(fig_dir, exist_ok=True)
    name = run["run"]
    prom = m["prom"]
    x0 = (t.get("fund_start") or t0) - t0 - 120
    x1 = t["run_end"] - t0 + 60

    # 1. throughput
    fig, ax = plt.subplots(figsize=(9, 3.2))
    bins = {}
    for p in passes.values():
        for b, v in p["bins"].items():
            bb = bins.setdefault(b, {"submitted": 0, "accepted": 0}); bb["submitted"] += v["submitted"]; bb["accepted"] += v["accepted"]
    bs = sorted(bins)
    if bs:
        xs, ys = gapped(bs, [bins[b]["submitted"] / 5 for b in bs], 15); ax.plot(rel(xs, t0), ys, label="offered (txload submits)", linestyle="-")
        xs, ys = gapped(bs, [bins[b]["accepted"] / 5 for b in bs], 15); ax.plot(rel(xs, t0), ys, label="accepted (202)", linestyle="--")
    if m["blocks"]:
        per5 = {}
        for h, ts, txs in m["blocks"]:
            per5[int(ts // 5 * 5)] = per5.get(int(ts // 5 * 5), 0) + txs
        ks = sorted(per5)
        xs, ys = gapped(ks, [per5[k] / 5 for k in ks], 15); ax.plot(rel(xs, t0), ys, label="included (block txs, Loki)", linestyle=":")
    if prom["txs_per_s"]:
        ax.plot(rel([x for x, _ in prom["txs_per_s"]], t0), [v for _, v in prom["txs_per_s"]], label="included rate(1m) Prometheus", linestyle="-.")
    vlines(ax, t, mode)
    ax.set_xlim(x0 / 60, x1 / 60); ax.set_xlabel("minutes from burst start"); ax.set_ylabel("tx/s")
    ax.set_title("%s — offered vs accepted vs included" % name); ax.legend(frameon=False, loc="upper right")
    fig.tight_layout(); save(fig, os.path.join(fig_dir, name + "-throughput.svg"))

    # 2. mempool
    fig, ax = plt.subplots(figsize=(9, 3))
    if prom["mempool"]:
        ax.plot(rel([x for x, _ in prom["mempool"]], t0), [v for _, v in prom["mempool"]], label="sqc_mempool_size (Prometheus, 30s scrape)", linestyle="-")
    pb = {}
    for p in passes.values():
        for b, v in p["bins"].items():
            if v["pool"]:
                pb[b] = max(pb.get(b, 0), max(v["pool"]))
    ks = sorted(pb)
    if ks:
        xs, ys = gapped(ks, [pb[k] for k in ks], 15); ax.plot(rel(xs, t0), ys, label="/status mempool_size seen by txload (5s)", linestyle="--")
    vlines(ax, t, mode)
    ax.set_xlim(x0 / 60, x1 / 60); ax.set_xlabel("minutes from burst start"); ax.set_ylabel("pending txs")
    ax.set_title("%s — mempool depth" % name); ax.legend(frameon=False)
    fig.tight_layout(); save(fig, os.path.join(fig_dir, name + "-mempool.svg"))

    # 3. blocks
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(9, 4.6), sharex=True)
    bl = m["blocks"]
    if len(bl) > 1:
        ax1.plot(rel([b[1] for b in bl[1:]], t0), [bl[i][1] - bl[i - 1][1] for i in range(1, len(bl))], linestyle="-", linewidth=0.7)
        ax1.axhline(5, color="black", linestyle=":", linewidth=0.6); ax1.set_ylabel("block interval s")
        ax2.plot(rel([b[1] for b in bl], t0), [b[2] for b in bl], linestyle="-", linewidth=0.7); ax2.set_ylabel("txs per block")
    vlines(ax1, t, mode); vlines(ax2, t, mode)
    ax1.set_title("%s — block time (dotted: 5 s stop rule) and txs per block, from node logs" % name)
    ax2.set_xlim(x0 / 60, x1 / 60); ax2.set_xlabel("minutes from burst start")
    fig.tight_layout(); save(fig, os.path.join(fig_dir, name + "-blocks.svg"))

    # 4. host
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(9, 4.6), sharex=True)
    for key, label, ls in (("cpu_pct_1m", "host CPU % (1m)", "-"), ("mem_used_pct", "host memory used %", "--")):
        if prom.get(key):
            ax1.plot(rel([x for x, _ in prom[key]], t0), [v for _, v in prom[key]], label=label, linestyle=ls)
    nc = [(x["epoch"], float(str(x["node_cpu"]).rstrip("%"))) for x in watch if str(x["node_cpu"]).rstrip("%").replace(".", "", 1).isdigit()]
    if nc:
        ax1.plot(rel([x for x, _ in nc], t0), [v for _, v in nc], label="sqc-node CPU % (docker stats, 100=1 core)", linestyle=":")
    dc = [(x["epoch"], float(x["drivers_cpu_pct"])) for x in watch if x.get("drivers_cpu_pct") not in (None, "")]
    if dc:
        ax1.plot(rel([x for x, _ in dc], t0), [v for _, v in dc], label="drivers CPU % (sum)", linestyle="-.")
    ax1.set_ylabel("%"); ax1.legend(frameon=False, fontsize=7)
    if prom.get("load1"):
        ax2.plot(rel([x for x, _ in prom["load1"]], t0), [v for _, v in prom["load1"]], label="load1", linestyle="-")
    ma = [(x["epoch"], x["mem_avail_mb"] / 1024) for x in watch]
    if ma:
        ax3 = ax2.twinx(); ax3.plot(rel([x for x, _ in ma], t0), [v for _, v in ma], label="MemAvailable GiB", linestyle="--"); ax3.set_ylabel("GiB"); ax3.legend(frameon=False, loc="lower right", fontsize=7)
    ax2.set_ylabel("load1"); ax2.legend(frameon=False, loc="upper left", fontsize=7)
    vlines(ax1, t, mode); vlines(ax2, t, mode)
    ax1.set_title("%s — host CPU / memory / load (4 vCPU t4g.xlarge)" % name)
    ax2.set_xlim(x0 / 60, x1 / 60); ax2.set_xlabel("minutes from burst start")
    fig.tight_layout(); save(fig, os.path.join(fig_dir, name + "-host.svg"))

    # 5. latency
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(9, 4.6), sharex=True)
    bins = main.get("bins", {}); ks = sorted(b for b in bins if bins[b]["rtt50"])
    if ks:
        xs, ys = gapped(ks, [statistics.median(bins[k]["rtt50"]) for k in ks], 15); ax1.plot(rel(xs, t0), ys, label="submit RTT p50 (median of containers)", linestyle="-")
        xs, ys = gapped(ks, [max(bins[k]["rtt95"]) for k in ks], 15); ax1.plot(rel(xs, t0), ys, label="submit RTT p95 (max of containers)", linestyle="--")
    ax1.set_ylabel("ms"); ax1.legend(frameon=False, fontsize=7); ax1.set_yscale("log")
    ki = sorted(b for b in bins if bins[b]["incl95"])
    if ki:
        ax2.plot(rel(ki, t0), [max(bins[k]["incl95"]) for k in ki], label="inclusion latency p95 (1-in-50 sampled)", linestyle="-", marker=".", markersize=2)
    ax2.set_ylabel("s"); ax2.legend(frameon=False, fontsize=7)
    vlines(ax1, t, mode); vlines(ax2, t, mode)
    ax1.set_title("%s — submit round-trip and inclusion latency" % name)
    ax2.set_xlim(x0 / 60, x1 / 60); ax2.set_xlabel("minutes from burst start")
    fig.tight_layout(); save(fig, os.path.join(fig_dir, name + "-latency.svg"))

    # 6. rejections
    fig, ax = plt.subplots(figsize=(9, 2.8))
    classes = {}
    for pname, p in passes.items():
        for k, v in p["rejected_by"].items():
            classes.setdefault(k, {})[pname] = v
        if p["rate_limited_429"]:
            classes.setdefault("http-429 (retried)", {})[pname] = p["rate_limited_429"]
        if p["transport_errors"]:
            classes.setdefault("transport error (retried)", {})[pname] = p["transport_errors"]
    names = sorted(classes)
    pn = list(passes)
    wdt = 0.8 / max(len(pn), 1)
    hatches = ["", "////", "....", "xxxx"]
    for i, pname in enumerate(pn):
        ax.bar([j + i * wdt for j in range(len(names))], [classes[n].get(pname, 0) for n in names], wdt, label=pname, color="white", edgecolor="black", hatch=hatches[i % 4])
    ax.set_xticks([j + wdt * (len(pn) - 1) / 2 for j in range(len(names))]); ax.set_xticklabels(names or ["(none)"], rotation=15, ha="right")
    ax.set_ylabel("count"); ax.set_title("%s — rejections and retried errors by class" % name)
    if names:
        ax.set_yscale("symlog"); ax.legend(frameon=False)
    fig.tight_layout(); save(fig, os.path.join(fig_dir, name + "-rejections.svg"))


COLS = [("stage", "stage"), ("accounts", "accounts"), ("submitted", "submitted"), ("accepted", "accepted"), ("accepted_pct", "acc %"),
        ("included_prom_window", "included*"), ("rate_limited_429", "429"), ("rejected", "rejected"),
        ("mempool_peak_prom", "pool pk prom"), ("mempool_peak_txload", "pool pk txload"), ("block_time_peak_s", "bt peak s"),
        ("block_time_recovery_s", "bt recov s"), ("txs_per_block_peak", "txs/blk pk"), ("exec_ms_per_tx_blocks_ge_1000", "ms/tx"), ("drain_s", "drain s"),
        ("cpu_pct_peak_prom", "cpu pk %"), ("node_cpu_pct_peak_docker", "node cpu pk %"), ("mem_used_pct_peak", "mem pk %"),
        ("rtt_ms_p50", "rtt p50 ms"), ("rtt_ms_p95", "rtt p95 ms"), ("incl_s_p95", "incl p95 s"),
        ("restarts", "restarts"), ("oom", "oom"), ("valset_end", "valset"), ("verdict", "verdict")]


def fmt(v):
    if v is None:
        return "-"
    if isinstance(v, float):
        return "%.2f" % v if abs(v) < 100 else "%.0f" % v
    return str(v)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--work", default=os.environ.get("DS_WORK", os.path.expanduser("~/.sqc-devnet-stress")))
    ap.add_argument("runs", nargs="*")
    args = ap.parse_args()
    work = args.work
    runs = args.runs or sorted(os.path.basename(os.path.dirname(p)) for p in glob.glob(os.path.join(work, "runs", "*", "metrics.json")))
    if not runs:
        sys.exit("no runs with metrics.json under %s/runs" % work)
    baseline = None
    pf = sorted(glob.glob(os.path.join(work, "preflight-*.json")))
    if pf:
        baseline = json.load(open(pf[-1]))
    rows = []
    for run in runs:
        r, m, passes, watch, rule = load_run(work, run)
        a = analyse(r, m, passes, watch, rule)
        figures(work, r, m, passes, watch, a)
        a["rejected_by"] = a["rejected_by"] or {}
        rows.append(a)
        print("%-30s %-9s accounts=%d submitted=%d accepted=%d 429=%d rejected=%d pool_pk=%s bt_pk=%s cpu_pk=%s restarts=%d %s" % (
            run, a["verdict"], a["accounts"], a["submitted"], a["accepted"], a["rate_limited_429"], a["rejected"],
            fmt(a["mempool_peak_prom"]), fmt(a["block_time_peak_s"]), fmt(a["cpu_pct_peak_prom"]), a["restarts"], "; ".join(a["evidence"])))
    os.makedirs(os.path.join(work, "report"), exist_ok=True)
    out = {"generated_utc": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "baseline": baseline, "stages": rows,
           "notes": ["sqc_txs_failed_total and sqc_rpc_requests_total are dead counters (never move); not used.",
                     "included* = increase of sqc_txs_processed_total over [burst_start, drain_end] (includes the ~0.8 tx/s heartbeat).",
                     "rtt p50 = median of per-container p50; rtt p95 / incl p95 = max over containers (conservative).",
                     "pool pk txload = max mempool_size any driver saw in /status every 5 s; prom = 30 s scrape.",
                     "drain s = seconds after the last driver exited until the mempool had been <= 2 for 3 min, minus those 3 min."]}
    json.dump(out, open(os.path.join(work, "report", "summary.json"), "w"), indent=2)
    with open(os.path.join(work, "report", "summary.md"), "w") as f:
        f.write("# Devnet stress test — per-stage summary\n\n")
        if baseline:
            f.write("Baseline: %s, block interval %.3f s (Loki p50 %.3f / p95 %.3f s), background %.2f tx/s, CPU credits %s, node %s.\n\n" % (
                baseline["instance_type"], baseline["block_interval_s_prom"] or 0, baseline["block_time_loki_p50"] or 0, baseline["block_time_loki_p95"] or 0,
                baseline["background_tx_per_s"] or 0, baseline["cpu_credit_balance"], baseline["status"]["image_tag"]))
        f.write("| " + " | ".join(h for _, h in COLS) + " |\n|" + "---|" * len(COLS) + "\n")
        for a in rows:
            f.write("| " + " | ".join(fmt(a[k]) for k, _ in COLS) + " |\n")
        f.write("\n## Rejections by class\n\n")
        for a in rows:
            f.write("- %s: %s; fund pass: %s\n" % (a["stage"], json.dumps(a["rejected_by"]) or "{}", json.dumps(a["fund"]["rejected_by"]) if a["fund"] else "-"))
        f.write("\n## Verdict evidence\n\n")
        for a in rows:
            f.write("- %s (%s): %s\n" % (a["stage"], a["verdict"], "; ".join(a["evidence"])))
        f.write("\n## Notes\n\n" + "".join("- %s\n" % n for n in out["notes"]))
        f.write("\nFigures: figures/<run>-{throughput,mempool,blocks,host,latency,rejections}.svg\n")
    print("wrote %s/report/summary.{json,md} and %s/figures/*.svg" % (work, work))


if __name__ == "__main__":
    main()
