#!/usr/bin/env python3
"""Paired agent runs: same task, grep-only vs jev-first, measured from transcripts."""
import json, os, re, sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from agentstats import stats

T = os.environ["JEV_BENCH_TASKS"]  # directory of agent transcripts (.output JSONL)
runs = json.load(open(os.path.join(os.path.dirname(__file__), "runs.json")))

def answer(agent_id):
    """The FILES: line the agent handed back."""
    path = f"{T}/{agent_id}.output"
    found = None
    for line in open(path, errors="replace"):
        if "FILES:" not in line:
            continue
        try:
            o = json.loads(line)
        except Exception:
            continue
        for b in ((o.get("message") or {}).get("content") or []):
            if isinstance(b, dict):
                txt = b.get("text") or (b.get("input") or {}).get("report") or ""
                m = re.search(r"FILES:\s*(.+)", str(txt))
                if m:
                    found = m.group(1).split("\n")[0].strip()
    if not found:
        return []
    if found.lower().startswith("none"):
        return []
    return [p.strip() for p in found.split(",") if p.strip()]

def score(got, truth):
    if not truth:                      # negative control: silence is the win
        return ("correct" if not got else f"{len(got)} false"), (1.0 if not got else 0.0)
    hit = len(set(got) & set(truth))
    return f"{hit}/{len(truth)}", hit / len(truth)

rows = []
print(f"{'task':<34}{'arm':<6}{'billed':>10}{'ctx':>8}{'turns':>7}{'calls':>7}{'recall':>9}")
print("-" * 82)
for t in runs["tasks"]:
    for arm in ("grep", "jev"):
        st = stats(f"{T}/{t[arm]}.output")
        got = answer(t[arm])
        label, rec = score(got, t["truth"])
        rows.append(dict(task=t["task"], kind=t["kind"], arm=arm, rec=rec,
                         billed=st["billed_in"], ctx=st["in_context"],
                         turns=st["turns"], calls=st["tool_calls"], out=st["output"]))
        print(f"{t['task']:<34}{arm:<6}{st['billed_in']:>10,}{st['in_context']:>8,}"
              f"{st['turns']:>7}{st['tool_calls']:>7}{label:>9}")
    print()

def agg(arm, kind=None):
    sel = [r for r in rows if r["arm"] == arm and (kind is None or r["kind"] == kind)]
    n = len(sel)
    return {k: sum(r[k] for r in sel) / n for k in ("billed", "ctx", "turns", "calls", "rec")} | {"n": n}

print("=" * 82)
print(f"{'arm':<8}{'n':>3}{'billed avg':>13}{'ctx avg':>10}{'turns':>8}{'calls':>8}{'recall':>9}")
for arm in ("grep", "jev"):
    a = agg(arm)
    print(f"{arm:<8}{a['n']:>3}{a['billed']:>13,.0f}{a['ctx']:>10,.0f}{a['turns']:>8.1f}{a['calls']:>8.1f}{a['rec']:>9.2f}")

g, j = agg("grep"), agg("jev")
print(f"\njev vs grep-only:")
for k, name in (("billed", "billed input tokens"), ("ctx", "tokens into context"),
                ("turns", "turns"), ("calls", "tool calls")):
    d = 100 * (j[k] - g[k]) / g[k]
    print(f"  {name:<22}{g[k]:>10,.0f} -> {j[k]:>9,.0f}   {d:+6.1f}%")
print(f"  recall                {g['rec']:>10.2f} -> {j['rec']:>9.2f}")

print("\nby task kind (billed input tokens):")
for kind in ("vocab_gap", "ordinary", "negative"):
    gg, jj = agg("grep", kind), agg("jev", kind)
    if gg["n"]:
        d = 100 * (jj["billed"] - gg["billed"]) / gg["billed"]
        print(f"  {kind:<12} n={gg['n']}  grep {gg['billed']:>9,.0f}  jev {jj['billed']:>9,.0f}  {d:+6.1f}%"
              f"   recall {gg['rec']:.2f} -> {jj['rec']:.2f}")

json.dump(rows, open(os.path.join(os.path.dirname(__file__), "results.json"), "w"), indent=1)

# runs.json / runs_ask.json map each task to the two agent transcripts that ran
# it. They are not published: they name private repositories and reference
# transcripts that exist only on the machine that produced them. The shape is
# {"tasks": [{"task", "truth": [...], "grep": "<agent-id>", "jev": "<agent-id>"}]}.
