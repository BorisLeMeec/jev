# jev

**Answer questions about a codebase without reading it into the agent's context.**

A Claude Code plugin backed by [TypeSafe](https://typesafe.ai)'s Jev — a small
model that returns calibrated probabilities instead of prose, at $0.042 per
million tokens. It reads your files so the agent doesn't have to.

![jev intercepting a Claude Code session](docs/demo.gif)

*Every turn is checked. Only searches and large reads are taken over — an
`Update`, a `Bash`, a file under 400 lines pass straight through. The panels
in it are a diagram, not the UI: the plugin is a hook and draws nothing.*

```
$ jev find "where is the register flow"
127 files scanned, 4 match:
  0.95  internal/app/api.go:81
  0.95  mobile/lib/screens/join.dart:71
  0.95  internal/app/app.go:341
  0.91  web/templates/join.html
```

That repository contains the string `register` **zero times**. The flow is called
*signup*, and the UI is in French.

---


## Install

Needs Go 1.22+ and a [TypeSafe API key](https://typesafe.ai).

```bash
claude plugin marketplace add BorisLeMeec/jev
claude plugin install jev@jev
```

Then, once:

```bash
export TYPE_SAFE_AI_KEY=...   # put this in your shell profile
```

To use `jev` from your own terminal too (the plugin only puts it on `PATH`
inside Claude Code):

```bash
make install    # builds and copies to ~/.local/bin/jev
jev probe       # verify the API contract, prints raw + decoded response
```

## Why

An agent does not pay for context once. Every file it reads is re-sent with every
later request. Measured across nine real Claude Code sessions:

| | tokens |
|---|---:|
| cache reads — context re-sent | 422,164,852 |
| cache writes | 14,899,661 |
| output | 2,989,998 |
| **all tool results combined** | **196,317** |

Average context re-sent per turn: **183,740 tokens**. Tool output was **0.04%**
of the total.

![context re-sent vs tool output](docs/context.png)

So shrinking tool output is not where the savings are. A 3k-token file read with
100 turns to go costs ~300k tokens before the session ends. Keeping those bytes
**out of context in the first place** is the lever.

## Results

| | quality | tokens | wall-clock |
|---|---|---|---|
| [**`jev find`**](#jev-find--locate-code-by-describing-it) | P@1[^p1] **0.96** vs 0.33 for BM25, 0.08 for grep | −30% billed | ≈ 0 |
| [**`jev ask`**](#jev-ask--a-yesno-question-across-many-files) | precision **1.00**, F1 0.97 vs 0.81 for grep | −38% billed, −70% context | ≈ 0 |
| [**Read hook**](#the-read-hook--narrows-a-big-file-to-the-part-you-asked-about) | 50 narrowed windows, **0 lost targets** | −41% billed, −78% context | +1.25 s/read |

On queries whose wording never appears in the code — the case this exists for —
grep and BM25 both collapse to **0.08** P@1 while jev holds **0.92**. Each tool
carries its full evidence [below](#what-you-get), and every
number links to the file it came from.

![paired agent runs, grep-only against jev](docs/benchmark.png)

**These tools save tokens, not time.** Wall-clock is a wash: the ~13 s a `find`
costs roughly cancels the turns it removes. The tokens those turns would have
parked in context are gone for good; the clock barely notices. Full numbers in
[`bench/`](bench/).

## What you get

### `jev find` — locate code by describing it

```bash
jev find "where is the register flow"
jev find "the code that rotates uploaded photos" internal/
jev find "rate limiting" --min 0.8 -n 5
```

Every file is screened from its declarations, the leading candidates are
re-scored against their full contents, then the winners are split into chunks to
find the line. A score marked `~` skipped verification.

![jev find against grep and BM25](docs/find.png)

**Measured** — 30 queries, 3 repositories, 2 of them never opened during
development ([`RESULTS.md`](bench/RESULTS.md)):

| | grep | BM25 | **jev** |
|---|---:|---:|---:|
| P@1, 24 answerable queries | 0.08 | 0.33 | **0.96** |
| P@1, *vocabulary gap* (12) | 0.08 | 0.08 | **0.92** |
| P@1, ordinary phrasing (12) | 0.08 | 0.58 | **1.00** |
| R@5[^r5] | 0.29 | 0.43 | **0.84** |
| MRR[^mrr] | 0.23 | 0.46 | **0.98** |

A *vocabulary gap* query is one whose key noun appears in **zero** files. Both
lexical methods drop to 1 correct answer in 12 — chance — and no amount of
lexical cleverness reaches those queries. On ordinary phrasing BM25 is a real
competitor for nothing per query.

`no match in N files` is a real answer. On six queries asking for a feature the
repository genuinely lacks, **jev returned nothing 6 times out of 6**; grep and
BM25 offered 5 files every time, because a lexical method cannot report absence.

What that saves is measured on agents, not rankings — 7 tasks, each run twice
with an identical prompt except for one line
([`TOKENS.md`](bench/tokenecon/TOKENS.md)):

| | grep arm | jev arm | |
|---|---:|---:|---|
| billed input tokens (mean) | 212,799 | 149,620 | **−30%** |
| turns | 12.4 | 9.1 | −26% |
| correct answers | 7/7 | 7/7 | equal quality |

Cheaper on 6 of 7 tasks, worst case 1.02. The saving is **not** smaller tool
results — those are 2.1k against 1.2k — it is three fewer turns, each of which
re-sends the entire conversation.

### `jev ask` — a yes/no question across many files

```bash
jev ask "does this build a SQL query by string concatenation?" internal/ -q
jev ask "does this handler verify the caller's identity?" api/ -q
```

Sweeps a directory and returns only the files that answer yes, with the line.

![jev ask against a developer's regex](docs/ask.png)

**Measured** — 9 questions, 217 per-file judgments, against the regex the
labelling agent said a developer would try first
([`RESULTS_ASK.md`](bench/RESULTS_ASK.md)):

| | grep | **jev** |
|---|---:|---:|
| precision | 0.74 | **1.00** |
| recall | 0.96 | 0.96 |
| F1 | 0.81 | **0.97** |
| F1 on *rare* properties (true for 1-3 files) | 0.72 | **1.00** |

Precision is the metric that decides whether an audit is usable: a checker that
flags fifteen innocent files to catch three real ones does not get used twice.
**No false positive anywhere in the benchmark.** The gap opens on rare
properties, where grep's false alarms dominate — on "which file mints a session
token pair" it returned 4 wrong files for 2 right ones.

Scores are strongly bimodal — true files 0.92-0.99, false files 0.02-0.13 — so
the threshold barely matters. ~$0.0016 and ~2 s per question.

On agents, 3 paired audits ([`TOKENS_ASK.md`](bench/tokenecon/TOKENS_ASK.md)):

| | grep arm | jev arm | |
|---|---:|---:|---|
| billed input tokens | 423,049 | 262,460 | **−38%** |
| tokens entering context | 9,193 | 2,777 | **−70%** |
| F1 | 0.93 | 0.96 | |

Cheaper on 3 of 3. **This is the largest effect measured anywhere in the suite**,
and the reason is structural: to locate a feature grep often suffices, but to
decide whether eighteen files each have a semantic property there is no shortcut
— the baseline agent has to read all eighteen.

### The Read hook — narrows a big file to the part you asked about

A `Read` of a file between 400 lines and 80 KB comes back as
about a fifth of the file, centred on what answers the current request, with a
note saying so and how to re-read.

```bash
JEV_HOOK_DISABLE=1    # turn it off entirely
JEV_HOOK_DEBUG=1      # print which gate decided, on stderr
```

This is the only part that can *hide* code, so recall is the only metric that
counts and compression is worthless without it
([`RESULTS_HOOK.md`](bench/hook/RESULTS_HOOK.md)):

| | |
|---|---|
| windows narrowed, file fits one request | **50 / 50 targets kept** |
| distance from the chosen line to the real one | **median 4 lines**, 31/32 within 25 |
| recall vs. a random window of the same size | 100% vs **28% chance** — +72 points |
| billed input tokens, 3 paired runs on Hugo | 111,916 → 65,616 — **−41%** |
| tokens entering context | 12,190 → 2,722 — **−78%** |
| cost | +1.25 s on every read that passes the gates |

Targets were placed one in each fifth of each file, so a tool that always
guessed "near the top" had to show up as wrong. Every gate fails toward doing
nothing, and it never touches a read where you gave an explicit `offset`.

**The load-bearing gate is size, not confidence.** Above 80 KB a file must be
split into sections, and recall falls to 8/11 because confidences from different
sections are not comparable — so such files are refused rather than narrowed.

Over a session the saving compounds, because every later turn re-reads what the
earlier ones read:

![context per turn, with and without the Read hook, over 15 turns](docs/context-growth.png)

*Measured, not projected: four Claude Code sessions (Haiku 4.5), two per arm,
15 prompts each, every prompt reading a Go standard-library file between 400
lines and 80 KB — the case the hook is built for. 15/15 correct answers in every
session, and no extra tool calls with jev (15 per session, against 17 without).
A session that reads fewer large files will see a smaller gap.*

## Benchmarks

Everything below was labelled by agents that read the code themselves and were
**forbidden from running `jev`** — the tool does not grade its own homework.
Labels were required to cite `file:line`, and each set includes deliberate
near-misses.

| | what it measures | |
|---|---|---|
| [`bench/RESULTS.md`](bench/RESULTS.md) | retrieval quality, 30 queries, 3 repos | vs grep and BM25 |
| [`bench/RESULTS_ASK.md`](bench/RESULTS_ASK.md) | classification, 9 questions, 217 judgments | vs a developer's regex |
| [`bench/hook/RESULTS_HOOK.md`](bench/hook/RESULTS_HOOK.md) | window recall, 52 targets in 11 files | against chance |
| [`bench/tokenecon/TOKENS.md`](bench/tokenecon/TOKENS.md) | paired agent runs on `find`, real transcripts | tokens at equal quality |
| [`bench/tokenecon/TOKENS_ASK.md`](bench/tokenecon/TOKENS_ASK.md) | paired agent runs on `ask`, 3 audits | the largest effect measured |
| [`bench/TIMING.md`](bench/TIMING.md) | wall-clock of the same runs | the one that says "no" |

The hook benchmark runs on **public repositories** (Hugo and Prometheus) and is
reproducible:

```bash
git clone --depth 1 https://github.com/gohugoio/hugo /tmp/hugo
git clone --depth 1 https://github.com/prometheus/prometheus /tmp/prometheus
python3 bench/hook/measure.py bench/hook/cases_hugo.json bench/hook/cases_prom.json
```

The `find` and `ask` query sets were labelled against private repositories and
are not published; the harnesses are, and take any query set in the same shape.

## Cost

`jev gain` shows where it went:

```
jev — token leverage
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Runs:                  105
  Examined by jev:       3.4M tokens, out of context
  Returned to the agent: 28.8K tokens, into context

  examined  ████████████████████████████████████████  3.4M
  returned  ▏░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  28.8K

  118× leverage — 118 tokens read for every 1 added to the conversation

By command
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  command    runs  requests   examined       cost   per run  share
  find         82     12.1K       2.9M      $0.41   $0.0050  ████████████
  ask          23       488     469.2K      $0.03   $0.0015  █░░░░░░░░░░░

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Spent on jev:                        $0.45
  Same tokens at Opus 5 input rates:   $17.08 (38× more, read once)
```

"Leverage" is not "saving", and the tool says so in its own output: an
agent would not have read every byte jev did, and the comparison assumes
those bytes entered the context once when in practice they are re-read
every turn.

| | |
|---|---|
| scanning a 434 KB repository | $0.0025 |
| `jev ask` over a directory | ~$0.0016 per question |
| a narrowed read | ~$0.0001 |

Developing this, running the benchmark suites and about two hundred
measurements cost **$0.45** in total.

## Limits

- **Grep is still better** for an exact symbol you can name: free, instant,
  exact. Reach for `jev` when the *name* is the unknown.
- **Jev never explains.** It answers *where* and *how likely*, never *why*.
- **Scores are calibrated probabilities, not proofs.** Open the cited line when
  it matters.
- Sample sizes are small — tens of queries, not thousands. Where a result says
  "1.00", read it as *no error observed*, not as a rate.
- Every benchmark repository is a Go backend with a Flutter or React frontend,
  plus Hugo and Prometheus. Nothing here speaks to other stacks.
- Thresholds were fitted on those samples and are all overridable by flag or
  environment variable.

## How it works

1. **Reduce** — each file becomes a skeleton: package, imports, declarations,
   routes, form fields. 434 KB of source becomes 34 KB.
2. **Screen** — one Noul question per file, one file per request, against a
   state holding exactly that file.
3. **Verify** — the leaders are re-scored on their full contents.
4. **Locate** — the winner is chunked and a Choice picks which chunk implements
   the goal.

The model returns probabilities, never prose. Every threshold lives in code and
is adjustable.

## Layout

```
cmd/jev/              CLI entry point
internal/typesafe/    API client (Noul / Choice / Score)
internal/scan/        tree walk and skeleton extraction
internal/run/         find, ask, scan, probe, the Read hook
internal/usage/       accounting behind `jev gain`
skills/jev/SKILL.md   tells the agent when to reach for this
hooks/hooks.json      PreToolUse on Read
bench/                every benchmark, harness and result
```

## License

MIT

---

[^p1]: ***P@1*** (*[precision at k](https://en.wikipedia.org/wiki/Evaluation_measures_(information_retrieval)#Precision_at_k)*, k=1) — the share of queries whose **first** result is correct. All-or-nothing per query: right file in first place scores 1, in second place scores 0. It is the metric that matches what an agent does, since it opens the first file it is handed. Computed over the 24 answerable queries only — a query whose feature does not exist has no correct first result, so the six negative controls are scored separately.

[^r5]: ***R@5*** (*[recall at k](https://en.wikipedia.org/wiki/Precision_and_recall#Recall)*, k=5) — of all the files a labeller marked relevant, the share that appear anywhere in the top 5. Forgiving about position, demanding about completeness: it is the one that notices when a feature is spread across a handler, a screen and a template and the ranking found only one of them.

[^mrr]: ***MRR*** (*[mean reciprocal rank](https://en.wikipedia.org/wiki/Mean_reciprocal_rank)*) — the mean of 1/rank of the first correct result, so first place is worth 1, second 0.5, third 0.33. The compromise between the two above: it tells "missed by one place" apart from "nowhere near", which P@1 scores identically at zero.
