# Results

30 queries across three repositories, 24 with a known answer plus 6 negative
controls. Methodology and the honesty notes are in `README.md`; re-run with
`python3 bench.py queries/*.json`. Raw output in `raw/`.

`repo A (Go + Flutter, French UI)` is the repository jev was developed against. `repo C (Go + Flutter)` and `repo B (Go + React PWA)`
were never opened during development. The five queries used while tuning are
excluded from all three sets.

## Pooled (24 answerable queries)

| method | P@1 | R@5 | MRR |
|---|---|---|---|
| grep | 0.08 | 0.29 | 0.23 |
| bm25 | 0.33 | 0.43 | 0.46 |
| jev_screen | 0.92 | 0.82 | 0.96 |
| **jev** | **0.96** | **0.84** | **0.98** |

Split by query kind, P@1:

| method | vocabulary gap (12) | ordinary (12) |
|---|---|---|
| grep | 0.08 | 0.08 |
| bm25 | 0.08 | 0.58 |
| **jev** | **0.92** | **1.00** |

The two columns are the whole story. On ordinary questions bm25 is a real
competitor — 0.58 P@1 for nothing per query. On questions whose wording never
appears in the code, both lexical methods collapse to 1 correct answer out of
12, which is roughly chance, while jev answers 11 of 12. Those queries were
built so the key noun appears in **zero** files, so no amount of lexical
cleverness reaches them.

## Per repository (8 answerable queries each)

| repo | | grep | bm25 | jev_screen | jev |
|---|---|---|---|---|---|
| repo B (Go + React PWA) (unseen) | P@1 | 0.12 | 0.38 | 1.00 | 1.00 |
| | R@5 | 0.10 | 0.46 | 0.75 | 0.75 |
| repo C (Go + Flutter) (unseen) | P@1 | 0.12 | 0.50 | 0.88 | 0.88 |
| | R@5 | 0.20 | 0.55 | 0.85 | 0.82 |
| repo A (Go + Flutter, French UI) (developed against) | P@1 | 0.00 | 0.12 | 0.88 | 1.00 |
| | R@5 | 0.57 | 0.27 | 0.86 | 0.94 |

jev does not do noticeably better on the repository it was built against, which
is the main thing this table is here to check.

## Negative controls

Six queries asking for a feature the repository genuinely lacks.

| method | files offered (mean) | correctly silent |
|---|---|---|
| grep | 5.0 | 0/6 |
| bm25 | 5.0 | 0/6 |
| **jev** | **0.0** | **6/6** |

Read this as a property of the ranking, not a prediction about agents. A lexical
method cannot report absence — some file always contains some query word — so
when the harness asks for a top 5 it produces one. jev returned nothing on all
six, at its default threshold of 0.6.

What that costs an agent is measured separately, in `tokenecon/TOKENS.md`, and
the mechanism there is not the one this table suggests. An agent does not read a
forced top-5 of irrelevant files. It looks at them, sees nothing relevant, and
tries another wording — and another. On one absent feature the grep-only agent
spent **20 turns and 328,699 tokens** establishing that a password-reset flow
does not exist, against 10 turns and 156,679 for the agent that asked jev once.

So the conclusion survives — being able to say "not here" is worth real tokens —
but the cost is paid in repeated searching, not in reading wrong files. Where the
two files disagree, `TOKENS.md` is the measurement of agent behaviour and this
table is only the ranking property behind it.

## Cost and latency

Whole benchmark, 30 queries over 469 files:

| method | time | cost |
|---|---|---|
| grep | 17s | 0 |
| bm25 | 0.2s | 0 |
| jev_screen | 349s | $0.138 |
| jev | 395s | $0.170 |

Per query: **~13 seconds and $0.006**. That buys roughly 150 files read
out-of-context and about 40 tokens returned into the agent's.

## Ablation: is the verify pass worth it?

`jev_screen` skips the second pass that re-scores leading candidates against
their full contents. Pooled, verify is worth **+1 correct top-1 answer out of
24** (0.92 → 0.96) for about 20% more cost and time.

That is inside the noise for 24 queries, and the per-repo picture is mixed:

- **repo A (Go + Flutter, French UI)** — verify clearly helps: P@1 0.88 → 1.00, R@5 0.86 → 0.94.
- **repo B (Go + React PWA)** — identical results; the screen was already perfect, so there was
  nothing to fix.
- **repo C (Go + Flutter)** — verify *hurt* twice. On `loading-spinner` it demoted the correct
  file out of first place, and on `guest-registration` recall fell 0.75 → 0.50.

So the honest summary is that the verify pass is not clearly earning its cost.
It is left on by default because its failures are recoverable — a demoted file
is usually still in the top 5 — while the screen's failures are not. Anyone
optimising for cost should try `--verify 0` first and lose very little.

## What this does not show

- 24 answerable queries is a small sample. A difference of one or two queries,
  such as the whole verify ablation, is not significant.
- One labeller per repository, not a consensus. "Relevant" is arguable for
  support files near a feature.
- Three repositories by one author, all Go backends with a Flutter or React
  frontend. Nothing here speaks to other stacks or to repositories of thousands
  of files, where one request per file becomes slow.
- Latency is real: 13s per query is far slower than grep, and jev is worth
  reaching for when grep has failed or the vocabulary is unknown, not by reflex.
