#!/usr/bin/env python3
"""Window recall for the Read hook, driven through the real binary.

Recall is the only metric that matters. A window that drops the answer is a
silent omission — the agent reads what it was handed and never learns the part
it wanted was cut. Compression is worthless without recall.

Usage: measure.py cases_*.json
"""
import json, os, re, statistics, subprocess, sys, tempfile
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor

JEV = os.environ.get("JEV_BIN", "jev")


def run(c):
    fd, tp = tempfile.mkstemp(suffix=".jsonl")
    with os.fdopen(fd, "w") as fh:
        fh.write(json.dumps({"type": "user",
                             "message": {"role": "user", "content": c["goal"]}}) + "\n")
    payload = {"tool_name": "Read",
               "tool_input": {"file_path": os.path.join(c["repo"], c["file"])},
               "cwd": c["repo"], "transcript_path": tp}
    # No JEV_HOOK_MIN_LINES: measure the floor the binary actually ships with.
    env = dict(os.environ, JEV_HOOK_ENABLE="1", JEV_HOOK_DEBUG="1")
    if "min_lines" in c:
        env["JEV_HOOK_MIN_LINES"] = str(c["min_lines"])
    r = subprocess.run([JEV, "hook", "read"], input=json.dumps(payload),
                       capture_output=True, text=True, env=env)
    os.unlink(tp)
    try:
        out = json.loads(r.stdout or "{}")
    except json.JSONDecodeError:
        out = {}
    ui = (out.get("hookSpecificOutput") or {}).get("updatedInput")
    ctx = out.get("additionalContext", "")
    conf = float(m.group(1)) if (m := re.search(r"confidence ([0-9.]+)", ctx)) else None
    pick = int(m.group(1)) if (m := re.search(r"match at line (\d+)", ctx)) else None
    n = c["lines"]
    if not ui:
        return dict(c, narrowed=False, why=r.stderr.strip(), hit=None,
                    quintile=min(4, (c["line"] - 1) * 5 // n))
    off, lim = ui["offset"], ui["limit"]
    return dict(c, narrowed=True, off=off, win=lim, conf=conf, pick=pick,
                hit=off <= c["line"] < off + lim, kept=lim / n,
                quintile=min(4, (c["line"] - 1) * 5 // n))


def main():
    cases = []
    for f in sys.argv[1:]:
        cases += json.load(open(f))
    with ThreadPoolExecutor(5) as ex:
        res = list(ex.map(run, cases))

    by_file = defaultdict(list)
    for r in res:
        by_file[r["file"]].append(r)

    print(f"{'file':<44}{'lines':>6}{'target':>7}{'picked':>7}{'conf':>6}{'window':>14}{'in?':>6}")
    print("-" * 92)
    for f in sorted(by_file):
        for r in sorted(by_file[f], key=lambda x: x["line"]):
            if not r["narrowed"]:
                print(f"{f[-43:]:<44}{r['lines']:>6}{r['line']:>7}{'-':>7}{'-':>6}"
                      f"{'passed through':>14}{'n/a':>6}")
                continue
            w = "%d-%d" % (r["off"], r["off"] + r["win"] - 1)
            print(f"{f[-43:]:<44}{r['lines']:>6}{r['line']:>7}{r['pick'] or 0:>7}"
                  f"{r['conf'] or 0:>6.2f}{w:>14}{'YES' if r['hit'] else 'LOST':>6}")

    nar = [r for r in res if r["narrowed"]]
    hit = sum(r["hit"] for r in nar)
    print("\n" + "=" * 92)
    print(f"cases              {len(res)}")
    print(f"narrowed           {len(nar)}   passed through {len(res)-len(nar)}")
    if nar:
        print(f"window recall      {hit}/{len(nar)} = {hit/len(nar):.2f}")
        print(f"mean file kept     {statistics.mean(r['kept'] for r in nar):.0%}")

        print("\nrecall by where the target sits in the file:")
        for q in range(5):
            sel = [r for r in nar if r["quintile"] == q]
            if sel:
                print(f"  {q*20:>3}-{q*20+20:<3}%  {sum(r['hit'] for r in sel)}/{len(sel)}")

        h = [r["conf"] for r in nar if r["hit"] and r["conf"]]
        m = [r["conf"] for r in nar if not r["hit"] and r["conf"]]
        print("\nconfidence of the windows that were narrowed:")
        if h:
            print(f"  hits   {min(h):.2f}-{max(h):.2f}  (n={len(h)})")
        if m:
            print(f"  misses {min(m):.2f}-{max(m):.2f}  (n={len(m)})   "
                  f"separable: {'no (overlap)' if h and min(h) <= max(m) else 'yes'}")
        else:
            print("  misses none observed")

    why = defaultdict(int)
    for r in res:
        if not r["narrowed"]:
            why[re.sub(r"[0-9.]+", "N", r.get("why", "")).strip()] += 1
    if why:
        print("\nwhy reads passed through:")
        for k, v in sorted(why.items(), key=lambda kv: -kv[1]):
            print(f"  {v:>3}x  {k[:80]}")

    json.dump(res, open(os.path.join(os.path.dirname(__file__) or ".", "results_big.json"), "w"), indent=1)


if __name__ == "__main__":
    main()
