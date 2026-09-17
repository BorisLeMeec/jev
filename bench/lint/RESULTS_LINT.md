# Results — the semantic lint

A `PostToolUse` hook on `Edit`/`Write` that checks a change against the
project's own written conventions, declared in `.jev-rules.json` at the
repository root.

It is the opposite of the Read hook in the way that matters: it only ever **adds
a line**, never removes anything. A wrong answer costs a moment's attention, not
hidden code. That asymmetry is why it runs on every edit while the Read hook is
fenced in.

**This is the one part of the project that costs tokens rather than saving
them.** Its value is correctness, not economy, and the numbers below are split
accordingly.

## Quality

27 labelled changes against three rules taken from the project's own `CLAUDE.md`,
written by agents that read the codebase and were forbidden from running `jev`.
**11 of the 27 are near-misses**: changes that touch the same ground without
breaking the rule, which is where a keyword matcher fails and where this has to
earn its place.

| | cases | P | R | F1 |
|---|---|---|---|---|
| **all** | 27 | **1.00** | **1.00** | **1.00** |
| rule: raw-material-widget | 12 | 1.00 | 1.00 | 1.00 |
| rule: english-ui-copy | 8 | 1.00 | 1.00 | 1.00 |
| rule: template-missing-field | 7 | 1.00 | 1.00 | 1.00 |
| obvious cases | 16 | 1.00 | 1.00 | 1.00 |
| **near-misses** | 11 | 1.00 | 1.00 | 1.00 |

Score separation: violations 0.68-0.97, clean changes 0.02-0.51.

The near-misses it saw through:

| change | score |
|---|---|
| an English code comment sitting beside French UI copy | 0.51 |
| a `Container` used for padding, not as a button | 0.25 |
| `ScaffoldMessenger` called inside an existing `GradientPage` | 0.22 |
| English API field names in a payload map | 0.04 |
| the loanword "match" inside a French sentence | 0.03 |
| `Field` set but `Tab` omitted — the rule only requires `Field` | 0.04 |
| `Field` added to the same map one line later | 0.06 |

## The threshold is measured

`jev ask`'s 0.5 fires on the English-comment near-miss at 0.51 on this set — one
false alarm, precision 0.89 — so the lint sets its own.

| threshold | P | R | F1 | false alarms |
|---|---|---|---|---|
| 0.3 – 0.5 | 0.89 | 1.00 | 0.94 | 1 |
| **0.6** | **1.00** | **1.00** | **1.00** | **0** |
| 0.7 – 0.8 | 1.00 | 0.88 | 0.93 | 0 |

0.60 sits in the gap between the highest clean score (0.51) and the lowest real
violation (0.68). Fitted to 27 cases, so a rule can override it with its own
`threshold`.

## Cost

| | |
|---|---|
| per edit | **683 tokens, $0.000029** |
| 100 edits in a session | **$0.0029** |
| added to the agent's context | ~55 tokens, and only when a rule fires |
| fired on | 8 of 27 changes — exactly the 8 real violations |
| latency | 0.73 s, **async**, so the agent never waits for it |

An edit that breaks nothing adds nothing to the conversation. The context cost
is paid only on a hit, which is what makes running this on every edit reasonable.

## Limits

- 27 changes, three rules, one repository, and the changes were written for the
  benchmark rather than taken from real commits. They are realistic — the
  labellers read the codebase first — but they are not history.
- Perfect scores on 27 cases means "no error observed", not a rate. The English
  near-miss at 0.51 is 0.09 from firing; a slightly different comment would
  cross it.
- Rules are the user's to write, and a badly phrased rule will produce confident
  nonsense. The three here were rewritten from `CLAUDE.md` sentences that were
  already precise.
- The benefit — violations caught before they land — is not measured here. What
  is measured is that the linter is right when it speaks, and quiet otherwise.
