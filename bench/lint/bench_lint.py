#!/usr/bin/env python3
"""Precision and recall for the semantic lint, driven through the real binary.

Precision is what decides whether a linter survives. One that cries wolf on
three clean edits gets turned off on the fourth day, and a rule that only ever
fires correctly is worth more than one that catches everything.

Near-miss cases are reported separately: a change that touches the same area
without breaking the rule is where a keyword matcher fails and where this is
supposed to earn its place.

Usage: bench_lint.py diffs_*.json
"""
import json, os, re, subprocess, sys
from collections import defaultdict

JEV = os.environ.get("JEV_BIN", "jev")
# The repository whose .jev-rules.json the diffs are judged against.
REPO = os.environ.get("JEV_BENCH_REPO", os.getcwd())

# Must match defaultLintThreshold in internal/run/lint.go.
THRESH = 0.60


def score(case):
    """Every rule's raw probability for one change."""
    payload = {"tool_name": "Edit", "cwd": REPO,
               "tool_input": {"file_path": os.path.join(REPO, case["path"]),
                              "old_string": case.get("before", ""),
                              "new_string": case["after"]}}
    r = subprocess.run([JEV, "lint", "edit"], input=json.dumps(payload),
                       capture_output=True, text=True,
                       env=dict(os.environ, JEV_LINT_DEBUG="1"))
    out = {}
    for line in r.stderr.splitlines():
        if m := re.match(r"jev lint:\s+(\S+)\s+([0-9.]+)$", line):
            out[m.group(1)] = float(m.group(2))
    fired = "additionalContext" in (r.stdout or "")
    return out, fired


def prf(tp, fp, fn):
    p = tp / (tp + fp) if tp + fp else 1.0
    r = tp / (tp + fn) if tp + fn else 1.0
    return p, r, (2 * p * r / (p + r) if p + r else 0.0)


def main():
    cases = []
    for f in sys.argv[1:]:
        cases += json.load(open(f))

    rows = []
    print(f"{'id':<28}{'rule':<24}{'kind':<11}{'label':<10}{'score':>7}{'verdict':>9}")
    print("-" * 90)
    for c in sorted(cases, key=lambda x: (x["rule"], x["kind"], x["label"])):
        scores, _ = score(c)
        s = scores.get(c["rule"])
        if s is None:
            print(f"{c['id'][:27]:<28}{c['rule']:<24}{c['kind']:<11}{c['label']:<10}"
                  f"{'-':>7}{'rule not applied':>9}")
            continue
        pred = "violates" if s >= THRESH else "clean"
        ok = pred == c["label"]
        rows.append(dict(c, score=s, pred=pred, ok=ok))
        print(f"{c['id'][:27]:<28}{c['rule']:<24}{c['kind']:<11}{c['label']:<10}"
              f"{s:>7.2f}{(pred if ok else pred + ' X'):>9}")

    print("\n" + "=" * 90)

    def report(label, sel):
        if not sel:
            return
        tp = sum(1 for r in sel if r["label"] == "violates" and r["pred"] == "violates")
        fp = sum(1 for r in sel if r["label"] == "clean" and r["pred"] == "violates")
        fn = sum(1 for r in sel if r["label"] == "violates" and r["pred"] == "clean")
        p, rc, f = prf(tp, fp, fn)
        print(f"{label:<34}{len(sel):>4} cases   P={p:.2f} R={rc:.2f} F1={f:.2f}"
              f"   (tp {tp}, fp {fp}, fn {fn})")

    report("ALL", rows)
    print()
    for rule in sorted({r["rule"] for r in rows}):
        report(f"  rule: {rule}", [r for r in rows if r["rule"] == rule])
    print()
    for kind in ("obvious", "near-miss"):
        report(f"  {kind}", [r for r in rows if r["kind"] == kind])

    clean = [r for r in rows if r["label"] == "clean"]
    viol = [r for r in rows if r["label"] == "violates"]
    if clean and viol:
        print(f"\nscore separation: violations {min(r['score'] for r in viol):.2f}-"
              f"{max(r['score'] for r in viol):.2f}   "
              f"clean {min(r['score'] for r in clean):.2f}-{max(r['score'] for r in clean):.2f}")
        nm = [r for r in clean if r["kind"] == "near-miss"]
        if nm:
            print(f"near-misses (the ones designed to fool it): "
                  f"{max(r['score'] for r in nm):.2f} highest, "
                  f"{sum(1 for r in nm if r['pred'] == 'violates')} false alarms of {len(nm)}")

    print("\nthreshold sweep (all cases):")
    print(f"{'thresh':>7}{'P':>7}{'R':>7}{'F1':>7}{'false alarms':>14}")
    for t in (0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8):
        tp = sum(1 for r in rows if r["label"] == "violates" and r["score"] >= t)
        fp = sum(1 for r in rows if r["label"] == "clean" and r["score"] >= t)
        fn = sum(1 for r in rows if r["label"] == "violates" and r["score"] < t)
        p, rc, f = prf(tp, fp, fn)
        print(f"{t:>7.1f}{p:>7.2f}{rc:>7.2f}{f:>7.2f}{fp:>14}")

    json.dump(rows, open(os.path.join(os.path.dirname(__file__) or ".", "results.json"), "w"), indent=1)


if __name__ == "__main__":
    main()
