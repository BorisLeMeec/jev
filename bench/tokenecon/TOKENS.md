# Token economy

The retrieval benchmark measures whether jev finds the right file. This one
measures the thing the tool actually exists for: does using it cost an agent
fewer tokens?

## Method

Seven tasks, each run twice by a fresh agent with an identical prompt except for
one line:

- **grep arm** — "use grep/rg, find, ls and reading files. The `jev` command must
  NOT be used."
- **jev arm** — "use `jev find` first; you may also grep and read files to confirm."

Both arms got the same stopping rule ("work until confident, then stop") and the
same output format, so neither was pushed to work longer than the other. Neither
was told the answer. Numbers come from each agent's own transcript, not an
estimate:

- **billed** — every input-side token the run paid for, cache reads included.
  Since each turn re-sends the conversation, this is the real bill and it grows
  faster than linearly with the number of turns.
- **into context** — the tokens tool results added to the conversation. This is
  the part re-read on every later turn, so it is what a search tool can remove.

## Results

Both arms answered **every task correctly** — recall 1.00 on all four tasks with
a real answer, and both correctly said "none" on all three absent features. The
comparison is therefore at equal quality.

| task | kind | grep | jev | ratio |
|---|---|---:|---:|---:|
| repo C (Go + Flutter) / member signs up for a game | vocab gap | 196,143 | 93,059 | 0.47 |
| repo C (Go + Flutter) / password reset | absent | 328,699 | 156,679 | 0.48 |
| repo B (Go + React PWA) / route optimisation | ordinary | 165,756 | 102,109 | 0.62 |
| repo A (Go + Flutter, French UI) / lobby screen | vocab gap | 166,065 | 109,380 | 0.66 |
| repo B (Go + React PWA) / poster stuck on a board | vocab gap | 184,846 | 147,252 | 0.80 |
| repo B (Go + React PWA) / billing | absent | 183,898 | 170,321 | 0.93 |
| repo A (Go + Flutter, French UI) / push notification | absent | 264,188 | 268,537 | 1.02 |

| | grep | jev | |
|---|---:|---:|---|
| billed tokens (mean) | 212,799 | 149,620 | **−30%** |
| tokens into context | 2,087 | 1,198 | −43% |
| turns | 12.4 | 9.1 | −26% |
| tool calls | 7.0 | 5.1 | −27% |
| recall | 1.00 | 1.00 | — |

Median paired ratio **0.66**; cheaper on 6 of 7 tasks; worst case 1.02, so jev
never meaningfully lost.

By kind: vocabulary gap −36%, ordinary −38%, absent feature −23%.

### Where the saving comes from

Not from smaller tool results — those are small either way, 2.1k tokens against
1.2k. It comes from **fewer turns**: 12.4 down to 9.1. Each turn re-sends the
whole conversation, so removing three turns from a twelve-turn task removes much
more than a quarter of the bill.

### Absent features too

A lexical search has no way to conclude that something is not there. It can only
keep trying more synonyms, and it does: on `repo C (Go + Flutter) / password reset` the grep
agent spent **20 turns and 328,699 tokens** establishing that a password-reset
flow does not exist, against 10 turns and 156,679 for the jev agent, which asked
once and got "no match". Two of the three absent-feature tasks favoured jev and
the third was level.

## Limits

- Seven paired runs, none repeated. Agent behaviour varies run to run, so a
  single task's ratio means little; the direction across all seven — cheaper on
  six, never meaningfully worse — is the sturdier signal.
- All tasks are pure search. An agent that must then *edit* the file has to read
  it anyway, and the saving shrinks.
- Sub-agent runs are short (2-12 turns). The re-read multiplier compounds with
  session length, so a long session should show a larger effect than measured
  here — but that is an extrapolation, not something these numbers demonstrate.
