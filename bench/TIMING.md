# Results — wall-clock

Read from the timestamps the paired agent runs already left behind, so this
costs nothing and measures the same runs the token numbers came from.

| | baseline | with jev | |
|---|---:|---:|---|
| `find` (grep-only vs jev-first), 7 pairs | 57 s | 39 s | −32% mean |
| `ask` (grep-only vs ask-first), 3 pairs | 52 s | 51 s | −3% |
| Read hook (whole file vs window), 3 pairs | 9 s | 9 s | −6% |

## The mean is a lie; the median is the answer

For `find` the per-pair ratios are 0.16, 0.90, 1.02, 1.03, 1.14, 1.73, 1.74.
The **median is 1.03** — a 3% slowdown. The −32% mean comes entirely from one
task, `repo C (Go + Flutter)/password-reset`, where the grep-only agent spent 215 s and twenty
turns establishing that a password-reset flow does not exist, against 34 s for
the agent that asked once. Drop that one case and the mean is +1.09.

So the honest statement is:

> **These tools save tokens, not time.** Wall-clock is a wash, except when the
> baseline would otherwise flounder — and then the difference is large.

That follows from the mechanics rather than contradicting them. A `jev find`
call costs ~13 s and removes two or three turns; a turn takes about that long.
The tokens those turns would have parked in context are gone for good, but the
clock barely notices.

## Per-call latency, for reference

| | |
|---|---|
| `jev find` | ~13 s (one request per file, plus a verify pass) |
| `jev ask` | ~2 s per question over a directory |
| Read hook | ~1.25 s, synchronous, added to a narrowed read |
| lint | ~0.73 s, **async**, so nothing waits on it |

## Limits

- The two arms of each pair ran concurrently, so absolute seconds include
  whatever contention there was. The ratio within a pair is the trustworthy
  number; the absolute times are not.
- Seven, three and three pairs. One outlier moved the `find` mean by 33 points,
  which is exactly how little it takes at this sample size.
- Latency is the one axis where the Read hook is a pure cost: it adds 1.25 s to
  a read and gives back no turns, because the agent was going to read the file
  either way.
