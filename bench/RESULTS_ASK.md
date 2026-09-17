# Results — `jev ask`

Nine questions across three repositories, each applied to every file in a
declared scope (12-34 files). Unlike the search benchmark this is a **per-file
binary classification**, so the metrics are precision, recall and F1 — and
precision is the one that decides whether an audit is usable at all. A checker
that flags fifteen innocent files to catch three real ones does not get used
twice.

Labels were written by agents that read the code and were forbidden from running
`jev`. Each was required to label *every* file in scope, not just the positives,
and to supply the regex a competent developer would plausibly try first — so the
grep baseline is their good-faith attempt, not one I wrote to lose.

Re-run with `python3 bench_ask.py queries_ask/*.json`.

## Overall (9 questions, 217 file judgments)

At the tool's threshold as shipped when measured (0.7):

| method | precision | recall | F1 |
|---|---|---|---|
| grep | 0.74 | 0.96 | 0.81 |
| **jev** | **1.00** | 0.96 | **0.97** |

By question kind (F1):

| kind | grep | jev |
|---|---|---|
| syntactic — basically greppable | 0.90 | **1.00** |
| semantic — needs reading the code | 0.82 | **0.92** |
| rare — true for 1-3 files in scope | 0.72 | **1.00** |

The shape matches the search benchmark. On greppable properties a regex is a
real competitor. The gap opens on rare properties, where grep's false alarms
dominate: on "which file mints a session token pair", grep returned 4 wrong
files for 2 right ones (P=0.33); on "insecure production default", 2 wrong for
2 right (P=0.50). **jev produced no false positive anywhere in the benchmark.**

## Where the threshold sits

The one miss: `verifies-caller-secret` scored recall 0.60, missing two files —
while its **AUC was 1.00**. The ranking was perfect; both missed files sat below
the cutoff. Every question's individually-optimal threshold was ≤ 0.50, and most
were ≤ 0.15.

Sweeping one global threshold over all nine:

| threshold | mean F1 | false neg | false pos |
|---|---|---|---|
| 0.15 | 0.950 | 0 | 3 |
| 0.25 | 0.962 | 0 | 2 |
| **0.50** | **0.988** | **1** | **0** |
| 0.70 | 0.972 | 2 | 0 |
| 0.90 | 0.930 | 5 | 0 |

0.50 weakly dominates 0.70 — one fewer missed file, still no false alarms — so
0.50 is the default.

One qualification. **This threshold is fitted to these nine questions**,
which is exactly the overfitting the search benchmark was built to avoid; 0.7
had simply been guessed, so 0.5 is better justified rather than well justified.
And the curve is nearly flat from 0.5 to 0.7, a difference of one file — this is
a correction, not a discovery.

## Why thresholding barely matters here

Scores are strongly bimodal. Across all nine questions:

| | true files | false files |
|---|---|---|
| typical range | 0.92 – 0.99 | 0.02 – 0.13 |
| worst case | 0.25 | 0.45 |

Seven of nine questions have a margin above 0.49 between their worst true file
and their best false one. The empty middle is why almost any cutoff between 0.2
and 0.7 gives nearly the same answer, and why the two genuinely hard cases —
a true file at 0.25, a false one at 0.45 — are the only ones that move.

## Cost

| | |
|---|---|
| 9 questions, 217 file judgments | $0.014, 21s |
| per question (12-34 files, full contents) | **~$0.0016, ~2s** |

`ask` sends whole files rather than skeletons, and is still cheaper per question
than `find`, because a scope is a directory rather than a repository.

## Limits

- Nine questions, three repositories, one labeller each. "Does this file itself
  check a secret, as opposed to relying on middleware" is a judgment call, and
  the two files jev scored lowest on that question are exactly the arguable ones.
- Scopes are hand-picked directories of 12-34 files. Behaviour over a scope of
  500 files is untested, and the threshold may not hold there — more files means
  more chances for one to land in the empty middle.
- All three repositories are Go backends with a Flutter or React frontend.
- The perfect precision is the headline, and nine questions is a thin basis for
  the word "perfect". Read it as "no false positive was observed", not as a rate.
