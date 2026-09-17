---
name: jev
description: >
  Find code by describing what it does, and ask yes/no questions about files,
  without reading those files into context. Use when locating a feature, flow or
  concept whose name in the codebase is unknown or may differ from the words in
  the request ("where is the register flow", "which files touch rate limiting"),
  when a grep returned nothing useful, or when checking a property across many
  files would otherwise mean reading all of them. Backed by the TypeSafe Jev
  model at $0.042 per million tokens; it reads the files so the agent does not.
---

# jev — ask about code without reading it

Context is not paid for once. Every file read early in a session is re-sent with
every later request, so a 3k-token file read with 100 turns to go costs roughly
300k tokens before the session ends. `jev` reads files itself, sends them to a
model that costs ~120× less than reading them here, and returns a few lines.

Scanning a 400 KB repository costs about **$0.0025** and adds about **40 tokens**
to this context.

## Commands

```bash
jev find "<what you are looking for>" [path...]   # rank files, with line numbers
jev ask "<yes/no question>" <file|dir...>          # per-file probability + line
jev scan [path...]                                 # dry run: cost, no API call
jev gain                                           # what it has cost so far
```

### find

```
$ jev find "where is the register flow"
130 files scanned, 3 match:
  0.94  internal/app/api.go:120
  0.91  web/templates/join.html:14
  0.72  mobile/lib/screens/join.dart:88
```

Go straight to those lines with `Read` and an `offset`. Do not re-read the whole
file to confirm — that gives back exactly the tokens this saved.

**Pass a directory whenever you can narrow one.** The second argument is the
main control on how long the call takes, because `find` sends one request per
file and the service caps requests per minute — so the wall-clock is linear in
the number of files, at roughly 60 ms each, and no amount of concurrency moves
it. Measured on a 127-file repository:

| | files | time |
|---|---:|---:|
| `jev find "…"` | 127 | 9.4 s |
| `jev find "…" internal/` | 8 | **2.5 s** |

About 2 s of that is a floor — the verify and locate passes — so narrowing below
a handful of files buys nothing. Scope on what the request already tells you: a
backend concern rarely lives in `mobile/`, a screen rarely lives in `internal/`.
When there is no such clue, search the whole tree; a wrong scope costs a second
search, which is worse than the seconds it saved.

### ask

```
$ jev ask "does this build a SQL query by string concatenation?" internal/ -q
  0.88  yes     internal/app/store.go:212
```

`-q` prints only the files that answer yes. Use it to sweep a directory for a
property; it replaces reading every file.

This is the code-review use, and it is measured: over nine labelled questions on
three repositories it produced **no false positive** (precision 1.00, recall
0.96), against 0.74 precision for the regex a developer would have tried.
The gap is widest on rare properties — the needle-in-a-haystack audits where a
regex returns four wrong files for every two right ones. Answers are strongly
bimodal: true files land at 0.92-0.99, false ones at 0.02-0.13.

## When to use it

- **Locating a feature, flow or concept by description.** This is the main case,
  and it is strongest exactly where grep is weakest: the codebase names the thing
  differently, uses another natural language, or uses an internal term. A repo
  whose UI is in French has no `register` anywhere — but it has a signup flow.
- **After a grep came back empty or with 200 useless hits.**
- **Checking one property across many files** — "which of these call the network
  on the main thread", "which handlers skip auth".
- **Before reading a large file**, to find which part matters.

## When not to use it

- **An exact symbol you know the name of.** `Grep` is free, instant and exact.
  Reach for `jev` when the *name* is the unknown, not the location.
- **When the file must be read anyway** to edit it. Locating it is the win;
  editing still needs the real content.
- **As a source of explanations.** Jev returns numbers, never prose. It says
  *where* and *how likely*, never *why*. Do not describe its output as reasoning.

## Reading the scores

| score | meaning |
|---|---|
| ≥ 0.9 | strong — act on it |
| 0.6–0.9 | plausible — verify by reading the cited lines |
| < 0.6 | not reported by default |

A score printed with a trailing `~` was ranked from the file's declarations only
and never checked against its contents. Treat it as a lead, not an answer.

On a repository where the answer exists, correct files have measured 0.89–0.97
and the nearest wrong file 0.8 or below. A cluster of results all around 0.65
usually means the thing is not really there.

`no match in N files (best 0.21: path)` is a real and useful answer: the thing
probably is not in this tree. Say so rather than grepping ten more times.

A score is a calibrated probability, not a proof. When a match matters, open the
cited line — that is one small `Read`, not a file sweep.

## The Read hook

A `Read` of a file between 400 lines and 80 KB may come back narrowed to about a
fifth of the file, with a note saying so. That is this plugin: it located the
part that answers the current request and dropped the rest, to keep the other
four fifths out of this context.

Nothing was removed from the file on disk. If the part you need is not in the
window, read it again with an explicit `offset` or `limit` — an explicit window
is never second-guessed. Measured: 50 narrowed windows, none lost its target.

## Setup

Needs `TYPE_SAFE_AI_KEY` in the environment. If a command reports a missing key,
tell the user rather than falling back to reading every file silently.

Run `jev probe` once after installing to confirm the API contract, and
`jev scan` to see what a search would send and cost without sending it.
