# Results — the Read hook

A `PreToolUse` hook on `Read` that rewrites `offset` and `limit` so the agent
receives the part of a large file that answers what it is looking for. This is
the one thing a hook does that the CLI cannot: it applies without the agent
having to choose it.

It is also the only part of this project that can **hide code**. A window that
drops the answer is a silent omission — the agent reads what it was handed and
never learns the part it wanted was cut. Recall is therefore the only metric that
counts, and compression is worthless without it.

## Evidence

Two rounds. 12 targets in three 1210-1482 line files (repo A (Go + Flutter, French UI), repo B (Go + React PWA), repo C (Go + Flutter)),
then 40 targets in eight 1439-5020 line files (Hugo, Prometheus). Every target is
a `(goal, line)` pair written by an agent that read the file and was forbidden
from running `jev`, with one target in each fifth of each file — a tool that
always guessed "near the top" had to show up as wrong.

### Recall

| | narrowed | recall |
|---|---|---|
| 413-1191 lines (three repos, three languages) | 28 | **28 / 28** |
| 1210-2237 lines, fits one request | 23 | **23 / 23** |
| over 80 KB, split into sections | 11 | 8 / 11 |

**50 narrowed windows with no loss** whenever the file fits in a single request.
Every loss was a sectioned file.

The size band was measured against the coverage a random window would get, which
is the control that matters on a small file: a window covering 55% of a file
catches a randomly placed target 55% of the time, so a high recall proves nothing
by itself.

| window rule | recall | coverage (chance) | lift |
|---|---|---|---|
| min 300 (was shipped) | 32/32 | 55% | +45% |
| min 150 (now shipped) | 32/32 | 28% | +72% |
| min 60 | 32/32 | 20% | +80% |

And the size-independent measure: **the chosen line sat a median of 4 lines from
the real one**, 31 of 32 within 25 lines, all 32 within 60. The model is not
landing inside a generous window by luck; it is pointing at the line.

### Tokens

Three paired runs on Hugo: one agent reads the whole file, one reads the window
the hook produces, both answer the same question.

| | whole file | window | |
|---|---:|---:|---|
| billed input tokens | 111,916 | 65,616 | **−41%** |
| tokens into context | 12,190 | 2,722 | **−78%** |
| correct answers | 3/3 | 3/3 | |

Verified stable: two readings 90 s apart hashed identically.

## Two limits the large files set

**The model's context limit is about 32k input tokens.** Measured: a 113 KB
payload reported 31,014 input tokens and passed; 134 KB was refused with
`max_tokens_exceeded`. This is not in the documentation, so it is measured here
and every payload cap in the tool derives from it — `jev ask` sends at most
90,000 characters for this reason.

**Sectioning does not work for narrowing.** A file too large for one request is
cut into sections, each located independently, and the most confident section
wins. Measured, that loses the target 3 times in 11, because confidences from
different sections are not comparable — one loss scored 0.85. `find` and `ask`
keep the sectioned path, where a wrong line is a hint beside a correctly ranked
file; the hook refuses to narrow such files at all.

## The gate that works is size, not confidence

A Noul asking whether any chunk really implements the goal is **miscalibrated
for this job**: it reads 0.26 and 0.31 on windows that are correct, so it cannot
serve as the gate.

The gate is the Choice's confidence, which on small files separates cleanly — hits 0.71-0.98
against one miss at 0.42. **On large files that separation disappears**: across
the 22 windows narrowed before the size gate, hits ran 0.63-0.98 and misses
0.61-0.85, fully overlapping. Confidence alone would not have caught them.

So the load-bearing gate is the **size** gate — only files that fit in one
request are narrowed — and the 0.60 confidence floor is a second filter on top,
not the protection.

## Where it fires

Only on a file between `JEV_HOOK_MIN_LINES` (400) and 80 KB. Both bounds are
measured rather than chosen:

- The **400-line floor** is where the economics start to pay: a 500-line file
  saves ~2,000 tokens per read, which is 100,000 tokens across 50 turns. The
  recall data covers 413 lines upward with no loss, so the floor sits at the
  bottom of the measured range rather than above it.
- The **150-line minimum window** is centred on the pick, so it forgives ±75 —
  comfortably past the worst error observed (under 60). A larger minimum keeps
  73% of a 413-line file and saves almost nothing.
- The **80 KB ceiling** is the one bound that is a hard safety limit rather than
  a tuning choice, because beyond it the file must be sectioned and recall drops
  to 8/11.

How much of a repository the floor reaches, on the three local ones:

| repo | files ≥1500 lines | files ≥400 lines |
|---|---|---|
| repo A (Go + Flutter, French UI) | 1 | 2 |
| repo B (Go + React PWA) | 1 | 11 |
| repo C (Go + Flutter) | 0 | 6 |

## Shipping posture

**On by default.** `JEV_HOOK_DISABLE=1` turns every narrowing off without
uninstalling anything — one variable to reach for if a read ever looks like it
lost something.

Every gate fails toward doing nothing: disabled, not a `Read`, an explicit
offset or limit already given, file under 400 lines, file over 80 KB, no goal in
the transcript, no API key, confidence under 0.60, any error or timeout — all
pass the read through untouched. `JEV_HOOK_DEBUG=1` names the gate that fired,
because a hook that silently does nothing cannot be told apart from one that is
broken.

When it does narrow it says so in `additionalContext` and states how to re-read,
so a bad window is recoverable rather than silent.

## Limits

- 23 narrowed windows with no loss is "no failure observed", not a rate.
- The size ceiling is derived from one measured limit on one model version; if
  that limit moves, the constant is wrong and the gate silently widens.
- The three token pairs are single-file reads. An agent doing more work around
  the read would dilute the 41%.
- The ~1.25 s is synchronous, added to every read that passes the gates.
