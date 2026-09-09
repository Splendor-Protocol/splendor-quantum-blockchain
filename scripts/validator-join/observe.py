#!/usr/bin/env python3
"""Assert that a live multi-validator window really was multi-validator.

Reads a height range from every node URL given and checks the properties that
distinguish "several nodes are connected" from "several nodes are producing
under a quorum":

  * every node returns the SAME block hash, state root and producer for every
    height in the window (agreeing heads, not just similar heights);
  * at least --min-producers DISTINCT addresses sealed blocks in the window;
  * every block carries a qc_root and at least --min-votes distinct votes, and
    every vote is for that block's own hash and height;
  * heights are contiguous and each block links to its parent.

Usage:
  observe.py --from H --to H --min-producers N --min-votes N URL [URL...]

Exits non-zero with a diagnosis on the first property that fails.
"""

import argparse
import base64
import json
import sys
import urllib.error
import urllib.request


def get(url, timeout=10):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return json.load(r)


def h32(v):
    """Hex a Go byte field however encoding/json rendered it.

    A [32]byte array comes back as a list of ints; a []byte (qc_root,
    signatures) comes back as a base64 string. Both appear in one block.
    """
    if v is None:
        return None
    if isinstance(v, str):
        return base64.b64decode(v).hex()
    return bytes(v).hex()


def addr(v):
    return "SQ:" + bytes(v).hex()


def fetch_block(node, height):
    try:
        return get(f"{node}/block/{height}")
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError) as e:
        raise SystemExit(f"FAIL: {node}/block/{height}: {e}")


def summarise(b):
    hdr = b["header"]
    votes = b.get("votes") or []
    return {
        "height": hdr["height"],
        "hash": h32(b["hash"]),
        "prev": h32(hdr["prev_hash"]),
        "state_root": h32(hdr["state_root"]),
        "producer": addr(hdr["validator_addr"]),
        "qc_root": h32(hdr.get("qc_root")) if hdr.get("qc_root") else None,
        "votes": [
            {
                "by": addr(v["validator_addr"]),
                "height": v["height"],
                "block": h32(v["block_hash"]),
                "round": v.get("round", 0),
            }
            for v in votes
        ],
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("nodes", nargs="+")
    ap.add_argument("--from", dest="lo", type=int, required=True)
    ap.add_argument("--to", dest="hi", type=int, required=True)
    ap.add_argument("--min-producers", type=int, default=2)
    ap.add_argument("--min-votes", type=int, default=2)
    ap.add_argument("--json-out", default=None)
    args = ap.parse_args()

    failures = []
    rows = []
    producers = set()
    prev_hash = None

    for h in range(args.lo, args.hi + 1):
        views = {node: summarise(fetch_block(node, h)) for node in args.nodes}
        ref_node, ref = next(iter(views.items()))

        for node, v in views.items():
            for field in ("hash", "state_root", "producer", "qc_root"):
                if v[field] != ref[field]:
                    failures.append(
                        f"height {h}: {node} reports {field}={v[field]} but "
                        f"{ref_node} reports {ref[field]}"
                    )

        if prev_hash is not None and ref["prev"] != prev_hash:
            failures.append(
                f"height {h}: prev_hash {ref['prev']} does not link to {prev_hash}"
            )
        prev_hash = ref["hash"]

        producers.add(ref["producer"])

        if not ref["qc_root"]:
            failures.append(f"height {h}: no qc_root — block was not certified")
        voters = {v["by"] for v in ref["votes"]}
        if len(voters) < args.min_votes:
            failures.append(
                f"height {h}: {len(voters)} distinct votes, need {args.min_votes} "
                f"({sorted(voters)})"
            )
        for v in ref["votes"]:
            if v["block"] != ref["hash"]:
                failures.append(f"height {h}: vote from {v['by']} names block {v['block']}")
            if v["height"] != h:
                failures.append(f"height {h}: vote from {v['by']} is for height {v['height']}")

        rows.append(ref)
        print(
            f"  h={h:<5} producer={ref['producer'][:14]}… votes={len(voters)} "
            f"qc={'yes' if ref['qc_root'] else 'NO':<3} hash={ref['hash'][:12]}…"
        )

    if len(producers) < args.min_producers:
        failures.append(
            f"only {len(producers)} distinct producer(s) in heights "
            f"{args.lo}..{args.hi}: {sorted(p[:14] for p in producers)} — "
            f"a joined validator that never seals a block has not been activated"
        )

    if args.json_out:
        with open(args.json_out, "w") as f:
            json.dump({"blocks": rows, "producers": sorted(producers)}, f, indent=2)

    if failures:
        print("\nFAILURES:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        sys.exit(1)

    print(
        f"\nOK: heights {args.lo}..{args.hi} agree across {len(args.nodes)} node(s); "
        f"{len(producers)} distinct producers; every block certified with "
        f">= {args.min_votes} votes."
    )


if __name__ == "__main__":
    main()
