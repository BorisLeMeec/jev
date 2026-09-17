#!/usr/bin/env python3
"""Measure what one agent run actually cost, from its transcript.

Two numbers matter and they are not the same thing:

  billed      every input-side token the run paid for, cache reads included.
              Because each turn re-sends the conversation, this grows with the
              square of how much work the agent does — it is the real bill.
  in_context  the tokens tool results put into the conversation. This is the
              part that gets re-read on every later turn, so it is what a
              search tool can actually remove.
"""
import json, sys, collections

def stats(path):
    s = collections.Counter()
    tools = collections.Counter()
    tool_chars = collections.Counter()
    id2name = {}
    turns = 0

    for line in open(path, errors="replace"):
        try:
            o = json.loads(line)
        except Exception:
            continue
        msg = o.get("message") or {}
        typ = o.get("type")

        if typ == "assistant":
            u = msg.get("usage") or {}
            if u:
                turns += 1
                for k in ("input_tokens", "cache_creation_input_tokens",
                          "cache_read_input_tokens", "output_tokens"):
                    s[k] += u.get(k, 0) or 0
            for b in (msg.get("content") or []):
                if isinstance(b, dict) and b.get("type") == "tool_use":
                    id2name[b.get("id")] = b.get("name")
                    tools[b.get("name")] += 1

        elif typ in ("user", "attachment"):
            for b in (msg.get("content") or []):
                if isinstance(b, dict) and b.get("type") == "tool_result":
                    name = id2name.get(b.get("tool_use_id"), "?")
                    c = b.get("content")
                    if isinstance(c, list):
                        n = sum(len(x.get("text", "")) for x in c if isinstance(x, dict))
                    elif isinstance(c, str):
                        n = len(c)
                    else:
                        n = 0
                    tool_chars[name] += n

    billed = s["input_tokens"] + s["cache_creation_input_tokens"] + s["cache_read_input_tokens"]
    return {
        "billed_in": billed,
        "output": s["output_tokens"],
        "fresh_in": s["input_tokens"],
        "cache_write": s["cache_creation_input_tokens"],
        "cache_read": s["cache_read_input_tokens"],
        "turns": turns,
        "tool_calls": sum(tools.values()),
        "in_context": sum(tool_chars.values()) // 4,
        "by_tool": {k: v // 4 for k, v in tool_chars.most_common()},
        "tool_counts": dict(tools),
    }

if __name__ == "__main__":
    for p in sys.argv[1:]:
        st = stats(p)
        print(f"\n{p.split('/')[-1]}")
        for k in ("billed_in", "output", "fresh_in", "cache_write", "cache_read",
                  "turns", "tool_calls", "in_context"):
            print(f"  {k:<14}{st[k]:>12,}")
        print(f"  tools: {st['tool_counts']}")
        print(f"  in-context by tool: {st['by_tool']}")
