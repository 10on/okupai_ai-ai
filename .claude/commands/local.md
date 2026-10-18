---
description: Turn local-LLM offload (the Mac) on or off for this session — /local, /local strict, /local off
argument-hint: "[strict|off]"
---

The user wants to change local-LLM mode for this session. Argument: "$ARGUMENTS".

- Empty argument → mode `prefer`: offload where it pays, the cloud takes over if the Mac is unavailable.
- `strict` → mode `strict`: minimal cloud tokens; the cloud only plans and dispatches, local results are
  accepted without cloud verification (marked "unverified"; code is the exception — it still has to pass
  tests before it is committed), and if the Mac is unavailable work is queued (`local_submit`) or you stop
  and report. You never take the work over yourself.
- `off` → call `local_disable`, then ask whether to also shut AI Mode down on the Mac
  (`local_disable` with `shutdown=true`, which runs `aimode off`). Don't ask for confirmation to switch off.

For `prefer` / `strict`:
1. Ask the user one short question first, e.g. "Enable local LLM on the Mac for this session (mode: prefer)?"
   Mention for strict that there will be no cloud fallback and no cloud verification.
2. Only after an explicit yes: call `local_enable` with the mode, then `local_status`.
3. Report in two lines: mode, Mac availability, active profile (or "no model loaded" — models load on
   first use via `local_ensure_profile`).

If the `local_enable` tool is not available at all, the aimode MCP server isn't registered in this agent:
say so and point to `nas-agents/README.md` (scripts/install.sh).
