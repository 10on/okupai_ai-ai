# okupai_ai-ai

Two-machine AI setup. This file describes the repo for working **on** it; it deliberately does not
tell agents to use the local LLM — that is opt-in per session via `/local` (see `nas-agents/`).

- `mac-ai-mode/` — Go CLI `aimode` for the Mac M1 Max: frees memory, starts/stops a local llama.cpp
  server (OpenAI-compatible API), model profiles in `cmd/aimode/defaults/models.json`. macOS-only.
- `nas-agents/` — NAS side: `aimode-mcp` (Go MCP server, gated off by default), the async job
  worker, the headless browser agent (`browser/`, Node + playwright-core), prompt templates and
  `scripts/install.sh` that registers everything in Claude Code and Codex.
- `.claude/commands/local.md`, `.claude/skills/local-*` — the `/local` command and offload skills
  (linked into `~/.claude` by install.sh).
- `docs/local-offload-spec.md` — the spec for the offload layer; `docs/benchmarks.md` — model numbers.

Build: `cd mac-ai-mode && go build -o bin/aimode ./cmd/aimode`;
`cd nas-agents/aimode-mcp && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ../bin/aimode-mcp .`

Testing anything that runs `aimode on`: use an isolated `AIMODE_HOME` whose config you have
verified (empty `apps`, `wired_limit_mb: 0`, another port) and `--dry-run` first — a real `on`
quits the Mac's apps.
