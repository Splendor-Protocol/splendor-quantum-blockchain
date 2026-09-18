#!/usr/bin/env python3
"""Black-and-white chart helpers for the v1.1.x validation report.

Library use
-----------
    series_chart(data, out, title, ylabel, xlabel=None, size="full")
        data : path to .json/.csv, a list of [ts, value], or {name: [[ts, value], ...]}
               ts may be epoch seconds, elapsed seconds, or ISO-8601 strings.
    bar_chart(categories, values, out, title, ylabel, size="full")
        values : list of numbers, or {name: [numbers]} for grouped bars.

Both write SVG (vector, text outlined so the result is font-independent) or PNG
at 300 dpi when `out` ends in .png. Black lines, grey fills, no colour.

Command line
------------
    charts.py build                    every chart report.html references
                                       (data/<name>.json when present, else a
                                       synthetic placeholder watermarked as such)
    charts.py series IN OUT --title T --ylabel Y [--xlabel X] [--size full|half]
    charts.py bar    IN OUT --title T --ylabel Y [--size full|half]
                                       IN = {"categories": [...], "values": [...] | {name: [...]}}

Data files for `build` (drop real exports here, re-run build.sh):
    data/tps.json         {"submitted": [[ts, v], ...], "confirmed": [[ts, v], ...]}
    data/height.json      [[ts, height], ...]
    data/mempool.json     [[ts, pending], ...]
    data/cpu.json         [[ts, percent], ...]
    data/run-matrix.json  {"categories": ["10k","20k","30k","40k"],
                           "values": {"peak tx/s": [...], "sustained tx/s": [...]}}
"""
from __future__ import annotations

import argparse
import csv
import json
import logging
import os
import sys
from datetime import datetime
from itertools import cycle
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402

logging.getLogger("matplotlib.font_manager").setLevel(logging.ERROR)

ROOT = Path(__file__).resolve().parent
DATA = ROOT / "data"
CHARTS = ROOT / "charts"

INK = "#000000"
GREYS = ["#000000", "#707070", "#b5b5b5", "#3a3a3a"]
LINESTYLES = ["-", "--", ":", "-."]
FILLS = ["#151515", "#7a7a7a", "#c9c9c9", "#ffffff"]
AREA = "#ebebeb"
GRID = "#d6d6d6"

FIGSIZE = {"full": (6.85, 2.55), "half": (3.3, 2.45)}

plt.rcParams.update({
    "font.family": "sans-serif",
    "font.sans-serif": ["Helvetica Neue", "Helvetica", "Arial", "DejaVu Sans"],
    "font.size": 8,
    "axes.titlesize": 9,
    "axes.titleweight": "medium",
    "axes.titlelocation": "left",
    "axes.labelsize": 7.5,
    "axes.labelcolor": "#333333",
    "axes.edgecolor": INK,
    "axes.linewidth": 0.6,
    "axes.spines.top": False,
    "axes.spines.right": False,
    "axes.grid": True,
    "axes.grid.axis": "y",
    "grid.color": GRID,
    "grid.linewidth": 0.4,
    "xtick.labelsize": 7,
    "ytick.labelsize": 7,
    "xtick.color": INK,
    "ytick.color": INK,
    "xtick.major.width": 0.5,
    "ytick.major.width": 0.5,
    "xtick.major.size": 2.5,
    "ytick.major.size": 2.5,
    "legend.frameon": False,
    "legend.fontsize": 7,
    "svg.fonttype": "path",      # outline text: identical rendering inside <img>
    "svg.hashsalt": "splendor",  # reproducible ids
    "figure.dpi": 100,
    "savefig.dpi": 300,
})


# --------------------------------------------------------------------------- data

def _parse_ts(t):
    if isinstance(t, (int, float)):
        return float(t)
    s = str(t).strip()
    try:
        return float(s)
    except ValueError:
        pass
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    return datetime.fromisoformat(s).timestamp()


def _points(rows):
    xs = np.array([_parse_ts(r[0]) for r in rows], dtype=float)
    ys = np.array([float(r[1]) for r in rows], dtype=float)
    return xs, ys


def load_series(path):
    """Return {name: [[ts, value], ...]} from a .json or .csv file."""
    p = Path(path)
    if p.suffix.lower() == ".csv":
        rows = []
        with p.open(newline="") as fh:
            for row in csv.reader(fh):
                if not row or row[0].lower() in ("ts", "time", "timestamp", "t"):
                    continue
                rows.append([row[0], row[1]])
        return {"": rows}
    with p.open() as fh:
        obj = json.load(fh)
    if isinstance(obj, dict) and "series" in obj:
        obj = obj["series"]
    if isinstance(obj, list):
        return {"": obj}
    return dict(obj)


def _as_series(data):
    if isinstance(data, (str, os.PathLike)):
        data = load_series(data)
    if isinstance(data, list):
        data = {"": data}
    out = {}
    t0 = None
    for name, rows in data.items():
        xs, ys = _points(rows)
        out[name] = (xs, ys)
        t0 = xs.min() if t0 is None else min(t0, xs.min())
    span = max(xs.max() for xs, _ in out.values()) - t0
    unit, div = ("elapsed (s)", 1.0) if span <= 900 else ("elapsed (min)", 60.0)
    return {n: ((xs - t0) / div, ys) for n, (xs, ys) in out.items()}, unit


# --------------------------------------------------------------------------- charts

def _finish(fig, ax, out, title, ylabel, xlabel, placeholder):
    ax.set_title(title, pad=8)
    ax.set_ylabel(ylabel)
    if xlabel:
        ax.set_xlabel(xlabel)
    ax.margins(x=0.01)
    ax.set_axisbelow(True)
    if placeholder:
        fig.text(0.995, 0.975, "synthetic placeholder data", ha="right", va="top",
                 fontsize=6.2, color="#8a8a8a")
    fig.tight_layout(pad=0.45)
    out = Path(out)
    out.parent.mkdir(parents=True, exist_ok=True)
    kw = {"metadata": {"Date": None}} if out.suffix.lower() == ".svg" else {}
    fig.savefig(out, **kw)
    plt.close(fig)
    return out


def series_chart(data, out, title, ylabel, xlabel=None, size="full", placeholder=False,
                 zero_floor=True):
    """Time-series line chart. One series: black line over a light grey area.
    Several: black / grey / light grey lines with distinct dashes."""
    series, unit = _as_series(data)
    fig, ax = plt.subplots(figsize=FIGSIZE[size])
    single = len(series) == 1
    for (name, (xs, ys)), colour, ls in zip(series.items(), cycle(GREYS), cycle(LINESTYLES)):
        if single:
            ax.fill_between(xs, ys, 0, color=AREA, linewidth=0)
            ax.plot(xs, ys, color=INK, linewidth=1.0)
        else:
            ax.plot(xs, ys, color=colour, linestyle=ls, linewidth=1.0, label=name)
    if zero_floor:
        ax.set_ylim(bottom=0)
    if not single:
        ax.legend(loc="upper left", ncol=min(len(series), 4), handlelength=2.4,
                  borderaxespad=0.2)
    return _finish(fig, ax, out, title, ylabel, xlabel or unit, placeholder)


def bar_chart(categories, values, out, title, ylabel, size="full", placeholder=False,
              value_labels=True, fmt="{:,.0f}"):
    """Grouped bars: dark grey, mid grey, light grey, white — all black-edged."""
    groups = values if isinstance(values, dict) else {"": list(values)}
    n = len(groups)
    x = np.arange(len(categories))
    width = 0.72 / n
    fig, ax = plt.subplots(figsize=FIGSIZE[size])
    for i, (name, vals) in enumerate(groups.items()):
        vals = [np.nan if v is None else float(v) for v in vals]
        pos = x + (i - (n - 1) / 2) * width
        bars = ax.bar(pos, vals, width * 0.9, color=FILLS[i % len(FILLS)], edgecolor=INK,
                      linewidth=0.5, label=name or None)
        if value_labels:
            for b, v in zip(bars, vals):
                if np.isnan(v):
                    ax.text(b.get_x() + b.get_width() / 2, 0, "TBD", ha="center", va="bottom",
                            fontsize=6.5, color="#8a8a8a", fontfamily="monospace")
                else:
                    ax.text(b.get_x() + b.get_width() / 2, v, fmt.format(v), ha="center",
                            va="bottom", fontsize=6.5, color=INK)
    ax.set_xticks(x, list(categories))
    ax.tick_params(axis="x", length=0)
    ax.set_ylim(bottom=0)
    ax.margins(y=0.14)
    if n > 1:
        ax.legend(loc="upper left", ncol=n, borderaxespad=0.2)
    return _finish(fig, ax, out, title, ylabel, None, placeholder)


# --------------------------------------------------------------------------- placeholders

def _synthetic():
    """Deterministic stand-in for a 4-stage escalating run (10k/20k/30k/40k accounts)."""
    rng = np.random.default_rng(2026_09_18)
    stages = [(10_000, 420), (20_000, 780), (30_000, 1_050), (40_000, 1_180)]
    stage_len = 600                      # 10 min per stage
    ts, submitted, confirmed, mempool, cpu, height = [], [], [], [], [], []
    h, pending = 107_055, 0
    t = 0
    for _, target in stages:
        for _ in range(0, stage_len, 5):
            phase = (t % stage_len) / 40.0
            sub = max(0.0, target * (1 - np.exp(-phase)) + rng.normal(0, target * 0.05))
            con = min(sub, 1_000 + rng.normal(0, 25)) if target > 1_000 else sub - abs(rng.normal(0, target * 0.02))
            pending = max(0, pending + (sub - con) * 5 - rng.normal(0, 40))
            ts.append(t)
            submitted.append(sub)
            confirmed.append(max(con, 0))
            mempool.append(pending)
            cpu.append(min(98, 12 + con / 14 + rng.normal(0, 2.5)))
            h += 5 / 2                   # 2 s blocks
            height.append(int(h))
            t += 5
    z = lambda ys: [[t_, float(y)] for t_, y in zip(ts, ys)]  # noqa: E731
    return {
        "tps": {"submitted": z(submitted), "confirmed": z(confirmed)},
        "height": z(height),
        "mempool": z(mempool),
        "cpu": z(cpu),
        "run-matrix": {
            "categories": ["10k", "20k", "30k", "40k"],
            "values": {"peak tx/s": [434, 812, 1_121, 1_244],
                       "sustained tx/s": [418, 776, 998, 1_004]},
        },
    }


SPECS = [
    # name, kind, title, ylabel, size
    ("tps", "series", "Transactions per second — submitted and confirmed", "tx/s", "full"),
    ("height", "series", "Block height during the run", "height", "full"),
    ("mempool", "series", "Mempool depth", "pending tx", "half"),
    ("cpu", "series", "Node CPU", "% of one core", "half"),
    ("run-matrix", "bar", "Run matrix — throughput by account count", "tx/s", "full"),
]


def build():
    CHARTS.mkdir(exist_ok=True)
    synth = _synthetic()
    for name, kind, title, ylabel, size in SPECS:
        src = DATA / f"{name}.json"
        out = CHARTS / f"{name}.svg"
        real = src.exists()
        if real:
            with src.open() as fh:
                payload = json.load(fh)
        else:
            payload = synth[name]
        if kind == "series":
            series_chart(payload, out, title, ylabel, size=size, placeholder=not real)
        else:
            bar_chart(payload["categories"], payload["values"], out, title, ylabel,
                      size=size, placeholder=not real)
        print(f"chart  {out.relative_to(ROOT)}  <- {'data/' + src.name if real else 'synthetic placeholder'}")


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("build", help="render every chart the report references")
    for kind in ("series", "bar"):
        p = sub.add_parser(kind)
        p.add_argument("input")
        p.add_argument("output")
        p.add_argument("--title", required=True)
        p.add_argument("--ylabel", required=True)
        p.add_argument("--xlabel")
        p.add_argument("--size", choices=("full", "half"), default="full")
    a = ap.parse_args(argv)
    if a.cmd == "build":
        build()
    elif a.cmd == "series":
        series_chart(a.input, a.output, a.title, a.ylabel, xlabel=a.xlabel, size=a.size)
        print(a.output)
    else:
        with open(a.input) as fh:
            payload = json.load(fh)
        bar_chart(payload["categories"], payload["values"], a.output, a.title, a.ylabel, size=a.size)
        print(a.output)


if __name__ == "__main__":
    sys.exit(main())
