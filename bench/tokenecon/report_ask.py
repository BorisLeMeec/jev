#!/usr/bin/env python3
"""Paired agent runs for `jev ask`: same audit, grep-only vs ask-first."""
import json, os, re, sys, statistics
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from agentstats import stats

T = os.environ["JEV_BENCH_TASKS"]  # directory of agent transcripts (.output JSONL)
runs = json.load(open(os.path.join(os.path.dirname(__file__), "runs_ask.json")))

def answer(agent_id):
    found = None
    for line in open(f"{T}/{agent_id}.output", errors="replace"):
        if "YES:" not in line:
            continue
        try:
            o = json.loads(line)
        except Exception:
            continue
        for b in ((o.get("message") or {}).get("content") or []):
            if isinstance(b, dict):
                txt = str(b.get("text") or (b.get("input") or {}).get("report") or "")
                m = re.search(r"YES:\s*(.+)", txt)
                if m:
                    found = m.group(1).split("\n")[0].strip()
    if not found or found.lower().startswith("none"):
        return set()
    return {p.strip() for p in found.split(",") if p.strip()}

def prf(pred, truth):
    tp, fp, fn = len(pred & truth), len(pred - truth), len(truth - pred)
    p = tp / (tp + fp) if tp + fp else 0.0
    r = tp / (tp + fn) if tp + fn else 1.0
    return p, r, (2 * p * r / (p + r) if p + r else 0.0), tp, fp, fn

rows = []
print(f"{'task':<28}{'arm':<6}{'billed':>10}{'ctx':>8}{'turns':>7}{'calls':>7}{'P':>6}{'R':>6}{'F1':>6}")
print("-" * 84)
for t in runs["tasks"]:
    truth = set(t["truth"])
    for arm in ("grep", "jev"):
        st = stats(f"{T}/{t[arm]}.output")
        p, r, f, tp, fp, fn = prf(answer(t[arm]), truth)
        rows.append(dict(task=t["task"], arm=arm, billed=st["billed_in"], ctx=st["in_context"],
                         turns=st["turns"], calls=st["tool_calls"], P=p, R=r, F1=f))
        print(f"{t['task']:<28}{arm:<6}{st['billed_in']:>10,}{st['in_context']:>8,}"
              f"{st['turns']:>7}{st['tool_calls']:>7}{p:>6.2f}{r:>6.2f}{f:>6.2f}")
    print()

def agg(arm):
    sel = [r for r in rows if r["arm"] == arm]
    return {k: statistics.mean(r[k] for r in sel) for k in ("billed", "ctx", "turns", "calls", "P", "R", "F1")}

g, j = agg("grep"), agg("jev")
print("=" * 84)
print(f"{'arm':<8}{'billed':>12}{'ctx':>10}{'turns':>8}{'calls':>8}{'P':>7}{'R':>7}{'F1':>7}")
for n, a in (("grep", g), ("jev", j)):
    print(f"{n:<8}{a['billed']:>12,.0f}{a['ctx']:>10,.0f}{a['turns']:>8.1f}{a['calls']:>8.1f}"
          f"{a['P']:>7.2f}{a['R']:>7.2f}{a['F1']:>7.2f}")
print()
for k, name in (("billed", "billed input tokens"), ("ctx", "tokens into context"),
                ("turns", "turns"), ("calls", "tool calls")):
    print(f"  {name:<22}{g[k]:>10,.0f} -> {j[k]:>9,.0f}   {100*(j[k]-g[k])/g[k]:+6.1f}%")
ratios = []
by = {}
for r in rows: by.setdefault(r["task"], {})[r["arm"]] = r
for t, v in by.items():
    ratios.append(v["jev"]["billed"] / v["grep"]["billed"])
print(f"\n  median paired ratio {statistics.median(ratios):.2f}   "
      f"cheaper on {sum(1 for x in ratios if x < 1)}/{len(ratios)}")
json.dump(rows, open(os.path.join(os.path.dirname(__file__), "results_ask.json"), "w"), indent=1)
