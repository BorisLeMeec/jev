# Token economy — `jev ask`

Same paired design as `TOKENS.md`, applied to auditing instead of searching.
Three audits, each run twice by a fresh agent with an identical prompt except
one line: **grep-only** versus **`jev ask` first**. Both were given the same file
scope and the same output format. Numbers come from each run's transcript.

Stability was confirmed before reporting: no measurement was taken until two
runs of the report, 100 seconds apart, hashed identically.

## Results

| task | arm | billed | into context | turns | P | R | F1 |
|---|---|---:|---:|---:|---:|---:|---:|
| repo B (Go + React PWA) / fire-and-forget (18 files) | grep | 373,302 | 3,519 | 20 | 0.75 | 1.00 | 0.86 |
| | **jev** | **274,069** | **1,755** | 16 | 1.00 | 1.00 | **1.00** |
| repo A (Go + Flutter, French UI) / auto network on display (18) | grep | 551,993 | 13,487 | 20 | 0.86 | 1.00 | 0.92 |
| | **jev** | **292,670** | **3,770** | 15 | 1.00 | 1.00 | **1.00** |
| repo C (Go + Flutter) / verifies caller secret (12) | grep | 343,853 | 10,573 | 15 | 1.00 | 1.00 | **1.00** |
| | **jev** | **220,640** | **2,806** | 12 | 1.00 | 0.80 | 0.89 |

| | grep | jev | |
|---|---:|---:|---|
| billed tokens | 423,049 | 262,460 | **−38%** |
| tokens into context | 9,193 | 2,777 | **−70%** |
| turns | 18.3 | 14.3 | −22% |
| tool calls | 10.0 | 7.7 | −23% |
| F1 | 0.93 | 0.96 | |

Median paired ratio **0.64**, cheaper on **3 of 3**.

## Why the context saving is so much larger than for search

`find` saved 43% of context; `ask` saves 70%. The difference is what the baseline
has to do. To locate a feature, grep often suffices — the agent greps, sees a
path, and stops. To decide whether eighteen files each have a semantic property,
there is no shortcut: the agent must **read all eighteen**. On the repo A (Go + Flutter, French UI) audit
the grep arm pulled 13,487 tokens of file content into its context. The jev arm
pulled 3,770.

That is the case this tool was built for, and it is the largest effect measured
anywhere in this benchmark suite.

## Quality was not identical, and the errors differ in kind

Not a clean tie. The grep arm **over-reported** — two false positives across the
three audits (`api/point_status.go`, `web/templates/chat.html`). The jev arm
**under-reported** once, missing `backend/internal/auth/password.go` on the
hardest question.

For an audit those are not equally bad. A false positive costs a minute of
reading; a false negative means the audit silently passed something. jev's error
is the worse kind, even though its F1 is higher. The miss is also the same file
the quality benchmark flagged as the hard case: `CheckPassword` performs the
bcrypt comparison but does not itself reject the request, so whether it qualifies
is a genuine judgment call rather than a clear error.

## Limits

- Three audits. The direction is consistent (3 of 3, and the context saving is
  large enough not to be noise) but this is not a rate.
- All three are semantic questions, chosen because that is where reading is
  unavoidable. On a purely greppable property the baseline would do far better —
  the quality benchmark shows grep reaching F1 0.90 on syntactic questions.
- Scopes are 12-18 files. A scope of 200 would cost more for jev in absolute
  terms while saving the baseline agent even more reading, so the ratio is
  untested at that size.
