# Benchmark

Compares `jev find` against lexical baselines on labelled retrieval queries.

```bash
export JEV_BIN=./bin/jev          # or leave unset to use jev on PATH
python3 bench.py queries/*.json --k=5 --methods=grep,bm25,jev_screen,jev
```

**The query sets are not in this repository.** They were labelled against three
private repositories, so only the harnesses are published. Each takes any query
set in the shape documented below, and the Read-hook benchmark ships with its
real cases because it runs on Hugo and Prometheus, which are public:

```bash
git clone --depth 1 https://github.com/gohugoio/hugo /tmp/hugo
git clone --depth 1 https://github.com/prometheus/prometheus /tmp/prometheus
python3 hook/measure.py hook/cases_hugo.json hook/cases_prom.json
```

The transcript-reading scripts under `tokenecon/` need `JEV_BENCH_TASKS` set to
a directory of agent transcripts, which are specific to the machine that ran
them.

## Method

**Same corpus for everyone.** Every method ranks the file list `jev scan --list`
returns, so a difference in results is a difference in ranking and not in what
each tool decided to look at.

**Baselines.**

| name | what it is |
|---|---|
| `grep` | what an agent reaches for first: case-insensitive alternation over the query's content words, files ranked by number of matching lines |
| `bm25` | a standard lexical IR ranker (k1=1.5, b=0.75). Identifiers are split on camelCase and underscores, so `apiSignup` matches "signup" — this is a deliberately strong baseline, stronger than plain grep |
| `jev_screen` | ablation: jev's skeleton screen only, with the verify pass disabled |
| `jev` | the shipping two-stage configuration |

**Metrics.** P@1, recall@5 and MRR against the labelled files. Negative-control
queries are scored separately: they ask for a feature that does not exist, and
what is reported is how many files each method still offers.

## Ground truth

The labels were written by separate agents that explored each repository with
grep and by reading files, and were **forbidden from running `jev`** — the tool
under test must not grade itself. Each labelled file carries a `file:line`
justification. A file counts as relevant only if it implements or defines the
thing; files that merely import, call, style or test it do not.

Every query set has three kinds of query:

- **`vocab_gap`** — the query uses a word the code never uses. Each one's
  `absent_term` was verified to appear in **zero** files, so these cannot be
  solved lexically at all. This is the case the tool exists for.
- **`ordinary`** — plainly-worded questions that may share vocabulary with the
  code. Lexical methods should do well here.
- **`negative`** — a plausible feature the repository genuinely lacks. Tests
  whether a method can say "not here" instead of offering its best wrong guess.

## Honesty notes

- The five queries used while tuning jev (register flow, photo rotation,
  websocket chat, 4-digit codes, dark mode) are **excluded** from every query
  set. Nothing here was fitted to.
- `repo A (Go + Flutter, French UI)` is the repository jev was developed against; `repo C (Go + Flutter)` and `repo B (Go + React PWA)`
  were never opened during development. Read the per-repo numbers before the
  pooled ones.
- Ground truth is one labeller's judgment per repo, not a consensus. "Relevant"
  is genuinely arguable for support files near a feature.
- `bm25` always returns its top k when any query term matches, so on negative
  controls it cannot stay silent by construction. That is a real property of
  lexical ranking, not a rigged comparison — but it is why the negative-control
  section reports counts rather than a score.
