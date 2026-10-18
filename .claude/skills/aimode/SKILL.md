---
name: aimode
description: Use when deciding whether to delegate a task (chat completion, code generation, a long job) to the local LLM running on the Mac via the aimode OpenAI-compatible API, when picking which model profile fits a task (fast/coding/browser/brain), or when a NAS console session needs to reach that API. Covers connection setup, health-checking, and profile selection.
---

# aimode — local LLM on the Mac, reachable from the NAS

`mac-ai-mode/` runs an OpenAI-compatible API on the Mac (`aimode`), started/stopped with
`ssh ai-mac aimode on|off`. This skill is about *using* that API from here, not running it.

## Connect first

```bash
cd nas-agents
source scripts/connect.sh     # exports OPENAI_BASE_URL / OPENAI_API_KEY, checks /health
```

If `/health` fails, AI Mode is probably off — `ssh ai-mac aimode on [profile]`, or
`ssh ai-mac aimode status` to see what's running.

If you're calling the API across separate, non-persistent tool invocations rather than one
real shell session, exported env vars will **not** carry over between calls — re-read
`nas-agents/config.env` and pass the values explicitly each time:

```bash
set -a; source nas-agents/config.env; set +a
curl -fsS "http://$AI_MAC_HOST:${AI_MAC_PORT:-8080}/v1/chat/completions" \
  -H "Authorization: Bearer ${AI_MAC_API_KEY:-none}" \
  -d '{"model":"aimode","messages":[{"role":"user","content":"..."}]}'
```

## Picking a profile

| Profile   | Use for                                             | Notes |
|-----------|------------------------------------------------------|-------|
| `fast`    | quick Q&A, short transforms, low latency              | ~2.5 GB, instant |
| `coding`  | code generation, agentic/tool-use tasks               | MoE, strong at code |
| `browser` | tasks that need to *see* a screenshot/page            | vision, needs mmproj |
| `brain`   | hardest reasoning, long/complex tasks, quality > speed| ~8-10 tok/s, slow to load |

Switch without leaving AI Mode: `ssh ai-mac aimode switch <profile>`. Switching reloads the
model (tens of seconds to minutes) — pick the profile up front, don't switch mid-task.

Full API/auth/network details: `mac-ai-mode/docs/remote-access.md`.
