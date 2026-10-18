#!/usr/bin/env bash
# Install the NAS side of local-LLM offload for Claude Code and Codex (idempotent):
#   - `Host ai-mac` in ~/.ssh/config
#   - browser agent dependency (playwright-core, uses the cached Chromium)
#   - aimode MCP server registered in Claude Code (user scope) and Codex
#   - /local command + local-* skills linked into ~/.claude, /local prompt into ~/.codex
#   - async job worker as a systemd --user service
# Build the binary first (on any machine with Go):
#   cd nas-agents/aimode-mcp && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ../bin/aimode-mcp .
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # nas-agents
REPO="$(dirname "$ROOT")"
BIN="$ROOT/bin/aimode-mcp"
ENV_FILE="$ROOT/config.env"

[[ -x "$BIN" ]] || { echo "missing $BIN — build it first (see header)" >&2; exit 1; }
[[ -f "$ENV_FILE" ]] || { echo "missing $ENV_FILE — copy config.example.env" >&2; exit 1; }
set -a; source "$ENV_FILE"; set +a
: "${AI_MAC_HOST:?AI_MAC_HOST not set in config.env}"

echo "== ssh alias ai-mac"
mkdir -p ~/.ssh && chmod 700 ~/.ssh
touch ~/.ssh/config && chmod 600 ~/.ssh/config
if ! grep -qE '^Host ai-mac$' ~/.ssh/config; then
    : "${AI_MAC_USER:?AI_MAC_USER not set in config.env}"
    cat >> ~/.ssh/config <<CFG

Host ai-mac
    HostName $AI_MAC_HOST
    User $AI_MAC_USER
    ServerAliveInterval 30
    ServerAliveCountMax 3
CFG
    echo "added Host ai-mac"
fi
ssh -o BatchMode=yes -o ConnectTimeout=5 ai-mac true && echo "ssh ai-mac ok"

echo "== browser agent"
(cd "$ROOT/browser" && npm install --omit=dev --no-audit --no-fund --silent)

echo "== MCP: Claude Code"
claude mcp remove -s user aimode >/dev/null 2>&1 || true
claude mcp add -s user aimode -- "$BIN"

echo "== MCP: Codex (lists all tools, hard-rejects while off — in case list_changed is ignored)"
codex mcp remove aimode >/dev/null 2>&1 || true
codex mcp add aimode --env AIMODE_MCP_LIST_ALL=1 -- "$BIN"

echo "== /local command and skills"
mkdir -p ~/.claude/commands ~/.claude/skills ~/.codex/prompts
ln -sfn "$REPO/.claude/commands/local.md" ~/.claude/commands/local.md
ln -sfn "$REPO/.claude/commands/local.md" ~/.codex/prompts/local.md
for d in "$REPO"/.claude/skills/local-*; do
    ln -sfn "$d" ~/.claude/skills/"$(basename "$d")"
done

echo "== job worker (systemd --user)"
mkdir -p ~/.config/systemd/user
sed "s|@BIN@|$BIN|" "$ROOT/systemd/aimode-worker.service" > ~/.config/systemd/user/aimode-worker.service
systemctl --user daemon-reload
systemctl --user enable --now aimode-worker.service
systemctl --user --no-pager --lines=0 status aimode-worker.service | head -3
loginctl show-user "$USER" -p Linger | grep -q yes || \
    echo "note: run 'sudo loginctl enable-linger $USER' so the worker keeps running without a login session"

echo "done. In an agent session: /local"
