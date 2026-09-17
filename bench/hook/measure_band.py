#!/usr/bin/env python3
"""Recall in the 400-1200 line band, against the coverage a random window gets.

On a small file the shipped window is most of the file, so a high recall proves
little on its own: a window covering 73% of a file would catch a randomly placed
target 73% of the time. Every rule below is therefore reported next to its own
coverage, and the lift between them is the only thing that shows the tool is
doing work. Several window rules are scored from one API call each, so the cost
of comparing them is one request per target.
"""
import json, os, statistics, sys, urllib.request
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor

KEY = os.environ["TYPE_SAFE_AI_KEY"]
LOC = ("Which numbered chunk of `file.chunks` contains the part of this file that most directly "
       "implements what `goal` describes? Each chunk is a run of consecutive lines from the file "
       "at `file.path`.")

# name -> minimum window; the window is max(minimum, lines/5)
RULES = {"shipped (min 300)": 300, "min 200": 200, "min 150": 150, "min 100": 100, "min 60": 60}


def locate(c):
    lines = open(os.path.join(c["repo"], c["file"]), errors="replace").read().split("\n")
    n = len(lines)
    size = max(10, (n + 199) // 200)
    chunks, crit = {}, {}
    for s in range(0, n, size):
        e = min(s + size, n)
        i = s // size
        chunks[f"c{i}"] = {"start_line": s + 1, "text": "\n".join(lines[s:e])}
        crit[f"c{i}"] = f"lines {s+1}-{e}"
    body = {"model": "jev-latest",
            "state": {"goal": c["goal"], "file": {"path": c["file"], "chunks": chunks}},
            "questions": {"where": {"type": "choice", "instructions": LOC, "criteria": crit}}}
    req = urllib.request.Request("https://api.typesafe.ai/v1/systemone",
                                 data=json.dumps(body).encode(),
                                 headers={"Authorization": f"Bearer {KEY}",
                                          "Content-Type": "application/json"})
    a = json.load(urllib.request.urlopen(req))["answers"]["where"]
    return dict(c, real_lines=n, chunk=size,
                pick=chunks[a["choice"]]["start_line"], conf=a["confidence"])


def window(n, start, minimum):
    win = max(minimum, n // 5)
    if win >= n:
        return None
    off = max(1, min(start - win // 2, n - win))
    return off, win


def main():
    cases = []
    for f in sys.argv[1:]:
        cases += json.load(open(f))
    with ThreadPoolExecutor(5) as ex:
        res = list(ex.map(locate, cases))

    print(f"{'file':<48}{'lines':>6}{'target':>7}{'picked':>7}{'off by':>8}{'conf':>6}")
    print("-" * 84)
    for r in sorted(res, key=lambda x: (x["file"], x["line"])):
        print(f"{r['file'][-47:]:<48}{r['real_lines']:>6}{r['line']:>7}{r['pick']:>7}"
              f"{abs(r['pick']-r['line']):>8}{r['conf']:>6.2f}")

    print("\n" + "=" * 84)
    print(f"{'window rule':<20}{'recall':>9}{'coverage':>10}{'lift':>8}{'kept':>8}{'narrowed':>10}")
    print("-" * 84)
    for name, minimum in RULES.items():
        hit = tot = 0
        cov, kept = [], []
        for r in res:
            w = window(r["real_lines"], r["pick"], minimum)
            if w is None:
                continue
            off, win = w
            tot += 1
            hit += off <= r["line"] < off + win
            cov.append(win / r["real_lines"])       # chance of catching a random target
            kept.append(win / r["real_lines"])
        if tot:
            rec = hit / tot
            c = statistics.mean(cov)
            print(f"{name:<20}{f'{hit}/{tot}':>9}{c:>9.0%}{rec-c:>+8.0%}"
                  f"{statistics.mean(kept):>8.0%}{tot:>10}")

    print("\nper file size, at the shipped rule:")
    buckets = defaultdict(list)
    for r in res:
        b = "400-600" if r["real_lines"] < 600 else ("600-900" if r["real_lines"] < 900 else "900+")
        w = window(r["real_lines"], r["pick"], 300)
        if w:
            buckets[b].append(w[0] <= r["line"] < w[0] + w[1])
    for b in ("400-600", "600-900", "900+"):
        if buckets[b]:
            print(f"  {b:<10}{sum(buckets[b])}/{len(buckets[b])}")

    # Distance from the chosen line to the target is size-independent, so it
    # shows whether the model is actually locating or merely landing inside a
    # window that happens to be most of the file.
    d = [abs(r["pick"] - r["line"]) for r in res]
    print(f"\ndistance from picked line to target: median {statistics.median(d):.0f} lines, "
          f"within 25 lines {sum(1 for x in d if x <= 25)}/{len(d)}, "
          f"within 60 {sum(1 for x in d if x <= 60)}/{len(d)}")

    json.dump(res, open(os.path.join(os.path.dirname(__file__) or ".", "results_band.json"), "w"), indent=1)


if __name__ == "__main__":
    main()
