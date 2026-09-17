#!/usr/bin/env python3
"""Evaluate `jev ask` as a per-file classifier.

`ask` is not retrieval: it answers one yes/no question about every file in a
scope. So the metrics are precision, recall and F1, not P@1 — and precision is
the one that decides whether an audit is usable. A checker that flags fifteen
innocent files to catch three real ones does not get used twice.

Reported per question:
  P/R/F1 @0.7   at the tool's default --yes threshold
  best F1       over every threshold, to show whether 0.7 is the right default
  AUC           probability a true file outranks a false one; threshold-free,
                so it separates "bad at judging" from "badly calibrated"
  grep          the regex a developer would plausibly have tried

Usage: bench_ask.py queries_ask/*.json
"""
import json, os, subprocess, sys, time
from collections import defaultdict

JEV = os.environ.get("JEV_BIN", "jev")
USAGE_LOG = os.path.expanduser("~/.jev/usage.jsonl")
THRESH = 0.7


def prf(pred, truth, scope):
    tp = len(pred & truth)
    fp = len(pred - truth)
    fn = len(truth - pred)
    p = tp / (tp + fp) if (tp + fp) else (1.0 if not truth else 0.0)
    r = tp / (tp + fn) if (tp + fn) else 1.0
    f = 2 * p * r / (p + r) if (p + r) else 0.0
    return p, r, f, tp, fp, fn


def auc(scores, truth):
    """Probability a positive scores above a negative; 0.5 = no signal."""
    pos = [s for f, s in scores.items() if f in truth]
    neg = [s for f, s in scores.items() if f not in truth]
    if not pos or not neg:
        return float("nan")
    wins = sum((a > b) + 0.5 * (a == b) for a in pos for b in neg)
    return wins / (len(pos) * len(neg))


def best_f1(scores, truth, scope):
    best = (0.0, 0.0)
    for t in [i / 20 for i in range(21)]:
        pred = {f for f, s in scores.items() if s >= t}
        _, _, f, _, _, _ = prf(pred, truth, scope)
        if f > best[0]:
            best = (f, t)
    return best


def run_jev(repo, question, scope):
    off = os.path.getsize(USAGE_LOG) if os.path.exists(USAGE_LOG) else 0
    t0 = time.time()
    r = subprocess.run([JEV, "ask", "--json", "--cite=false", question] + scope,
                       cwd=repo, capture_output=True, text=True)
    dt = time.time() - t0
    tok = 0
    if os.path.exists(USAGE_LOG):
        with open(USAGE_LOG) as fh:
            fh.seek(off)
            for line in fh:
                try:
                    tok += json.loads(line).get("in_tokens", 0)
                except json.JSONDecodeError:
                    pass
    try:
        rows = json.loads(r.stdout)
    except json.JSONDecodeError:
        sys.stderr.write(f"  jev ask failed: {r.stderr.strip()[:300]}\n")
        return {}, dt, tok
    return {x["path"]: x["score"] for x in rows}, dt, tok


def run_grep(repo, pattern, scope):
    if not pattern:
        return set()
    r = subprocess.run(["grep", "-liE", pattern] + scope, cwd=repo,
                       capture_output=True, text=True)
    return {l.strip() for l in r.stdout.splitlines() if l.strip()}


def main():
    agg = defaultdict(lambda: defaultdict(list))
    tot_tok = tot_time = 0
    for qf in sys.argv[1:]:
        spec = json.load(open(qf))
        repo = spec["repo"]
        print(f"\n### {os.path.basename(repo)}")
        for q in spec["questions"]:
            scope, truth = q["scope"], set(q["yes"])
            scores, dt, tok = run_jev(repo, q["question"], scope)
            tot_tok += tok
            tot_time += dt
            if not scores:
                continue
            gpred = run_grep(repo, q.get("grep_baseline", ""), scope)

            jp = {f for f, s in scores.items() if s >= THRESH}
            jP, jR, jF, tp, fp, fn = prf(jp, truth, scope)
            gP, gR, gF, gtp, gfp, gfn = prf(gpred, truth, scope)
            a = auc(scores, truth)
            bf, bt = best_f1(scores, truth, scope)

            for name, (P, R, F) in (("jev", (jP, jR, jF)), ("grep", (gP, gR, gF))):
                agg[name][q["kind"]].append((P, R, F))
                agg[name]["ALL"].append((P, R, F))
            agg["jev"]["_auc_" + q["kind"]].append((a, a, a))
            agg["jev"]["_auc_ALL"].append((a, a, a))

            print(f"  {q['id']:<28} {q['kind']:<10} {len(truth)}/{len(scope)} true")
            print(f"      jev @0.7   P={jP:.2f} R={jR:.2f} F1={jF:.2f}   "
                  f"(tp {tp}, fp {fp}, fn {fn})   AUC={a:.2f}   bestF1={bf:.2f}@{bt:.2f}")
            print(f"      grep       P={gP:.2f} R={gR:.2f} F1={gF:.2f}   "
                  f"(tp {gtp}, fp {gfp}, fn {gfn})")

    print("\n" + "=" * 74)
    print(f"{'method':<8}{'kind':<12}{'n':>3}{'P':>8}{'R':>8}{'F1':>8}")
    for name in ("jev", "grep"):
        for kind in ("syntactic", "semantic", "rare", "ALL"):
            v = agg[name].get(kind)
            if not v:
                continue
            n = len(v)
            print(f"{name:<8}{kind:<12}{n:>3}{sum(x[0] for x in v)/n:>8.2f}"
                  f"{sum(x[1] for x in v)/n:>8.2f}{sum(x[2] for x in v)/n:>8.2f}")
        print("-" * 74)
    for kind in ("syntactic", "semantic", "rare", "ALL"):
        v = agg["jev"].get("_auc_" + kind)
        if v:
            print(f"jev AUC {kind:<12}{sum(x[0] for x in v)/len(v):.2f}")
    print(f"\njev cost: {tot_tok:,} tokens  ${tot_tok*0.042/1e6:.4f}   {tot_time:.0f}s")


if __name__ == "__main__":
    main()
