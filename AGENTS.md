# okupai_ai-ai

Two-machine AI setup:

- `mac-ai-mode/` — a Mac M1 Max runs `aimode`: starts/stops a local llama.cpp server
  (OpenAI-compatible API) and frees memory for it. Controlled over SSH:
  `ssh ai-mac aimode on|off|status|switch <profile>`.
- `nas-agents/` — the NAS side: agents run by hand in a console session and call that
  API over the LAN. No orchestration here — just the connection layer.

## Using the local LLM from here (aimode)

Before calling the API, connect once per shell session:

```bash
cd nas-agents
source scripts/connect.sh     # exports OPENAI_BASE_URL / OPENAI_API_KEY, checks /health
```

If `/health` fails, AI Mode is probably off: `ssh ai-mac aimode on [profile]`.

Exported env vars only last for that one real shell — if you're issuing requests from
separate, non-persistent invocations (e.g. one-off tool calls rather than an interactive
session), re-read `nas-agents/config.env` and pass the values explicitly each time instead
of relying on inherited env.

### Model profiles — pick based on the task

| Profile   | Use for                                             | Notes |
|-----------|------------------------------------------------------|-------|
| `fast`    | quick Q&A, short transforms, low latency              | ~2.5 GB, instant |
| `coding`  | code generation, agentic/tool-use tasks               | MoE, strong at code |
| `browser` | tasks that need to *see* a screenshot/page            | vision, needs mmproj |
| `brain`   | hardest reasoning, long/complex tasks, quality > speed| ~8-10 tok/s, slow to load |

Switch without leaving AI Mode: `ssh ai-mac aimode switch <profile>`. Switching reloads the
model (tens of seconds to minutes) — pick up front, don't switch mid-task.

Full API/auth details: `mac-ai-mode/docs/remote-access.md`.
