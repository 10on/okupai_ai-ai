# nas-agents

The NAS-side half of the setup: AI agents run **by hand, in a console session on the NAS**
(same way you'd run Claude Code) and talk to the `aimode` inference API on the Mac over the LAN.
There's no orchestration layer here on purpose — the Mac side (`mac-ai-mode/`) owns the model and
the API; this side just needs to be pointed at it correctly before each session.

Different tools/agents will be used over time (CLI coding agents, scripts, maybe a browser
agent later) — this directory only standardizes *how they connect*, not what they do.

## Setup

```bash
cd nas-agents
cp config.example.env config.env
# edit config.env: AI_MAC_HOST (and AI_MAC_API_KEY if the Mac's api_key is set)
```

## Before each session

```bash
source scripts/connect.sh
```

This exports `OPENAI_BASE_URL` / `OPENAI_API_KEY` for any OpenAI-compatible CLI tool and checks
`aimode`'s `/health` endpoint, so you find out immediately if AI Mode isn't on yet
(`ssh ai-mac aimode on`) instead of an agent failing confusingly mid-task.

## Notes

- `config.env` is gitignored — it holds a real LAN host/IP, keep it local.
- If a tool doesn't read `OPENAI_BASE_URL`/`OPENAI_API_KEY`, check its docs for the equivalent
  flags/env vars and point them at the same values by hand.
- See `../mac-ai-mode/docs/remote-access.md` for the Mac-side API details (auth, LAN-only, etc).

## If the agent running here is Claude Code or Codex

This repo's root `AGENTS.md` (imported by `CLAUDE.md`) teaches both tools about `aimode` and
the model profiles automatically, every session, with no extra step.

On top of that, for Claude Code specifically:

- `/aimode` — a command that connects, checks health, and lists the profiles on demand.
- The `aimode` skill (`.claude/skills/aimode/`) — same knowledge, but also auto-triggers when
  the task looks like it should be delegated to the local LLM, so Claude can suggest it unprompted.
