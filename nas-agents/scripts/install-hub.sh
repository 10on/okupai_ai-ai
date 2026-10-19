#!/usr/bin/env bash
# Hub side (run on the NAS):    install-hub.sh serve
#   installs and starts agent-hub as a systemd --user service (port HUB_PORT, default 7800)
# Agent side (every machine):   install-hub.sh agent <name> <hub-url> <token> [note]
#   registers the agent-hub MCP bridge in Claude Code and Codex. Create the token on the NAS
#   with:  node nas-agents/hub/hub.mjs add-agent <name>
set -euo pipefail
HUB="$(cd "$(dirname "${BASH_SOURCE[0]}")/../hub" && pwd)/hub.mjs"

case "${1:-}" in
serve)
    mkdir -p ~/.config/systemd/user
    sed "s|@HUB@|$HUB|" "$(dirname "$HUB")/../systemd/agent-hub.service" > ~/.config/systemd/user/agent-hub.service
    systemctl --user daemon-reload
    systemctl --user enable --now agent-hub.service
    systemctl --user --no-pager --lines=0 status agent-hub.service | head -3
    ;;
agent)
    NAME="${2:?name}"; URL="${3:?hub url, e.g. http://192.168.8.x:7800}"; TOKEN="${4:?token}"; NOTE="${5:-}"
    claude mcp remove -s user agent-hub >/dev/null 2>&1 || true
    claude mcp add -s user agent-hub -e HUB_URL="$URL" -e HUB_TOKEN="$TOKEN" -e HUB_NOTE="$NOTE" -- node "$HUB" mcp
    codex mcp remove agent-hub >/dev/null 2>&1 || true
    codex mcp add agent-hub --env HUB_URL="$URL" --env HUB_TOKEN="$TOKEN" --env HUB_NOTE="$NOTE" -- node "$HUB" mcp
    # Claude Code hooks: push new messages into a busy agent (Codex has no equivalent — it polls hub_inbox)
    SETTINGS=~/.claude/settings.json
    [[ -f "$SETTINGS" ]] || echo '{}' > "$SETTINGS"
    HUB_URL="$URL" HUB_TOKEN="$TOKEN" node -e '
      const fs=require("fs"),f=process.argv[1],s=JSON.parse(fs.readFileSync(f,"utf8"));
      const cmd=`HUB_URL=${process.env.HUB_URL} HUB_TOKEN=${process.env.HUB_TOKEN} node ${process.argv[2]} hook`;
      s.hooks??={};
      for(const ev of ["PostToolUse","UserPromptSubmit","Stop"]){
        s.hooks[ev]=(s.hooks[ev]||[]).filter(g=>!g.hooks?.some(h=>h.command?.includes("hub.mjs hook")));
        s.hooks[ev].push({hooks:[{type:"command",command:cmd,timeout:5}]});
      }
      fs.writeFileSync(f,JSON.stringify(s,null,2));' "$SETTINGS" "$HUB"
    chmod 600 "$SETTINGS"
    echo "registered agent-hub as '$NAME'"
    ;;
*) echo "usage: install-hub.sh serve | agent <name> <hub-url> <token> [note]" >&2; exit 2 ;;
esac
