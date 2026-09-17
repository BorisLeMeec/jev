#!/usr/bin/env python3
"""Wall-clock of the paired agent runs, read from the transcripts they left.

Every pair already ran; this only reads the timestamps, so it costs nothing and
measures the same runs the token numbers came from. The caveat is that the
arms ran concurrently, so absolute seconds include whatever contention there
was — the ratio between two arms of the same pair is the trustworthy part.
"""
import json, os, statistics, sys
from datetime import datetime

T = os.environ["JEV_BENCH_TASKS"]  # directory of agent transcripts (.output JSONL)


def seconds(agent_id):
    first = last = None
    try:
        for line in open(f"{T}/{agent_id}.output", errors="replace"):
            try:
                o = json.loads(line)
            except Exception:
                continue
            ts = o.get("timestamp")
            if not ts:
                continue
            t = datetime.fromisoformat(ts.replace("Z", "+00:00"))
            first = first or t
            last = t
    except OSError:
        return None
    return (last - first).total_seconds() if first and last else None


SETS = [
    ("find   (grep-only vs jev-first)", "tokenecon/runs.json", "grep", "jev"),
    ("ask    (grep-only vs ask-first)", "tokenecon/runs_ask.json", "grep", "jev"),
    ("Read   (whole file vs window)", "tokenecon/runs_hook.json", "full", "windowed"),
]

for label, path, arm_a, arm_b in SETS:
    spec = json.load(open(os.path.join(os.path.dirname(__file__), path)))
    rows = []
    for t in spec["tasks"]:
        a, b = seconds(t[arm_a]), seconds(t[arm_b])
        if a and b:
            rows.append((t["task"], a, b))
    if not rows:
        continue
    print(f"\n{label}")
    print(f"  {'task':<34}{arm_a:>10}{arm_b:>11}{'ratio':>8}")
    for name, a, b in rows:
        print(f"  {name[:33]:<34}{a:>9.0f}s{b:>10.0f}s{b/a:>8.2f}")
    ma, mb = statistics.mean(r[1] for r in rows), statistics.mean(r[2] for r in rows)
    print(f"  {'mean':<34}{ma:>9.0f}s{mb:>10.0f}s{mb/ma:>8.2f}"
          f"   {100*(mb-ma)/ma:+.0f}%")
