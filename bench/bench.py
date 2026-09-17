#!/usr/bin/env python3
"""Compare jev against lexical baselines on labelled retrieval queries.

Every method ranks the same corpus — the file set `jev scan --list` reports — so
differences come from ranking, not from what each tool chose to look at.

Baselines:
  grep  what an agent reaches for first: case-insensitive alternation over the
        query's content words, files ranked by number of matching lines.
  bm25  a real lexical IR ranker, the standard retrieval baseline. Identifiers
        are split on camelCase and underscores so `apiSignup` matches "signup".

Usage: bench.py queries/*.json [--methods grep,bm25,jev] [--k 5]
"""
import functools, json, math, os, re, subprocess, sys, time
from collections import Counter, defaultdict

# Progress must be visible while a run takes minutes; stdout is block-buffered
# when redirected to a file.
print = functools.partial(__builtins__.print, flush=True)

JEV = os.environ.get("JEV_BIN", "jev")
USAGE_LOG = os.path.expanduser("~/.jev/usage.jsonl")

STOP = {"the","a","an","is","are","where","how","what","which","does","do","in","of","to","and","or",
        "for","on","at","by","with","this","that","it","its","from","when","code","file","files",
        "there","any","get","set","their","them","being","been","be","has","have"}

def words(text):
    """Content words, with identifiers split so apiSignup -> api, signup."""
    out = []
    for raw in re.split(r"[^A-Za-z0-9]+", text):
        if not raw:
            continue
        for part in re.findall(r"[A-Z]+(?![a-z])|[A-Z][a-z]+|[a-z]+|[0-9]+", raw):
            p = part.lower()
            if len(p) > 2 and p not in STOP:
                out.append(p)
    return out

def corpus(repo):
    out = subprocess.run([JEV, "scan", "--list"], cwd=repo, capture_output=True, text=True)
    return [p for p in out.stdout.splitlines() if p.strip()]

# ---- methods: each returns a ranked list of paths ----

def m_grep(repo, query, files, k):
    terms = sorted(set(words(query)))
    if not terms:
        return []
    pat = "|".join(re.escape(t) for t in terms)
    scored = []
    for f in files:
        r = subprocess.run(["grep", "-ciE", pat, f], cwd=repo, capture_output=True, text=True)
        try:
            n = int(r.stdout.strip() or 0)
        except ValueError:
            n = 0
        if n:
            scored.append((n, f))
    scored.sort(reverse=True)
    return [f for _, f in scored[:k]]

_bm25_cache = {}

def bm25_index(repo, files):
    key = (repo, len(files))
    if key in _bm25_cache:
        return _bm25_cache[key]
    docs, df = {}, Counter()
    for f in files:
        try:
            text = open(os.path.join(repo, f), encoding="utf-8", errors="replace").read()
        except OSError:
            text = ""
        tf = Counter(words(text))
        docs[f] = tf
        for t in tf:
            df[t] += 1
    avgdl = sum(sum(tf.values()) for tf in docs.values()) / max(len(docs), 1)
    _bm25_cache[key] = (docs, df, avgdl)
    return _bm25_cache[key]

def m_bm25(repo, query, files, k, k1=1.5, b=0.75):
    docs, df, avgdl = bm25_index(repo, files)
    N = len(docs)
    q = words(query)
    scored = []
    for f, tf in docs.items():
        dl = sum(tf.values()) or 1
        s = 0.0
        for t in q:
            if t not in tf:
                continue
            idf = math.log(1 + (N - df[t] + 0.5) / (df[t] + 0.5))
            s += idf * tf[t] * (k1 + 1) / (tf[t] + k1 * (1 - b + b * dl / avgdl))
        if s > 0:
            scored.append((s, f))
    scored.sort(reverse=True)
    return [f for _, f in scored[:k]]

def _jev(repo, query, k, extra):
    r = subprocess.run([JEV, "find", "--json", "--min", "0", "-n", str(k), "--locate", "0"] + extra + [query],
                       cwd=repo, capture_output=True, text=True)
    try:
        d = json.loads(r.stdout)
    except json.JSONDecodeError:
        sys.stderr.write(f"  jev failed on {query!r}: {r.stderr.strip()[:200]}\n")
        return []
    return [(m["path"], m["score"]) for m in d.get("matches", [])]

def m_jev(repo, query, files, k):
    """The shipping configuration: screen every file, verify the leaders."""
    return _jev(repo, query, k, [])

def m_jev_screen(repo, query, files, k):
    """Ablation: skeleton screen only, no full-content verify pass."""
    return _jev(repo, query, k, ["--verify", "0"])

METHODS = {"grep": m_grep, "bm25": m_bm25, "jev": m_jev, "jev_screen": m_jev_screen}
JEV_METHODS = {"jev", "jev_screen"}
JEV_THRESHOLD = 0.6   # the tool's default --min

# ---- metrics ----

def evaluate(ranked, relevant, k):
    """P@1, recall@k and reciprocal rank of the first relevant file."""
    rel = set(relevant)
    top = ranked[:k]
    p1 = 1.0 if top and top[0] in rel else 0.0
    rec = len(rel & set(top)) / len(rel) if rel else None
    rr = 0.0
    for i, p in enumerate(top, 1):
        if p in rel:
            rr = 1.0 / i
            break
    return p1, rec, rr

def usage_offset():
    try:
        return os.path.getsize(USAGE_LOG)
    except OSError:
        return 0

def usage_since(offset):
    tok = 0
    try:
        with open(USAGE_LOG) as fh:
            fh.seek(offset)
            for line in fh:
                try:
                    tok += json.loads(line).get("in_tokens", 0)
                except json.JSONDecodeError:
                    pass
    except OSError:
        pass
    return tok

def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    opts = {a.split("=")[0]: a.split("=", 1)[1] for a in sys.argv[1:] if "=" in a and a.startswith("--")}
    methods = opts.get("--methods", "grep,bm25,jev").split(",")
    k = int(opts.get("--k", 5))

    agg = defaultdict(lambda: defaultdict(list))   # method -> category -> [(p1, rec, rr)]
    alarms = defaultdict(list)                     # method -> [n_results on negative queries]
    timing = defaultdict(float)
    tokens = defaultdict(int)
    rows = []

    for qf in args:
        spec = json.load(open(qf))
        repo = spec["repo"]
        files = corpus(repo)
        print(f"\n### {os.path.basename(repo)} — {len(files)} files, {len(spec['queries'])} queries")
        print(f"    {spec.get('description','')}")

        for q in spec["queries"]:
            line = f"  {q['id']:<26} {q['category']:<10}"
            for m in methods:
                off = usage_offset()
                t0 = time.time()
                out = METHODS[m](repo, q["query"], files, k)
                timing[m] += time.time() - t0
                if m in JEV_METHODS:
                    tokens[m] += usage_since(off)
                    ranked_all = out
                    ranked = [p for p, s in ranked_all]
                    shown = [p for p, s in ranked_all if s >= JEV_THRESHOLD]
                else:
                    ranked = out
                    shown = out

                if q["category"] == "negative":
                    alarms[m].append(len(shown))
                    line += f" | {m}:{len(shown)} shown"
                else:
                    p1, rec, rr = evaluate(ranked, q["relevant"], k)
                    agg[m][q["category"]].append((p1, rec, rr))
                    agg[m]["ALL"].append((p1, rec, rr))
                    line += f" | {m}: P@1={p1:.0f} R@{k}={rec:.2f}"
                    rows.append((os.path.basename(repo), q["id"], q["category"], m, p1, rec, rr))
            print(line)

    print("\n" + "=" * 78)
    print(f"{'method':<12}{'category':<12}{'n':>4}{'P@1':>8}{f'R@{k}':>8}{'MRR':>8}")
    print("-" * 78)
    for m in methods:
        for cat in ("vocab_gap", "ordinary", "ALL"):
            v = agg[m].get(cat)
            if not v:
                continue
            n = len(v)
            print(f"{m:<12}{cat:<12}{n:>4}{sum(x[0] for x in v)/n:>8.2f}"
                  f"{sum(x[1] for x in v)/n:>8.2f}{sum(x[2] for x in v)/n:>8.2f}")
        print("-" * 78)

    print("\nnegative controls — files offered for a feature that does not exist (lower is better)")
    for m in methods:
        a = alarms[m]
        if a:
            print(f"  {m:<6} mean {sum(a)/len(a):>5.1f} files   silent on {sum(1 for x in a if x==0)}/{len(a)}")

    print("\ncost and latency (whole run)")
    for m in methods:
        extra = f"   {tokens[m]:,} tokens  ${tokens[m]*0.042/1e6:.4f}" if m in JEV_METHODS else ""
        print(f"  {m:<6} {timing[m]:>7.1f}s{extra}")

    with open("bench_results.json", "w") as fh:
        json.dump(rows, fh, indent=1)

if __name__ == "__main__":
    main()
